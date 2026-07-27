package prechecks

import (
	"fmt"
	"sort"
	"strings"

	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/model"
)

// DuplicateScope describes where a MAC address collision happens, because the
// three shapes read very differently in a report.
type DuplicateScope string

const (
	// ScopeSameVM is two vNICs of one VM sharing a MAC address.
	ScopeSameVM DuplicateScope = "same VM"
	// ScopeSameCluster is two VMs on one cluster sharing a MAC address.
	ScopeSameCluster DuplicateScope = "same cluster"
	// ScopeAcrossClusters is two VMs on different clusters registered to the
	// same Prism Central sharing a MAC address.
	ScopeAcrossClusters DuplicateScope = "across clusters"
)

// DuplicateMAC is one MAC address seen more than once.
type DuplicateMAC struct {
	MACAddress  string
	Scopes      []DuplicateScope
	Occurrences []MACOccurrence
}

// FindDuplicateMACs returns every MAC address that appears on more than one
// vNIC in the Prism Central inventory.
//
// This is the check that the existing Python tooling performs by shelling into a
// CVM and calling nuclei. Building the index from the v4 VM and vNIC listings
// gets the same answer over the supported API surface, which is what makes the
// tool runnable from an operator's workstation.
func FindDuplicateMACs(inv *Inventory) []DuplicateMAC {
	var out []DuplicateMAC
	for _, occurrences := range inv.MACOccurrences {
		if len(occurrences) < 2 {
			continue
		}
		out = append(out, DuplicateMAC{
			MACAddress:  occurrences[0].MACAddress,
			Scopes:      classifyScopes(occurrences),
			Occurrences: occurrences,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return model.NormalizeMAC(out[i].MACAddress) < model.NormalizeMAC(out[j].MACAddress)
	})
	return out
}

func classifyScopes(occurrences []MACOccurrence) []DuplicateScope {
	var (
		vms      = map[string]struct{}{}
		clusters = map[string]struct{}{}
		perVM    = map[string]int{}
	)
	for _, o := range occurrences {
		vms[o.VMExtID] = struct{}{}
		clusters[o.ClusterExtID] = struct{}{}
		perVM[o.VMExtID]++
	}

	var scopes []DuplicateScope
	for _, count := range perVM {
		if count > 1 {
			scopes = append(scopes, ScopeSameVM)
			break
		}
	}
	if len(clusters) > 1 {
		scopes = append(scopes, ScopeAcrossClusters)
	} else if len(vms) > 1 {
		scopes = append(scopes, ScopeSameCluster)
	}
	if len(scopes) == 0 {
		scopes = append(scopes, ScopeSameVM)
	}
	return scopes
}

// CheckDuplicateMACs turns the duplicate index into findings for one subnet. A
// subnet is affected when any vNIC attached to it carries a duplicated address,
// even if the other sighting is on a different cluster.
func CheckDuplicateMACs(subnet model.Subnet, inv *Inventory, duplicates []DuplicateMAC) []model.Finding {
	byMAC := make(map[string]DuplicateMAC, len(duplicates))
	for _, d := range duplicates {
		byMAC[model.NormalizeMAC(d.MACAddress)] = d
	}

	var (
		findings []model.Finding
		reported = map[string]struct{}{}
	)
	for _, vnic := range inv.VnicsBySubnet[subnet.ExtID] {
		key := model.NormalizeMAC(vnic.MACAddress)
		if key == "" {
			continue
		}
		duplicate, ok := byMAC[key]
		if !ok {
			continue
		}
		if _, seen := reported[key]; seen {
			continue
		}
		reported[key] = struct{}{}

		findings = append(findings, model.Finding{
			Check:       model.CheckDuplicateMAC,
			Severity:    model.SeverityBlocker,
			SubnetExtID: subnet.ExtID,
			SubnetName:  subnet.Name,
			VMExtID:     vnic.VMExtID,
			VMName:      vnic.VMName,
			VnicExtID:   vnic.ExtID,
			MACAddress:  vnic.MACAddress,
			Message: fmt.Sprintf("MAC address %s is used by %d vNICs (%s): %s",
				vnic.MACAddress, len(duplicate.Occurrences), joinScopes(duplicate.Scopes), describeOccurrences(duplicate.Occurrences)),
			Remediation: model.RemediationSupportTicket,
		})
	}
	return findings
}

func joinScopes(scopes []DuplicateScope) string {
	parts := make([]string, 0, len(scopes))
	for _, s := range scopes {
		parts = append(parts, string(s))
	}
	return strings.Join(parts, " and ")
}

func describeOccurrences(occurrences []MACOccurrence) string {
	parts := make([]string, 0, len(occurrences))
	for _, o := range occurrences {
		vm := o.VMName
		if vm == "" {
			vm = o.VMExtID
		}
		if vm == "" {
			vm = "(unknown VM)"
		}
		label := vm
		if o.ClusterName != "" {
			label += " on cluster " + o.ClusterName
		}
		if o.SubnetName != "" {
			label += " via subnet " + o.SubnetName
		}
		parts = append(parts, label)
	}
	sort.Strings(parts)
	return strings.Join(parts, "; ")
}
