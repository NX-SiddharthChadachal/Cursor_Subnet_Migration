// Package report renders the run report as human readable text and as JSON.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/model"
)

// WriteJSON writes the run report to path, creating parent directories.
func WriteJSON(path string, run *model.RunReport) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create report directory %s: %w", dir, err)
		}
	}
	encoded, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		return fmt.Errorf("write report %s: %w", path, err)
	}
	return nil
}

// WriteText renders the operator-facing summary: which subnets were migrated and
// which VMs they carry, then which subnets were not and what to do about them.
func WriteText(w io.Writer, run *model.RunReport) {
	b := &strings.Builder{}

	section(b, "Environment")
	fmt.Fprintf(b, "  Prism Central : %s (%s %s)\n", run.Environment.PrismCentral.Address,
		run.Environment.PrismCentral.Version.Product, run.Environment.PrismCentral.Version)
	if pe := run.Environment.PrismElement; pe != nil {
		fmt.Fprintf(b, "  Prism Element : %s (%s %s)\n", pe.Address, pe.Version.Product, pe.Version)
	} else {
		fmt.Fprintf(b, "  Prism Element : not supplied\n")
	}
	fmt.Fprintf(b, "  Service layer : %s\n", run.Environment.Backend)
	for _, c := range run.Environment.NetworkControllers {
		fmt.Fprintf(b, "  Flow VN       : version %s, status %s, default VLAN stack %s\n", c.Version, c.Status, c.DefaultVlanStack)
	}
	if run.DryRun {
		fmt.Fprintf(b, "  Mode          : dry run, no subnet was modified\n")
	}

	if run.Execution != nil {
		migrated := run.Execution.Migrated()
		section(b, fmt.Sprintf("Subnets migrated (%d)", len(migrated)))
		if len(migrated) == 0 {
			fmt.Fprintf(b, "  none\n")
		}
		for _, outcome := range migrated {
			fmt.Fprintf(b, "  %s\n", outcome.Subnet.Describe())
			fmt.Fprintf(b, "    uuid : %s\n", outcome.Subnet.ExtID)
			fmt.Fprintf(b, "    task : %s\n", outcome.TaskExtID)
			writeVMs(b, outcome.VMs)
		}

		if failed := run.Execution.Failed(); len(failed) > 0 {
			section(b, fmt.Sprintf("Subnets that failed to migrate (%d)", len(failed)))
			for _, outcome := range failed {
				fmt.Fprintf(b, "  %s (uuid %s)\n", outcome.Subnet.Describe(), outcome.Subnet.ExtID)
				fmt.Fprintf(b, "    task   : %s\n", orNone(outcome.TaskExtID))
				fmt.Fprintf(b, "    status : %s\n", outcome.Status)
				if outcome.Error != "" {
					fmt.Fprintf(b, "    reason : %s\n", outcome.Error)
				}
			}
		}

		if len(run.Execution.Skipped) > 0 {
			section(b, fmt.Sprintf("Eligible subnets not attempted (%d)", len(run.Execution.Skipped)))
			for _, subnet := range run.Execution.Skipped {
				fmt.Fprintf(b, "  %s (uuid %s)\n", subnet.Describe(), subnet.ExtID)
			}
		}
	}

	if run.Prechecks != nil {
		if run.DryRun || run.Execution == nil {
			eligible := run.Prechecks.Eligible()
			section(b, fmt.Sprintf("Subnets eligible for migration (%d)", len(eligible)))
			if len(eligible) == 0 {
				fmt.Fprintf(b, "  none\n")
			}
			for _, subnet := range eligible {
				fmt.Fprintf(b, "  %s (uuid %s)\n", subnet.Describe(), subnet.ExtID)
				if assessment, ok := run.Prechecks.Assessment(subnet.ExtID); ok {
					writeVMs(b, assessment.VMs)
				}
			}
		}

		ineligible := run.Prechecks.Ineligible()
		section(b, fmt.Sprintf("Subnets that cannot be migrated (%d)", len(ineligible)))
		if len(ineligible) == 0 {
			fmt.Fprintf(b, "  none\n")
		}
		for _, assessment := range ineligible {
			fmt.Fprintf(b, "  %s (uuid %s)\n", assessment.Subnet.Describe(), assessment.Subnet.ExtID)
			for _, finding := range assessment.Blockers() {
				fmt.Fprintf(b, "    - [%s] %s\n", finding.Check, finding.Message)
				if finding.Remediation != model.RemediationNone {
					fmt.Fprintf(b, "      action: %s\n", finding.Remediation)
				}
			}
			writeVMs(b, assessment.VMs)
		}

		var warnings []model.Finding
		warnings = append(warnings, filterWarnings(run.Prechecks.Global)...)
		for _, assessment := range run.Prechecks.Assessments {
			warnings = append(warnings, assessment.Warnings()...)
		}
		if len(warnings) > 0 {
			section(b, fmt.Sprintf("Checks that need attention (%d)", len(warnings)))
			for _, finding := range warnings {
				scope := finding.SubnetName
				if scope == "" {
					scope = "run"
				}
				fmt.Fprintf(b, "  - [%s] %s: %s\n", finding.Check, scope, finding.Message)
			}
		}
	}

	if run.Postchecks != nil && len(run.Postchecks.Findings) > 0 {
		section(b, fmt.Sprintf("Postcheck findings (%d)", len(run.Postchecks.Findings)))
		for _, finding := range run.Postchecks.Findings {
			fmt.Fprintf(b, "  - [%s][%s] %s\n", finding.Severity, finding.Check, finding.Message)
			if finding.Remediation != model.RemediationNone {
				fmt.Fprintf(b, "    action: %s\n", finding.Remediation)
			}
		}
	}

	if run.AbortReason != "" {
		section(b, "Run stopped early")
		fmt.Fprintf(b, "  %s\n", run.AbortReason)
	}

	fmt.Fprint(w, b.String())
}

func writeVMs(b *strings.Builder, vms []model.VMSummary) {
	if len(vms) == 0 {
		fmt.Fprintf(b, "    vms  : none attached\n")
		return
	}
	fmt.Fprintf(b, "    vms  : %d\n", len(vms))
	for _, vm := range vms {
		name := vm.Name
		if name == "" {
			name = vm.ExtID
		}
		line := fmt.Sprintf("      - %s (uuid %s", name, vm.ExtID)
		if vm.ClusterName != "" {
			line += ", cluster " + vm.ClusterName
		}
		if len(vm.MACAddresses) > 0 {
			line += ", mac " + strings.Join(vm.MACAddresses, "/")
		}
		line += ")"
		fmt.Fprintln(b, line)
	}
}

func filterWarnings(in []model.Finding) []model.Finding {
	var out []model.Finding
	for _, f := range in {
		if f.Severity == model.SeverityWarning {
			out = append(out, f)
		}
	}
	return out
}

func section(b *strings.Builder, title string) {
	fmt.Fprintf(b, "\n%s\n%s\n", title, strings.Repeat("-", len(title)))
}

func orNone(s string) string {
	if s == "" {
		return "not submitted"
	}
	return s
}
