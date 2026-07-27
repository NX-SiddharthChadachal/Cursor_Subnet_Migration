package model

import "time"

// PrecheckReport is what the Prechecks component hands back to the Controller.
type PrecheckReport struct {
	StartedAt   time.Time          `json:"startedAt"`
	FinishedAt  time.Time          `json:"finishedAt"`
	Assessments []SubnetAssessment `json:"assessments"`
	// Global holds findings that are not attributable to a single subnet, such
	// as an unreachable optional API.
	Global []Finding `json:"global,omitempty"`
}

// Eligible returns the subnets that cleared every blocking precheck.
func (r PrecheckReport) Eligible() []Subnet {
	out := make([]Subnet, 0, len(r.Assessments))
	for _, a := range r.Assessments {
		if a.Eligible {
			out = append(out, a.Subnet)
		}
	}
	return out
}

// Ineligible returns the assessments that carry at least one blocker.
func (r PrecheckReport) Ineligible() []SubnetAssessment {
	var out []SubnetAssessment
	for _, a := range r.Assessments {
		if !a.Eligible {
			out = append(out, a)
		}
	}
	return out
}

// Assessment looks up the assessment for a subnet UUID.
func (r PrecheckReport) Assessment(subnetExtID string) (SubnetAssessment, bool) {
	for _, a := range r.Assessments {
		if a.Subnet.ExtID == subnetExtID {
			return a, true
		}
	}
	return SubnetAssessment{}, false
}

// MigrationOutcome is the result of migrating one subnet.
type MigrationOutcome struct {
	Subnet     Subnet      `json:"subnet"`
	TaskExtID  string      `json:"taskExtId,omitempty"`
	Status     TaskStatus  `json:"status"`
	StartedAt  time.Time   `json:"startedAt"`
	FinishedAt time.Time   `json:"finishedAt"`
	Error      string      `json:"error,omitempty"`
	VMs        []VMSummary `json:"vms,omitempty"`
}

// Succeeded reports whether the subnet reached the advanced stack.
func (o MigrationOutcome) Succeeded() bool {
	return o.Status == TaskSucceeded && o.Error == ""
}

// ExecutionReport is what the Executioner hands back to the Controller.
type ExecutionReport struct {
	StartedAt  time.Time          `json:"startedAt"`
	FinishedAt time.Time          `json:"finishedAt"`
	Outcomes   []MigrationOutcome `json:"outcomes"`
	// Skipped lists subnets that were eligible but never attempted, for example
	// because the run was aborted after an earlier failure.
	Skipped []Subnet `json:"skipped,omitempty"`
}

// Migrated returns the outcomes that completed successfully.
func (r ExecutionReport) Migrated() []MigrationOutcome {
	var out []MigrationOutcome
	for _, o := range r.Outcomes {
		if o.Succeeded() {
			out = append(out, o)
		}
	}
	return out
}

// Failed returns the outcomes that did not complete successfully.
func (r ExecutionReport) Failed() []MigrationOutcome {
	var out []MigrationOutcome
	for _, o := range r.Outcomes {
		if !o.Succeeded() {
			out = append(out, o)
		}
	}
	return out
}

// PostcheckReport is what the Postchecks component hands back to the Controller.
type PostcheckReport struct {
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
	Findings   []Finding `json:"findings,omitempty"`
	// VerifiedSubnets lists the subnets confirmed to be on the advanced stack
	// after the migration.
	VerifiedSubnets []Subnet `json:"verifiedSubnets,omitempty"`
}

// HasBlockers reports whether any postcheck produced a blocking finding.
func (r PostcheckReport) HasBlockers() bool {
	for _, f := range r.Findings {
		if f.Severity == SeverityBlocker {
			return true
		}
	}
	return false
}

// EnvironmentInfo captures what the preflight stage discovered about the target.
type EnvironmentInfo struct {
	PrismCentral       EndpointInfo        `json:"prismCentral"`
	PrismElement       *EndpointInfo       `json:"prismElement,omitempty"`
	Clusters           []Cluster           `json:"clusters,omitempty"`
	NetworkControllers []NetworkController `json:"networkControllers,omitempty"`
	// Backend names the service layer the run used ("go-sdk" or "v4-rest").
	Backend string `json:"backend"`
}

// EndpointInfo is the reachability and version summary for one endpoint.
type EndpointInfo struct {
	Kind          EndpointKind   `json:"kind"`
	Address       string         `json:"address"`
	TCPReachable  bool           `json:"tcpReachable"`
	ICMPReachable *bool          `json:"icmpReachable,omitempty"`
	Version       ProductVersion `json:"version"`
	Supported     bool           `json:"supported"`
}

// RunReport is the complete record of a run: the input environment plus the
// output of all three phases.
type RunReport struct {
	Tool        string           `json:"tool"`
	Version     string           `json:"version"`
	StartedAt   time.Time        `json:"startedAt"`
	FinishedAt  time.Time        `json:"finishedAt"`
	DryRun      bool             `json:"dryRun"`
	Environment EnvironmentInfo  `json:"environment"`
	Prechecks   *PrecheckReport  `json:"prechecks,omitempty"`
	Execution   *ExecutionReport `json:"execution,omitempty"`
	Postchecks  *PostcheckReport `json:"postchecks,omitempty"`
	// AbortReason is set when the Controller stopped before finishing.
	AbortReason string `json:"abortReason,omitempty"`
}
