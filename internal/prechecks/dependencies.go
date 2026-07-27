package prechecks

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/logging"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/model"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/parallel"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/service"
)

// fsvmNamePattern matches the VM names Nutanix Files gives its FSVMs:
// "NTNX-<file server name>-<index>". It is the fallback signal when the Prism
// Element file server inventory is unavailable and therefore the exact subnet
// UUIDs are unknown.
var fsvmNamePattern = regexp.MustCompile(`(?i)^NTNX-(.+)-\d+$`)

// DependencyIndex records which subnets are consumed by a Nutanix Files instance
// and which VMs on those subnets belong to a protection domain or a Prism
// Central protection policy.
type DependencyIndex struct {
	// FileServerSubnets maps a subnet UUID to the file servers using it.
	FileServerSubnets map[string][]string
	// FSVMsBySubnet maps a subnet UUID to the FSVM names found on it.
	FSVMsBySubnet map[string][]string
	// ProtectionDomainsByVM maps a VM UUID to the protection domains holding it.
	ProtectionDomainsByVM map[string][]string
	// ProtectionPolicyByVM maps a VM UUID to how Prism Central reports it as
	// protected.
	ProtectionPolicyByVM map[string]model.ProtectionInfo
	// Incomplete carries warnings for checks that could not be completed.
	Incomplete []model.Finding
}

// BuildDependencyIndex correlates the Files and data protection inventories with
// the VMs found on the candidate subnets.
//
// Two sources are used for Files, in order of precision:
//
//  1. The Prism Element file server inventory, which names the internal and
//     client subnets outright. This is authoritative.
//  2. FSVM name matching against the Prism Central file server list, used when
//     the first source is unavailable. It is a heuristic, so a match it finds
//     alone is reported as such.
//
// For data protection the Prism Central protected-resource lookup covers
// protection policies, and the Prism Element protection domain listing covers
// the legacy async DR configuration. Both are consulted because a subnet can be
// referenced by either.
func BuildDependencyIndex(ctx context.Context, pc service.PrismCentral, inv *Inventory, candidates []model.Subnet, log *logging.Logger, concurrency int) *DependencyIndex {
	idx := &DependencyIndex{
		FileServerSubnets:     map[string][]string{},
		FSVMsBySubnet:         map[string][]string{},
		ProtectionDomainsByVM: map[string][]string{},
		ProtectionPolicyByVM:  map[string]model.ProtectionInfo{},
	}
	idx.Incomplete = append(idx.Incomplete, inv.Incomplete...)

	for _, fsn := range inv.FileServerNetworks {
		name := fsn.FileServerName
		if name == "" {
			name = fsn.FileServerExtID
		}
		for _, subnetExtID := range fsn.SubnetExtIDs {
			idx.FileServerSubnets[subnetExtID] = appendUnique(idx.FileServerSubnets[subnetExtID], name)
		}
	}

	fileServerNames := make([]string, 0, len(inv.FileServers))
	for _, fs := range inv.FileServers {
		if fs.Name != "" {
			fileServerNames = append(fileServerNames, fs.Name)
		}
	}
	for _, subnet := range candidates {
		for _, vnic := range inv.VnicsBySubnet[subnet.ExtID] {
			vm, ok := inv.VMsByExtID[vnic.VMExtID]
			if !ok {
				continue
			}
			if fsName, matched := matchFSVM(vm, fileServerNames); matched {
				idx.FSVMsBySubnet[subnet.ExtID] = appendUnique(idx.FSVMsBySubnet[subnet.ExtID], fmt.Sprintf("%s (file server %s)", vm.Name, fsName))
			}
		}
	}

	for _, pd := range inv.ProtectionDomains {
		for _, vmExtID := range pd.VMExtIDs {
			idx.ProtectionDomainsByVM[vmExtID] = appendUnique(idx.ProtectionDomainsByVM[vmExtID], pd.Name)
		}
	}

	// The protected-resource lookup is per entity, so only VMs on candidate
	// subnets are queried, de-duplicated across subnets.
	vmExtIDs := candidateVMExtIDs(inv, candidates)
	var (
		mu          sync.Mutex
		unsupported bool
	)
	err := parallel.ForEach(ctx, concurrency, vmExtIDs, func(ctx context.Context, vmExtID string) error {
		info, err := pc.GetProtectionInfo(ctx, vmExtID)
		switch {
		case err == nil:
			mu.Lock()
			idx.ProtectionPolicyByVM[vmExtID] = info
			mu.Unlock()
			return nil
		case errors.Is(err, service.ErrNotFound):
			// The overwhelmingly common case: the VM is not protected.
			return nil
		case errors.Is(err, service.ErrUnsupported):
			mu.Lock()
			unsupported = true
			mu.Unlock()
			return nil
		default:
			return fmt.Errorf("read protection status of VM %s: %w", vmExtID, err)
		}
	})
	if err != nil {
		log.Warnf("Could not complete the Prism Central protection lookup: %v", err)
		idx.Incomplete = append(idx.Incomplete, incompleteFinding(model.CheckProtectionDomain,
			fmt.Sprintf("The Prism Central protection lookup did not complete: %v", err)))
	}
	if unsupported {
		idx.Incomplete = append(idx.Incomplete, incompleteFinding(model.CheckProtectionDomain,
			"Prism Central does not serve the data protection namespace, so protection policy usage could not be verified"))
	}

	return idx
}

// CheckDependencies flags subnets used by a Nutanix Files instance or by VMs
// under data protection. Either can break a migration mid-flight, so both are
// blockers, but the remediation offers the cheaper option too: leave the subnet
// out of this batch.
func CheckDependencies(subnet model.Subnet, inv *Inventory, idx *DependencyIndex) []model.Finding {
	var findings []model.Finding

	if servers := idx.FileServerSubnets[subnet.ExtID]; len(servers) > 0 {
		findings = append(findings, model.Finding{
			Check:       model.CheckFileServer,
			Severity:    model.SeverityBlocker,
			SubnetExtID: subnet.ExtID,
			SubnetName:  subnet.Name,
			Message: fmt.Sprintf("Nutanix Files instance(s) %s are configured to use this subnet; migrating it would disrupt the file server",
				strings.Join(servers, ", ")),
			Remediation: model.RemediationExcludeOrTicket,
		})
	} else if fsvms := idx.FSVMsBySubnet[subnet.ExtID]; len(fsvms) > 0 {
		// Name matching only, so the message says so rather than asserting it.
		findings = append(findings, model.Finding{
			Check:       model.CheckFileServer,
			Severity:    model.SeverityBlocker,
			SubnetExtID: subnet.ExtID,
			SubnetName:  subnet.Name,
			Message: fmt.Sprintf("VM(s) %s on this subnet match the Nutanix Files FSVM naming convention, so a file server appears to use this subnet",
				strings.Join(fsvms, ", ")),
			Remediation: model.RemediationExcludeOrTicket,
		})
	}

	for _, vnic := range inv.VnicsBySubnet[subnet.ExtID] {
		if vnic.VMExtID == "" {
			continue
		}
		if pds := idx.ProtectionDomainsByVM[vnic.VMExtID]; len(pds) > 0 {
			findings = append(findings, model.Finding{
				Check:       model.CheckProtectionDomain,
				Severity:    model.SeverityBlocker,
				SubnetExtID: subnet.ExtID,
				SubnetName:  subnet.Name,
				VMExtID:     vnic.VMExtID,
				VMName:      vnic.VMName,
				VnicExtID:   vnic.ExtID,
				Message: fmt.Sprintf("VM %s on this subnet belongs to protection domain(s) %s; the migration can disrupt replication for it",
					describeVM(vnic), strings.Join(pds, ", ")),
				Remediation: model.RemediationExcludeOrTicket,
			})
		}
		if info, ok := idx.ProtectionPolicyByVM[vnic.VMExtID]; ok {
			findings = append(findings, model.Finding{
				Check:       model.CheckProtectionDomain,
				Severity:    model.SeverityBlocker,
				SubnetExtID: subnet.ExtID,
				SubnetName:  subnet.Name,
				VMExtID:     vnic.VMExtID,
				VMName:      vnic.VMName,
				VnicExtID:   vnic.ExtID,
				Message:     fmt.Sprintf("VM %s on this subnet is a protected resource in Prism Central%s; the migration can disrupt replication for it", describeVM(vnic), describeProtection(info)),
				Remediation: model.RemediationExcludeOrTicket,
			})
		}
	}

	return dedupeFindings(findings)
}

func describeProtection(info model.ProtectionInfo) string {
	var parts []string
	if info.ConsistencyGroupExtID != "" {
		parts = append(parts, "consistency group "+info.ConsistencyGroupExtID)
	}
	if len(info.CategoryFqNames) > 0 {
		parts = append(parts, "categories "+strings.Join(info.CategoryFqNames, ", "))
	}
	if len(info.ProtectedBy) > 0 {
		parts = append(parts, strings.Join(info.ProtectedBy, ", "))
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, "; ") + ")"
}

// matchFSVM reports whether a VM looks like a Files FSVM. It requires the name
// pattern to resolve to a file server that actually exists, which keeps a VM
// coincidentally named "NTNX-something-1" from being flagged.
func matchFSVM(vm model.VM, fileServerNames []string) (string, bool) {
	m := fsvmNamePattern.FindStringSubmatch(vm.Name)
	if m == nil {
		return "", false
	}
	candidate := m[1]
	for _, name := range fileServerNames {
		if strings.EqualFold(name, candidate) {
			return name, true
		}
	}
	// An agent VM with the FSVM naming shape is still worth surfacing even when
	// the Prism Central file server list could not be read.
	if vm.IsAgentVM && len(fileServerNames) == 0 {
		return candidate, true
	}
	return "", false
}

func candidateVMExtIDs(inv *Inventory, candidates []model.Subnet) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, subnet := range candidates {
		for _, vnic := range inv.VnicsBySubnet[subnet.ExtID] {
			if vnic.VMExtID == "" {
				continue
			}
			if _, ok := seen[vnic.VMExtID]; ok {
				continue
			}
			seen[vnic.VMExtID] = struct{}{}
			out = append(out, vnic.VMExtID)
		}
	}
	sort.Strings(out)
	return out
}

func appendUnique(list []string, value string) []string {
	for _, v := range list {
		if v == value {
			return list
		}
	}
	return append(list, value)
}

func dedupeFindings(in []model.Finding) []model.Finding {
	seen := map[string]struct{}{}
	out := make([]model.Finding, 0, len(in))
	for _, f := range in {
		key := fmt.Sprintf("%s|%s|%s|%s|%s", f.Check, f.SubnetExtID, f.VMExtID, f.VnicExtID, f.Message)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, f)
	}
	return out
}
