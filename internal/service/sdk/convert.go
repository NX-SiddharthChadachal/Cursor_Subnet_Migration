package sdk

import (
	"strings"

	clustermgmtconfig "github.com/nutanix/ntnx-api-golang-clients/clustermgmt-go-client/v4/models/clustermgmt/v4/config"
	dataprotectionconfig "github.com/nutanix/ntnx-api-golang-clients/dataprotection-go-client/v4/models/dataprotection/v4/config"
	filesconfig "github.com/nutanix/ntnx-api-golang-clients/files-go-client/v4/models/files/v4/config"
	networkingconfig "github.com/nutanix/ntnx-api-golang-clients/networking-go-client/v4/models/networking/v4/config"
	prismconfig "github.com/nutanix/ntnx-api-golang-clients/prism-go-client/v4/models/prism/v4/config"
	ahvconfig "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/ahv/config"

	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/model"
)

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// cleanEnum drops the "$UNKNOWN"/"$REDACTED" placeholders the generated enums
// use for values the SDK does not know about.
func cleanEnum(name string) string {
	if strings.HasPrefix(name, "$") {
		return ""
	}
	return name
}

func convertSubnet(in networkingconfig.Subnet) model.Subnet {
	out := model.Subnet{
		ExtID:                deref(in.ExtId),
		Name:                 deref(in.Name),
		Type:                 model.SubnetTypeUnknown,
		VLANID:               deref(in.NetworkId),
		IsAdvancedNetworking: deref(in.IsAdvancedNetworking),
		IsExternal:           deref(in.IsExternal),
		ClusterExtIDs:        in.ClusterReferenceList,
		ClusterNames:         in.ClusterNameList,
		VirtualSwitchExtID:   deref(in.VirtualSwitchReference),
		BridgeName:           deref(in.BridgeName),
		HypervisorType:       deref(in.HypervisorType),
		IPPrefix:             deref(in.IpPrefix),
	}

	// Older responses populate the singular cluster fields only.
	if len(out.ClusterExtIDs) == 0 && in.ClusterReference != nil {
		out.ClusterExtIDs = []string{*in.ClusterReference}
	}
	if len(out.ClusterNames) == 0 && in.ClusterName != nil {
		out.ClusterNames = []string{*in.ClusterName}
	}

	if in.SubnetType != nil {
		switch name := cleanEnum(in.SubnetType.GetName()); name {
		case "VLAN":
			out.Type = model.SubnetTypeVLAN
		case "OVERLAY":
			out.Type = model.SubnetTypeOverlay
		}
	}

	if in.MigrationState != nil {
		if name := cleanEnum(in.MigrationState.GetName()); name != "" {
			out.MigrationState = model.MigrationState(name)
		}
	}

	return out
}

func convertVnic(in networkingconfig.Vnic, subnet model.Subnet) model.Vnic {
	return model.Vnic{
		ExtID:       deref(in.ExtId),
		MACAddress:  deref(in.MacAddress),
		VMExtID:     deref(in.VmReference),
		SubnetExtID: subnet.ExtID,
		SubnetName:  subnet.Name,
		VlanMode:    model.VlanModeUnknown,
	}
}

func convertVM(in ahvconfig.Vm) model.VM {
	out := model.VM{
		ExtID:     deref(in.ExtId),
		Name:      deref(in.Name),
		IsAgentVM: deref(in.IsAgentVm),
	}
	if in.Cluster != nil {
		out.ClusterExtID = deref(in.Cluster.ExtId)
	}
	if in.PowerState != nil {
		out.PowerState = cleanEnum(in.PowerState.GetName())
	}
	for _, nic := range in.Nics {
		out.Nics = append(out.Nics, convertNic(nic, out))
	}
	return out
}

func convertNic(in ahvconfig.Nic, vm model.VM) model.Vnic {
	out := model.Vnic{
		ExtID:        deref(in.ExtId),
		VMExtID:      vm.ExtID,
		VMName:       vm.Name,
		ClusterExtID: vm.ClusterExtID,
		VlanMode:     model.VlanModeUnknown,
	}
	if in.BackingInfo != nil {
		out.MACAddress = deref(in.BackingInfo.MacAddress)
		out.IsConnected = deref(in.BackingInfo.IsConnected)
	}
	if ni := in.NetworkInfo; ni != nil {
		out.TrunkedVLANs = ni.TrunkedVlans
		if ni.Subnet != nil {
			// The VMM NIC reference carries only the subnet UUID; the name is
			// filled in later from the subnet inventory when it is needed.
			out.SubnetExtID = deref(ni.Subnet.ExtId)
		}
		if ni.NicType != nil {
			out.NicType = cleanEnum(ni.NicType.GetName())
		}
		if ni.VlanMode != nil {
			switch cleanEnum(ni.VlanMode.GetName()) {
			case "ACCESS":
				out.VlanMode = model.VlanModeAccess
			case "TRUNK":
				out.VlanMode = model.VlanModeTrunk
			}
		}
	}
	return out
}

func convertCluster(in clustermgmtconfig.Cluster) model.Cluster {
	out := model.Cluster{
		ExtID: deref(in.ExtId),
		Name:  deref(in.Name),
	}
	if in.Config == nil {
		return out
	}
	if in.Config.BuildInfo != nil {
		out.AOSVersion = deref(in.Config.BuildInfo.Version)
	}
	if in.Config.IsAvailable != nil {
		out.IsAvailable = *in.Config.IsAvailable
	}
	for _, h := range in.Config.HypervisorTypes {
		if name := cleanEnum(h.GetName()); name != "" {
			out.HypervisorTypes = append(out.HypervisorTypes, name)
		}
	}
	for _, f := range in.Config.ClusterFunction {
		if name := cleanEnum(f.GetName()); name != "" {
			out.Functions = append(out.Functions, name)
		}
	}
	return out
}

func convertNetworkController(in networkingconfig.NetworkController) model.NetworkController {
	out := model.NetworkController{
		ExtID:      deref(in.ExtId),
		Version:    deref(in.ControllerVersion),
		MinimumAOS: deref(in.MinimumNOSVersion),
		MinimumAHV: deref(in.MinimumAHVVersion),
	}
	if in.ControllerStatus != nil {
		out.Status = cleanEnum(in.ControllerStatus.GetName())
	}
	if in.DefaultVlanStack != nil {
		out.DefaultVlanStack = cleanEnum(in.DefaultVlanStack.GetName())
	}
	return out
}

func convertFileServer(in filesconfig.FileServer) model.FileServer {
	return model.FileServer{
		ExtID: deref(in.ExtId),
		Name:  deref(in.Name),
	}
}

func convertProtectionInfo(in dataprotectionconfig.ProtectedResource) model.ProtectionInfo {
	out := model.ProtectionInfo{
		EntityExtID:           deref(in.EntityExtId),
		ConsistencyGroupExtID: deref(in.ConsistencyGroupExtId),
		CategoryFqNames:       in.CategoryFqNames,
	}
	if out.EntityExtID == "" {
		out.EntityExtID = deref(in.ExtId)
	}
	if in.EntityType != nil {
		out.EntityType = cleanEnum(in.EntityType.GetName())
	}
	for _, s := range in.SiteProtectionInfo {
		if s.LocationReference != nil && s.LocationReference.ClusterExtId != nil {
			out.ProtectedBy = append(out.ProtectedBy, "cluster:"+*s.LocationReference.ClusterExtId)
		}
	}
	if in.SourceSiteReference != nil && in.SourceSiteReference.ClusterExtId != nil {
		out.ProtectedBy = append(out.ProtectedBy, "sourceCluster:"+*in.SourceSiteReference.ClusterExtId)
	}
	return out
}

func convertTask(in prismconfig.Task) model.Task {
	out := model.Task{
		ExtID:              deref(in.ExtId),
		Status:             model.TaskUnknown,
		Operation:          deref(in.Operation),
		ProgressPercentage: deref(in.ProgressPercentage),
		LegacyErrorMessage: deref(in.LegacyErrorMessage),
		CompletedTime:      in.CompletedTime,
		LastUpdatedTime:    in.LastUpdatedTime,
	}
	if in.Status != nil {
		if name := cleanEnum(in.Status.GetName()); name != "" {
			out.Status = model.TaskStatus(name)
		}
	}
	for _, m := range in.ErrorMessages {
		if m.Message != nil && *m.Message != "" {
			out.ErrorMessages = append(out.ErrorMessages, *m.Message)
		}
	}
	for _, e := range in.EntitiesAffected {
		if e.ExtId != nil {
			out.EntitiesAffected = append(out.EntitiesAffected, *e.ExtId)
		}
	}
	return out
}
