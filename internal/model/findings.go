package model

import (
	"fmt"
	"sort"
)

// Severity grades a precheck or postcheck finding.
type Severity string

const (
	// SeverityInfo records context that does not affect eligibility.
	SeverityInfo Severity = "INFO"
	// SeverityWarning records something the operator should know about but
	// which does not stop the migration, for example a check that could not be
	// completed because an optional API was unavailable.
	SeverityWarning Severity = "WARNING"
	// SeverityBlocker makes the affected subnet ineligible for migration.
	SeverityBlocker Severity = "BLOCKER"
)

// CheckID identifies the check that produced a finding.
type CheckID string

const (
	CheckTrunkedVnic      CheckID = "trunked-vnic"
	CheckDuplicateMAC     CheckID = "duplicate-mac"
	CheckFileServer       CheckID = "file-server-usage"
	CheckProtectionDomain CheckID = "protection-domain-usage"
	CheckSubnetState      CheckID = "subnet-state"
	CheckNetworkStack     CheckID = "network-stack"
	CheckMigrationResult  CheckID = "migration-result"
)

// Remediation is the action suggested to the operator for a finding.
type Remediation string

const (
	// RemediationSupportTicket is used for conditions that need Nutanix
	// engineering involvement, such as duplicate MAC addresses or trunked
	// vNICs.
	RemediationSupportTicket Remediation = "Raise a Nutanix support ticket before migrating this subnet"
	// RemediationExcludeOrTicket applies to dependencies that the operator can
	// either resolve with support or side-step by leaving the subnet out of the
	// migration batch.
	RemediationExcludeOrTicket Remediation = "Raise a Nutanix support ticket, or exclude this subnet from the migration list"
	// RemediationRetryLater applies to subnets that are transiently busy.
	RemediationRetryLater Remediation = "Wait for the in-flight migration to settle and re-run the tool"
	// RemediationNone is used for informational findings.
	RemediationNone Remediation = ""
)

// Finding is a single observation about a subnet, optionally narrowed down to a
// VM, a vNIC or a MAC address.
type Finding struct {
	Check       CheckID     `json:"check"`
	Severity    Severity    `json:"severity"`
	SubnetExtID string      `json:"subnetExtId,omitempty"`
	SubnetName  string      `json:"subnetName,omitempty"`
	VMExtID     string      `json:"vmExtId,omitempty"`
	VMName      string      `json:"vmName,omitempty"`
	VnicExtID   string      `json:"vnicExtId,omitempty"`
	MACAddress  string      `json:"macAddress,omitempty"`
	Message     string      `json:"message"`
	Remediation Remediation `json:"remediation,omitempty"`
}

func (f Finding) String() string {
	scope := f.SubnetName
	if scope == "" {
		scope = f.SubnetExtID
	}
	if scope == "" {
		scope = "cluster-wide"
	}
	return fmt.Sprintf("[%s] %s: %s", f.Severity, scope, f.Message)
}

// VMSummary is the compact VM record carried in the output report.
type VMSummary struct {
	ExtID        string   `json:"extId"`
	Name         string   `json:"name,omitempty"`
	ClusterExtID string   `json:"clusterExtId,omitempty"`
	ClusterName  string   `json:"clusterName,omitempty"`
	PowerState   string   `json:"powerState,omitempty"`
	MACAddresses []string `json:"macAddresses,omitempty"`
	VnicExtIDs   []string `json:"vnicExtIds,omitempty"`
}

// SubnetAssessment is the precheck verdict for one subnet.
type SubnetAssessment struct {
	Subnet   Subnet      `json:"subnet"`
	Eligible bool        `json:"eligible"`
	Findings []Finding   `json:"findings,omitempty"`
	VMs      []VMSummary `json:"vms,omitempty"`
}

// Blockers returns the findings that made the subnet ineligible.
func (a SubnetAssessment) Blockers() []Finding {
	return filterBySeverity(a.Findings, SeverityBlocker)
}

// Warnings returns the non-blocking findings for the subnet.
func (a SubnetAssessment) Warnings() []Finding {
	return filterBySeverity(a.Findings, SeverityWarning)
}

func filterBySeverity(in []Finding, s Severity) []Finding {
	var out []Finding
	for _, f := range in {
		if f.Severity == s {
			out = append(out, f)
		}
	}
	return out
}

// SortFindings orders findings deterministically so that reports and logs from
// two runs over the same inventory can be diffed.
func SortFindings(in []Finding) {
	sort.SliceStable(in, func(i, j int) bool {
		a, b := in[i], in[j]
		if a.Severity != b.Severity {
			return severityRank(a.Severity) < severityRank(b.Severity)
		}
		if a.Check != b.Check {
			return a.Check < b.Check
		}
		if a.SubnetName != b.SubnetName {
			return a.SubnetName < b.SubnetName
		}
		if a.VMName != b.VMName {
			return a.VMName < b.VMName
		}
		return a.Message < b.Message
	})
}

func severityRank(s Severity) int {
	switch s {
	case SeverityBlocker:
		return 0
	case SeverityWarning:
		return 1
	case SeverityInfo:
		return 2
	default:
		return 3
	}
}
