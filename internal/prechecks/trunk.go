package prechecks

import (
	"fmt"

	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/model"
)

// CheckTrunkedVnics flags subnets that carry a trunked vNIC.
//
// The basic-to-advanced VLAN migration moves a port from the Acropolis stack to
// Atlas, and Atlas does not accept a port that carries more than one VLAN. Two
// shapes matter:
//
//   - a vNIC whose VLAN mode is TRUNK and whose subnet is the one being
//     migrated, which is the port's native VLAN, and
//   - a vNIC on some other subnet whose trunked VLAN list includes this subnet's
//     VLAN ID, which means traffic for this VLAN rides that port too.
//
// Both make the subnet ineligible, and neither is something an operator can fix
// from Prism, hence the support ticket recommendation.
func CheckTrunkedVnics(subnet model.Subnet, inv *Inventory) []model.Finding {
	var findings []model.Finding

	for _, vnic := range inv.VnicsBySubnet[subnet.ExtID] {
		if !vnic.IsTrunked() {
			continue
		}
		findings = append(findings, model.Finding{
			Check:       model.CheckTrunkedVnic,
			Severity:    model.SeverityBlocker,
			SubnetExtID: subnet.ExtID,
			SubnetName:  subnet.Name,
			VMExtID:     vnic.VMExtID,
			VMName:      vnic.VMName,
			VnicExtID:   vnic.ExtID,
			MACAddress:  vnic.MACAddress,
			Message: fmt.Sprintf("vNIC %s of VM %s is attached to this subnet in TRUNK mode (trunked VLANs: %s); migrating a trunked vNIC to the advanced stack is not supported",
				describeVnic(vnic), describeVM(vnic), formatVLANs(vnic.TrunkedVLANs)),
			Remediation: model.RemediationSupportTicket,
		})
	}

	// A trunked port on another subnet can still carry this VLAN.
	if subnet.Type == model.SubnetTypeVLAN {
		for _, vm := range inv.VMs {
			for _, nic := range vm.Nics {
				if nic.SubnetExtID == subnet.ExtID || !nic.IsTrunked() {
					continue
				}
				if !containsInt(nic.TrunkedVLANs, subnet.VLANID) {
					continue
				}
				findings = append(findings, model.Finding{
					Check:       model.CheckTrunkedVnic,
					Severity:    model.SeverityBlocker,
					SubnetExtID: subnet.ExtID,
					SubnetName:  subnet.Name,
					VMExtID:     vm.ExtID,
					VMName:      vm.Name,
					VnicExtID:   nic.ExtID,
					MACAddress:  nic.MACAddress,
					Message: fmt.Sprintf("vNIC %s of VM %s trunks VLAN %d on subnet %s; this subnet's VLAN is carried by a trunked port and cannot be migrated",
						describeVnic(nic), vm.Name, subnet.VLANID, nicSubnetLabel(nic, inv)),
					Remediation: model.RemediationSupportTicket,
				})
			}
		}
	}

	return findings
}

func describeVnic(vnic model.Vnic) string {
	if vnic.ExtID != "" {
		return vnic.ExtID
	}
	if vnic.MACAddress != "" {
		return vnic.MACAddress
	}
	return "(unidentified)"
}

func describeVM(vnic model.Vnic) string {
	if vnic.VMName != "" {
		return vnic.VMName
	}
	if vnic.VMExtID != "" {
		return vnic.VMExtID
	}
	return "(unknown VM)"
}

func nicSubnetLabel(nic model.Vnic, inv *Inventory) string {
	if nic.SubnetName != "" {
		return nic.SubnetName
	}
	if s, ok := inv.SubnetsByExtID[nic.SubnetExtID]; ok && s.Name != "" {
		return s.Name
	}
	if nic.SubnetExtID != "" {
		return nic.SubnetExtID
	}
	return "(no subnet)"
}

func formatVLANs(vlans []int) string {
	if len(vlans) == 0 {
		return "all VLANs"
	}
	out := ""
	for i, v := range vlans {
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprint(v)
	}
	return out
}

func containsInt(haystack []int, needle int) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}
