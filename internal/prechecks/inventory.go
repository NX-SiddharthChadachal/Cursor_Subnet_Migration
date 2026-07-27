package prechecks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/logging"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/model"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/parallel"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/service"
)

// Inventory is the snapshot of Prism Central and Prism Element state that every
// check reads. It is gathered once so that the three checks can run concurrently
// against a consistent picture instead of each issuing its own API calls.
type Inventory struct {
	Subnets []model.Subnet
	VMs     []model.VM

	// SubnetsByExtID indexes Subnets.
	SubnetsByExtID map[string]model.Subnet
	// VMsByExtID indexes VMs.
	VMsByExtID map[string]model.VM
	// ClusterNames maps a cluster UUID to its name for readable messages.
	ClusterNames map[string]string

	// VnicsBySubnet holds the vNICs attached to each candidate subnet, merged
	// from the networking subnet-vNIC listing and the VMM NIC configuration.
	VnicsBySubnet map[string][]model.Vnic

	// MACOccurrences maps a normalised MAC address to every place it was seen.
	MACOccurrences map[string][]MACOccurrence

	FileServers        []model.FileServer
	FileServerNetworks []model.FileServerNetwork
	ProtectionDomains  []model.ProtectionDomain

	// Incomplete records checks whose data source was unavailable, so that the
	// report can say "not verified" instead of implying a clean result.
	Incomplete []model.Finding
}

// MACOccurrence is one sighting of a MAC address.
type MACOccurrence struct {
	MACAddress   string
	VMExtID      string
	VMName       string
	VnicExtID    string
	ClusterExtID string
	ClusterName  string
	SubnetExtID  string
	SubnetName   string
}

// Gather collects everything the checks need. candidates limits the per-subnet
// vNIC listing to the subnets under consideration; the MAC index is always built
// across every VM in Prism Central, because a duplicate on another cluster is
// exactly one of the failure modes this tool exists to catch.
func Gather(ctx context.Context, pc service.PrismCentral, pe service.PrismElement, log *logging.Logger, concurrency int, candidates []model.Subnet) (*Inventory, error) {
	inv := &Inventory{
		SubnetsByExtID: map[string]model.Subnet{},
		VMsByExtID:     map[string]model.VM{},
		ClusterNames:   map[string]string{},
		VnicsBySubnet:  map[string][]model.Vnic{},
		MACOccurrences: map[string][]MACOccurrence{},
	}

	var (
		mu                 sync.Mutex
		subnets            []model.Subnet
		vms                []model.VM
		fileServers        []model.FileServer
		fileServerNetworks []model.FileServerNetwork
		protectionDomains  []model.ProtectionDomain
		clusters           []model.Cluster
	)

	err := parallel.Group(
		func() error {
			var err error
			subnets, err = pc.ListSubnets(ctx)
			if err != nil {
				return fmt.Errorf("list subnets: %w", err)
			}
			log.Debugf("Read %d subnets from Prism Central", len(subnets))
			return nil
		},
		func() error {
			var err error
			vms, err = pc.ListVMs(ctx)
			if err != nil {
				return fmt.Errorf("list VMs: %w", err)
			}
			log.Debugf("Read %d VMs from Prism Central", len(vms))
			return nil
		},
		func() error {
			var err error
			clusters, err = pc.ListClusters(ctx)
			if err != nil {
				// Cluster names are cosmetic; a failure here must not stop a run.
				log.Warnf("Could not read cluster names: %v", err)
			}
			return nil
		},
		func() error {
			var err error
			fileServers, err = pc.ListFileServers(ctx)
			if err != nil {
				mu.Lock()
				defer mu.Unlock()
				inv.Incomplete = append(inv.Incomplete, incompleteFinding(model.CheckFileServer,
					fmt.Sprintf("Could not enumerate Nutanix Files instances from Prism Central: %v", err)))
				log.Warnf("Could not enumerate file servers: %v", err)
			}
			return nil
		},
		func() error {
			if pe == nil {
				mu.Lock()
				defer mu.Unlock()
				inv.Incomplete = append(inv.Incomplete, incompleteFinding(model.CheckFileServer,
					"No Prism Element endpoint was supplied, so the subnets each Nutanix Files instance uses could not be read"))
				return nil
			}
			var err error
			fileServerNetworks, err = pe.ListFileServerNetworks(ctx)
			if err != nil {
				mu.Lock()
				defer mu.Unlock()
				inv.Incomplete = append(inv.Incomplete, incompleteFinding(model.CheckFileServer,
					fmt.Sprintf("Could not read file server network assignments from Prism Element: %v", err)))
				log.Warnf("Could not read file server networks: %v", err)
			}
			return nil
		},
		func() error {
			if pe == nil {
				mu.Lock()
				defer mu.Unlock()
				inv.Incomplete = append(inv.Incomplete, incompleteFinding(model.CheckProtectionDomain,
					"No Prism Element endpoint was supplied, so legacy protection domains could not be read"))
				return nil
			}
			var err error
			protectionDomains, err = pe.ListProtectionDomains(ctx)
			if err != nil {
				mu.Lock()
				defer mu.Unlock()
				inv.Incomplete = append(inv.Incomplete, incompleteFinding(model.CheckProtectionDomain,
					fmt.Sprintf("Could not read legacy protection domains from Prism Element: %v", err)))
				log.Warnf("Could not read protection domains: %v", err)
			}
			return nil
		},
	)
	if err != nil {
		return nil, err
	}

	inv.Subnets = subnets
	inv.VMs = vms
	inv.FileServers = fileServers
	inv.FileServerNetworks = fileServerNetworks
	inv.ProtectionDomains = protectionDomains

	for _, c := range clusters {
		inv.ClusterNames[c.ExtID] = c.Name
	}
	for _, s := range subnets {
		inv.SubnetsByExtID[s.ExtID] = s
	}
	for _, vm := range vms {
		inv.VMsByExtID[vm.ExtID] = vm
	}

	// The VMM NIC configuration is the only source for the VLAN mode, and it
	// covers every cluster Prism Central manages, so it seeds both the MAC index
	// and the per-subnet vNIC lists.
	for _, vm := range vms {
		clusterName := inv.ClusterNames[vm.ClusterExtID]
		for _, nic := range vm.Nics {
			nic.VMExtID = vm.ExtID
			nic.VMName = vm.Name
			nic.ClusterExtID = vm.ClusterExtID
			if nic.SubnetExtID != "" {
				if subnet, ok := inv.SubnetsByExtID[nic.SubnetExtID]; ok && nic.SubnetName == "" {
					nic.SubnetName = subnet.Name
				}
				inv.VnicsBySubnet[nic.SubnetExtID] = append(inv.VnicsBySubnet[nic.SubnetExtID], nic)
			}
			inv.addMAC(MACOccurrence{
				MACAddress:   nic.MACAddress,
				VMExtID:      vm.ExtID,
				VMName:       vm.Name,
				VnicExtID:    nic.ExtID,
				ClusterExtID: vm.ClusterExtID,
				ClusterName:  clusterName,
				SubnetExtID:  nic.SubnetExtID,
				SubnetName:   nic.SubnetName,
			})
		}
	}

	// The networking subnet-vNIC listing is authoritative for subnet membership
	// and catches ports whose VM the VMM listing did not return, for example a
	// VM on a cluster that is temporarily unreachable.
	err = parallel.ForEach(ctx, concurrency, candidates, func(ctx context.Context, subnet model.Subnet) error {
		vnics, err := pc.ListVnicsBySubnet(ctx, subnet.ExtID)
		if err != nil {
			if errors.Is(err, service.ErrUnsupported) {
				log.Warnf("Subnet %s: the vNIC listing is not available on this backend; falling back to the VM NIC configuration", subnet.Describe())
				return nil
			}
			return fmt.Errorf("list vNICs of subnet %s: %w", subnet.Describe(), err)
		}
		mu.Lock()
		defer mu.Unlock()
		for _, vnic := range vnics {
			if vm, ok := inv.VMsByExtID[vnic.VMExtID]; ok {
				vnic.VMName = vm.Name
				vnic.ClusterExtID = vm.ClusterExtID
			}
			inv.mergeVnic(subnet.ExtID, vnic)
			inv.addMAC(MACOccurrence{
				MACAddress:   vnic.MACAddress,
				VMExtID:      vnic.VMExtID,
				VMName:       vnic.VMName,
				VnicExtID:    vnic.ExtID,
				ClusterExtID: vnic.ClusterExtID,
				ClusterName:  inv.ClusterNames[vnic.ClusterExtID],
				SubnetExtID:  subnet.ExtID,
				SubnetName:   subnet.Name,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return inv, nil
}

// addMAC records a sighting, ignoring repeats of the same vNIC so that merging
// two sources does not manufacture duplicates.
func (i *Inventory) addMAC(occ MACOccurrence) {
	key := model.NormalizeMAC(occ.MACAddress)
	if key == "" {
		return
	}
	for _, existing := range i.MACOccurrences[key] {
		if existing.VnicExtID != "" && existing.VnicExtID == occ.VnicExtID {
			return
		}
	}
	i.MACOccurrences[key] = append(i.MACOccurrences[key], occ)
}

// mergeVnic appends a vNIC to a subnet unless the same port is already listed.
func (i *Inventory) mergeVnic(subnetExtID string, vnic model.Vnic) {
	for idx, existing := range i.VnicsBySubnet[subnetExtID] {
		if existing.ExtID != "" && existing.ExtID == vnic.ExtID {
			// Keep the richer record: the VMM source knows the VLAN mode, the
			// networking source knows subnet membership.
			if existing.VlanMode == model.VlanModeUnknown && vnic.VlanMode != model.VlanModeUnknown {
				i.VnicsBySubnet[subnetExtID][idx].VlanMode = vnic.VlanMode
			}
			return
		}
		if existing.MACAddress != "" && model.NormalizeMAC(existing.MACAddress) == model.NormalizeMAC(vnic.MACAddress) &&
			existing.VMExtID == vnic.VMExtID {
			return
		}
	}
	i.VnicsBySubnet[subnetExtID] = append(i.VnicsBySubnet[subnetExtID], vnic)
}

// VMSummaries renders the VMs attached to a subnet for the output report.
func (i *Inventory) VMSummaries(subnetExtID string) []model.VMSummary {
	byVM := map[string]*model.VMSummary{}
	var order []string
	for _, vnic := range i.VnicsBySubnet[subnetExtID] {
		if vnic.VMExtID == "" {
			continue
		}
		summary, ok := byVM[vnic.VMExtID]
		if !ok {
			vm := i.VMsByExtID[vnic.VMExtID]
			name := vnic.VMName
			if name == "" {
				name = vm.Name
			}
			cluster := vnic.ClusterExtID
			if cluster == "" {
				cluster = vm.ClusterExtID
			}
			summary = &model.VMSummary{
				ExtID:        vnic.VMExtID,
				Name:         name,
				ClusterExtID: cluster,
				ClusterName:  i.ClusterNames[cluster],
				PowerState:   vm.PowerState,
			}
			byVM[vnic.VMExtID] = summary
			order = append(order, vnic.VMExtID)
		}
		if vnic.MACAddress != "" && !contains(summary.MACAddresses, vnic.MACAddress) {
			summary.MACAddresses = append(summary.MACAddresses, vnic.MACAddress)
		}
		if vnic.ExtID != "" && !contains(summary.VnicExtIDs, vnic.ExtID) {
			summary.VnicExtIDs = append(summary.VnicExtIDs, vnic.ExtID)
		}
	}

	out := make([]model.VMSummary, 0, len(order))
	for _, id := range order {
		out = append(out, *byVM[id])
	}
	return out
}

func incompleteFinding(check model.CheckID, message string) model.Finding {
	return model.Finding{
		Check:       check,
		Severity:    model.SeverityWarning,
		Message:     message,
		Remediation: model.RemediationNone,
	}
}

func contains(haystack []string, needle string) bool {
	for _, v := range haystack {
		if strings.EqualFold(v, needle) {
			return true
		}
	}
	return false
}
