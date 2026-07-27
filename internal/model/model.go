// Package model holds the vendor-neutral domain types that the migration
// components exchange. Both service backends (Nutanix Go SDK and raw v4 REST)
// normalise their responses into these types so that the Controller,
// Prechecks, Executioner and Postchecks never depend on a transport choice.
package model

import (
	"fmt"
	"strings"
	"time"
)

// EndpointKind distinguishes a Prism Central from a Prism Element endpoint.
type EndpointKind string

const (
	EndpointPrismCentral EndpointKind = "PrismCentral"
	EndpointPrismElement EndpointKind = "PrismElement"
)

// Endpoint describes how to reach a Prism instance.
type Endpoint struct {
	Kind     EndpointKind
	Host     string
	Port     int
	Username string
	Password string
	// Insecure skips TLS verification, which is the norm for Prism deployments
	// using the self-signed certificate they ship with.
	Insecure bool
}

func (e Endpoint) Address() string {
	return fmt.Sprintf("%s:%d", e.Host, e.Port)
}

func (e Endpoint) BaseURL() string {
	return fmt.Sprintf("https://%s:%d", e.Host, e.Port)
}

// ProductVersion is the version of a Prism Central or AOS build.
type ProductVersion struct {
	// Product is a human readable name such as "Prism Central" or "AOS".
	Product string `json:"product,omitempty"`
	// Raw is the version string exactly as reported by the API, for example
	// "pc.7.5" or "7.5".
	Raw string `json:"raw,omitempty"`
	// Major and Minor are parsed from Raw; they are zero when Raw could not be
	// interpreted.
	Major int `json:"major,omitempty"`
	Minor int `json:"minor,omitempty"`
	// Source names the API that produced the value, useful in the audit log.
	Source string `json:"source,omitempty"`
}

func (v ProductVersion) String() string {
	if v.Raw == "" {
		return "unknown"
	}
	return v.Raw
}

// Cluster is a registered Prism Element cluster as seen from Prism Central.
type Cluster struct {
	ExtID           string   `json:"extId"`
	Name            string   `json:"name,omitempty"`
	AOSVersion      string   `json:"aosVersion,omitempty"`
	HypervisorTypes []string `json:"hypervisorTypes,omitempty"`
	Functions       []string `json:"functions,omitempty"`
	IsAvailable     bool     `json:"isAvailable"`
}

// SubnetType mirrors the networking v4 subnet type discriminator.
type SubnetType string

const (
	SubnetTypeVLAN    SubnetType = "VLAN"
	SubnetTypeOverlay SubnetType = "OVERLAY"
	SubnetTypeUnknown SubnetType = "UNKNOWN"
)

// MigrationState mirrors the networking v4 subnet migration state.
type MigrationState string

const (
	MigrationStateNone       MigrationState = ""
	MigrationStateInProgress MigrationState = "IN_PROGRESS"
	MigrationStateFailed     MigrationState = "FAILED"
	MigrationStateCompleted  MigrationState = "COMPLETED"
	MigrationStateUnknown    MigrationState = "UNKNOWN"
)

// Subnet is a Prism Central subnet (a VLAN in the vocabulary of this tool).
type Subnet struct {
	ExtID                string         `json:"extId"`
	Name                 string         `json:"name,omitempty"`
	Type                 SubnetType     `json:"type,omitempty"`
	VLANID               int            `json:"vlanId"`
	IsAdvancedNetworking bool           `json:"isAdvancedNetworking"`
	IsExternal           bool           `json:"isExternal,omitempty"`
	MigrationState       MigrationState `json:"migrationState,omitempty"`
	ClusterExtIDs        []string       `json:"clusterExtIds,omitempty"`
	ClusterNames         []string       `json:"clusterNames,omitempty"`
	VirtualSwitchExtID   string         `json:"virtualSwitchExtId,omitempty"`
	BridgeName           string         `json:"bridgeName,omitempty"`
	HypervisorType       string         `json:"hypervisorType,omitempty"`
	IPPrefix             string         `json:"ipPrefix,omitempty"`
}

// IsBasicVLAN reports whether the subnet is a VLAN subnet still served by the
// Acropolis (basic) network stack, and therefore a migration candidate.
func (s Subnet) IsBasicVLAN() bool {
	return s.Type == SubnetTypeVLAN && !s.IsAdvancedNetworking
}

// Describe renders a short label used throughout the logs and reports.
func (s Subnet) Describe() string {
	name := s.Name
	if name == "" {
		name = s.ExtID
	}
	if s.Type == SubnetTypeVLAN {
		return fmt.Sprintf("%s (VLAN %d)", name, s.VLANID)
	}
	return name
}

// VlanMode mirrors the VMM v4 NIC VLAN mode.
type VlanMode string

const (
	VlanModeAccess  VlanMode = "ACCESS"
	VlanModeTrunk   VlanMode = "TRUNK"
	VlanModeUnknown VlanMode = "UNKNOWN"
)

// Vnic is a virtual NIC of a VM. Depending on the source it may be populated
// from the networking subnet-vNIC listing (MAC + VM reference only) or from the
// VMM VM configuration (full NIC detail including the VLAN mode).
type Vnic struct {
	ExtID        string
	MACAddress   string
	VMExtID      string
	VMName       string
	ClusterExtID string
	SubnetExtID  string
	SubnetName   string
	VlanMode     VlanMode
	TrunkedVLANs []int
	NicType      string
	IsConnected  bool
}

// IsTrunked reports whether the NIC carries more than one VLAN, which the
// basic-to-advanced subnet migration does not support.
func (n Vnic) IsTrunked() bool {
	return n.VlanMode == VlanModeTrunk || len(n.TrunkedVLANs) > 0
}

// VM is an AHV virtual machine together with its NICs.
type VM struct {
	ExtID        string
	Name         string
	ClusterExtID string
	ClusterName  string
	PowerState   string
	// IsAgentVM marks infrastructure VMs such as Files FSVMs, which the Files
	// dependency check uses as one of its signals.
	IsAgentVM bool
	Nics      []Vnic
}

// NetworkController is the Flow Virtual Networking (Atlas) controller that owns
// advanced networking on a Prism Central.
type NetworkController struct {
	ExtID            string `json:"extId"`
	Version          string `json:"version,omitempty"`
	Status           string `json:"status,omitempty"`
	DefaultVlanStack string `json:"defaultVlanStack,omitempty"`
	MinimumAOS       string `json:"minimumAosVersion,omitempty"`
	MinimumAHV       string `json:"minimumAhvVersion,omitempty"`
}

// IsUsable reports whether the controller is in a state that can accept a
// subnet migration.
func (c NetworkController) IsUsable() bool {
	return strings.EqualFold(c.Status, "UP")
}

// FileServer is a Nutanix Files instance.
type FileServer struct {
	ExtID string
	Name  string
}

// FileServerNetwork associates a file server with the subnets it consumes. It
// is only available from the Prism Element legacy inventory, which is the sole
// API that exposes the internal and client network UUIDs.
type FileServerNetwork struct {
	FileServerExtID string
	FileServerName  string
	// SubnetExtIDs holds the internal (storage) and external (client) subnet
	// UUIDs the file server is configured with.
	SubnetExtIDs []string
	// VMExtIDs holds the FSVM UUIDs when the source reports them.
	VMExtIDs []string
}

// ProtectionDomain is a legacy (Prism Element) protection domain.
type ProtectionDomain struct {
	Name     string
	Active   bool
	VMExtIDs []string
	VMNames  []string
}

// ProtectionInfo describes how a VM or volume group is protected according to
// the Prism Central data protection service.
type ProtectionInfo struct {
	EntityExtID           string
	EntityType            string
	ConsistencyGroupExtID string
	CategoryFqNames       []string
	// ProtectedBy names the policies or sites that reference the entity when
	// the API reports them.
	ProtectedBy []string
}

// TaskStatus mirrors the Prism v4 task status.
type TaskStatus string

const (
	TaskQueued    TaskStatus = "QUEUED"
	TaskRunning   TaskStatus = "RUNNING"
	TaskCanceling TaskStatus = "CANCELING"
	TaskSucceeded TaskStatus = "SUCCEEDED"
	TaskFailed    TaskStatus = "FAILED"
	TaskCanceled  TaskStatus = "CANCELED"
	TaskSuspended TaskStatus = "SUSPENDED"
	TaskUnknown   TaskStatus = "UNKNOWN"
)

// IsTerminal reports whether the task will not change state again.
func (s TaskStatus) IsTerminal() bool {
	switch s {
	case TaskSucceeded, TaskFailed, TaskCanceled:
		return true
	default:
		return false
	}
}

// Task is a Prism task.
type Task struct {
	ExtID              string
	Status             TaskStatus
	Operation          string
	ProgressPercentage int
	ErrorMessages      []string
	LegacyErrorMessage string
	CompletedTime      *time.Time
	LastUpdatedTime    *time.Time
	EntitiesAffected   []string
}

// FailureReason renders the best available explanation of a failed task.
func (t Task) FailureReason() string {
	if len(t.ErrorMessages) > 0 {
		return strings.Join(t.ErrorMessages, "; ")
	}
	if t.LegacyErrorMessage != "" {
		return t.LegacyErrorMessage
	}
	return fmt.Sprintf("task %s ended in state %s without an error message", t.ExtID, t.Status)
}

// NormalizeMAC lower-cases a MAC address and strips the separators so that
// addresses reported in different notations still compare equal.
func NormalizeMAC(mac string) string {
	var b strings.Builder
	b.Grow(12)
	for _, r := range mac {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
			b.WriteRune(r)
		case r >= 'A' && r <= 'F':
			b.WriteRune(r + ('a' - 'A'))
		}
	}
	return b.String()
}
