package rest

import (
	"strings"
	"time"

	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/model"
)

// The structs below mirror only the fields this tool reads. The v4 payloads are
// far larger; decoding a subset keeps the mapping obvious and tolerates the
// additive changes each namespace release brings.

type subnetPayload struct {
	ExtID                string   `json:"extId"`
	Name                 string   `json:"name"`
	SubnetType           string   `json:"subnetType"`
	NetworkID            *int     `json:"networkId"`
	IsAdvancedNetworking *bool    `json:"isAdvancedNetworking"`
	IsExternal           *bool    `json:"isExternal"`
	MigrationState       string   `json:"migrationState"`
	ClusterReference     string   `json:"clusterReference"`
	ClusterReferenceList []string `json:"clusterReferenceList"`
	ClusterName          string   `json:"clusterName"`
	ClusterNameList      []string `json:"clusterNameList"`
	VirtualSwitchRef     string   `json:"virtualSwitchReference"`
	BridgeName           string   `json:"bridgeName"`
	HypervisorType       string   `json:"hypervisorType"`
	IPPrefix             string   `json:"ipPrefix"`
}

func (p subnetPayload) toModel() model.Subnet {
	out := model.Subnet{
		ExtID:                p.ExtID,
		Name:                 p.Name,
		Type:                 model.SubnetTypeUnknown,
		IsAdvancedNetworking: derefBool(p.IsAdvancedNetworking),
		IsExternal:           derefBool(p.IsExternal),
		ClusterExtIDs:        p.ClusterReferenceList,
		ClusterNames:         p.ClusterNameList,
		VirtualSwitchExtID:   p.VirtualSwitchRef,
		BridgeName:           p.BridgeName,
		HypervisorType:       p.HypervisorType,
		IPPrefix:             p.IPPrefix,
	}
	if p.NetworkID != nil {
		out.VLANID = *p.NetworkID
	}
	switch strings.ToUpper(p.SubnetType) {
	case "VLAN":
		out.Type = model.SubnetTypeVLAN
	case "OVERLAY":
		out.Type = model.SubnetTypeOverlay
	}
	if state := strings.ToUpper(p.MigrationState); state != "" && !strings.HasPrefix(state, "$") {
		out.MigrationState = model.MigrationState(state)
	}
	if len(out.ClusterExtIDs) == 0 && p.ClusterReference != "" {
		out.ClusterExtIDs = []string{p.ClusterReference}
	}
	if len(out.ClusterNames) == 0 && p.ClusterName != "" {
		out.ClusterNames = []string{p.ClusterName}
	}
	return out
}

type vnicPayload struct {
	ExtID       string `json:"extId"`
	MacAddress  string `json:"macAddress"`
	VMReference string `json:"vmReference"`
}

func (p vnicPayload) toModel(subnet model.Subnet) model.Vnic {
	return model.Vnic{
		ExtID:       p.ExtID,
		MACAddress:  p.MacAddress,
		VMExtID:     p.VMReference,
		SubnetExtID: subnet.ExtID,
		SubnetName:  subnet.Name,
		VlanMode:    model.VlanModeUnknown,
	}
}

type vmPayload struct {
	ExtID      string `json:"extId"`
	Name       string `json:"name"`
	PowerState string `json:"powerState"`
	IsAgentVM  *bool  `json:"isAgentVm"`
	Cluster    *struct {
		ExtID string `json:"extId"`
	} `json:"cluster"`
	Nics []struct {
		ExtID       string `json:"extId"`
		BackingInfo *struct {
			MacAddress  string `json:"macAddress"`
			IsConnected *bool  `json:"isConnected"`
		} `json:"backingInfo"`
		NetworkInfo *struct {
			NicType      string `json:"nicType"`
			VlanMode     string `json:"vlanMode"`
			TrunkedVlans []int  `json:"trunkedVlans"`
			Subnet       *struct {
				ExtID string `json:"extId"`
				Name  string `json:"name"`
			} `json:"subnet"`
		} `json:"networkInfo"`
	} `json:"nics"`
}

func (p vmPayload) toModel() model.VM {
	out := model.VM{
		ExtID:      p.ExtID,
		Name:       p.Name,
		PowerState: cleanEnum(p.PowerState),
		IsAgentVM:  derefBool(p.IsAgentVM),
	}
	if p.Cluster != nil {
		out.ClusterExtID = p.Cluster.ExtID
	}
	for _, nic := range p.Nics {
		converted := model.Vnic{
			ExtID:        nic.ExtID,
			VMExtID:      out.ExtID,
			VMName:       out.Name,
			ClusterExtID: out.ClusterExtID,
			VlanMode:     model.VlanModeUnknown,
		}
		if nic.BackingInfo != nil {
			converted.MACAddress = nic.BackingInfo.MacAddress
			converted.IsConnected = derefBool(nic.BackingInfo.IsConnected)
		}
		if ni := nic.NetworkInfo; ni != nil {
			converted.NicType = cleanEnum(ni.NicType)
			converted.TrunkedVLANs = ni.TrunkedVlans
			switch strings.ToUpper(ni.VlanMode) {
			case "ACCESS":
				converted.VlanMode = model.VlanModeAccess
			case "TRUNK":
				converted.VlanMode = model.VlanModeTrunk
			}
			if ni.Subnet != nil {
				converted.SubnetExtID = ni.Subnet.ExtID
				converted.SubnetName = ni.Subnet.Name
			}
		}
		out.Nics = append(out.Nics, converted)
	}
	return out
}

type clusterPayload struct {
	ExtID  string `json:"extId"`
	Name   string `json:"name"`
	Config *struct {
		BuildInfo *struct {
			Version string `json:"version"`
		} `json:"buildInfo"`
		IsAvailable     *bool    `json:"isAvailable"`
		HypervisorTypes []string `json:"hypervisorTypes"`
		ClusterFunction []string `json:"clusterFunction"`
	} `json:"config"`
}

func (p clusterPayload) toModel() model.Cluster {
	out := model.Cluster{ExtID: p.ExtID, Name: p.Name}
	if p.Config == nil {
		return out
	}
	if p.Config.BuildInfo != nil {
		out.AOSVersion = p.Config.BuildInfo.Version
	}
	out.IsAvailable = derefBool(p.Config.IsAvailable)
	for _, h := range p.Config.HypervisorTypes {
		if v := cleanEnum(h); v != "" {
			out.HypervisorTypes = append(out.HypervisorTypes, v)
		}
	}
	for _, f := range p.Config.ClusterFunction {
		if v := cleanEnum(f); v != "" {
			out.Functions = append(out.Functions, v)
		}
	}
	return out
}

type domainManagerPayload struct {
	ExtID  string `json:"extId"`
	Config *struct {
		Name      string `json:"name"`
		BuildInfo *struct {
			Version string `json:"version"`
		} `json:"buildInfo"`
	} `json:"config"`
}

type networkControllerPayload struct {
	ExtID             string `json:"extId"`
	ControllerVersion string `json:"controllerVersion"`
	ControllerStatus  string `json:"controllerStatus"`
	DefaultVlanStack  string `json:"defaultVlanStack"`
	MinimumNOSVersion string `json:"minimumNOSVersion"`
	MinimumAHVVersion string `json:"minimumAHVVersion"`
}

func (p networkControllerPayload) toModel() model.NetworkController {
	return model.NetworkController{
		ExtID:            p.ExtID,
		Version:          p.ControllerVersion,
		Status:           cleanEnum(p.ControllerStatus),
		DefaultVlanStack: cleanEnum(p.DefaultVlanStack),
		MinimumAOS:       p.MinimumNOSVersion,
		MinimumAHV:       p.MinimumAHVVersion,
	}
}

type fileServerPayload struct {
	ExtID string `json:"extId"`
	Name  string `json:"name"`
}

type protectedResourcePayload struct {
	ExtID                 string   `json:"extId"`
	EntityExtID           string   `json:"entityExtId"`
	EntityType            string   `json:"entityType"`
	ConsistencyGroupExtID string   `json:"consistencyGroupExtId"`
	CategoryFqNames       []string `json:"categoryFqNames"`
	SourceSiteReference   *struct {
		ClusterExtID string `json:"clusterExtId"`
	} `json:"sourceSiteReference"`
	SiteProtectionInfo []struct {
		LocationReference *struct {
			ClusterExtID string `json:"clusterExtId"`
		} `json:"locationReference"`
	} `json:"siteProtectionInfo"`
}

func (p protectedResourcePayload) toModel() model.ProtectionInfo {
	out := model.ProtectionInfo{
		EntityExtID:           p.EntityExtID,
		EntityType:            cleanEnum(p.EntityType),
		ConsistencyGroupExtID: p.ConsistencyGroupExtID,
		CategoryFqNames:       p.CategoryFqNames,
	}
	if out.EntityExtID == "" {
		out.EntityExtID = p.ExtID
	}
	for _, s := range p.SiteProtectionInfo {
		if s.LocationReference != nil && s.LocationReference.ClusterExtID != "" {
			out.ProtectedBy = append(out.ProtectedBy, "cluster:"+s.LocationReference.ClusterExtID)
		}
	}
	if p.SourceSiteReference != nil && p.SourceSiteReference.ClusterExtID != "" {
		out.ProtectedBy = append(out.ProtectedBy, "sourceCluster:"+p.SourceSiteReference.ClusterExtID)
	}
	return out
}

type taskPayload struct {
	ExtID              string     `json:"extId"`
	Status             string     `json:"status"`
	Operation          string     `json:"operation"`
	ProgressPercentage *int       `json:"progressPercentage"`
	LegacyErrorMessage string     `json:"legacyErrorMessage"`
	CompletedTime      *time.Time `json:"completedTime"`
	LastUpdatedTime    *time.Time `json:"lastUpdatedTime"`
	ErrorMessages      []struct {
		Message string `json:"message"`
	} `json:"errorMessages"`
	EntitiesAffected []struct {
		ExtID string `json:"extId"`
	} `json:"entitiesAffected"`
}

func (p taskPayload) toModel() model.Task {
	out := model.Task{
		ExtID:              p.ExtID,
		Status:             model.TaskUnknown,
		Operation:          p.Operation,
		LegacyErrorMessage: p.LegacyErrorMessage,
		CompletedTime:      p.CompletedTime,
		LastUpdatedTime:    p.LastUpdatedTime,
	}
	if p.ProgressPercentage != nil {
		out.ProgressPercentage = *p.ProgressPercentage
	}
	if status := cleanEnum(strings.ToUpper(p.Status)); status != "" {
		out.Status = model.TaskStatus(status)
	}
	for _, m := range p.ErrorMessages {
		if m.Message != "" {
			out.ErrorMessages = append(out.ErrorMessages, m.Message)
		}
	}
	for _, e := range p.EntitiesAffected {
		if e.ExtID != "" {
			out.EntitiesAffected = append(out.EntitiesAffected, e.ExtID)
		}
	}
	return out
}

type taskReferencePayload struct {
	ExtID string `json:"extId"`
}

func derefBool(p *bool) bool {
	return p != nil && *p
}

// cleanEnum drops the "$UNKNOWN"/"$REDACTED" placeholder values the v4 APIs
// return for enum members a client is not entitled to see.
func cleanEnum(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "$") {
		return ""
	}
	return v
}
