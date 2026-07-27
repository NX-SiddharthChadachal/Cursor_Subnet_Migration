// Package postchecks implements the Postchecks component. It runs after every
// eligible subnet has been migrated and answers two questions: did the subnets
// actually land on the advanced stack, and did the migration leave any duplicate
// MAC addresses behind.
package postchecks

import (
	"context"
	"fmt"
	"time"

	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/logging"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/model"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/prechecks"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/service"
)

// Options configures a postcheck run.
type Options struct {
	Concurrency int
}

// Runner is the Postchecks component.
type Runner struct {
	pc   service.PrismCentral
	log  *logging.Logger
	opts Options
}

// New builds a Postchecks component.
func New(pc service.PrismCentral, log *logging.Logger, opts Options) *Runner {
	if opts.Concurrency < 1 {
		opts.Concurrency = 8
	}
	return &Runner{pc: pc, log: log.WithPhase("postchecks"), opts: opts}
}

// Run re-reads the migrated subnets and rebuilds the MAC index from scratch.
//
// The MAC check is repeated rather than reused from the prechecks on purpose:
// moving a port between the Acropolis and Atlas stacks is exactly the operation
// that can produce a collision, so a fresh read is the only meaningful check.
func (r *Runner) Run(ctx context.Context, migrated []model.Subnet) (*model.PostcheckReport, error) {
	report := &model.PostcheckReport{StartedAt: time.Now().UTC()}
	defer func() { report.FinishedAt = time.Now().UTC() }()

	if len(migrated) == 0 {
		r.log.Infof("No subnet was migrated, so there is nothing to verify")
		return report, nil
	}

	// Confirm each subnet reports the advanced stack now.
	verified := make([]model.Subnet, 0, len(migrated))
	for _, subnet := range migrated {
		current, err := r.pc.GetSubnet(ctx, subnet.ExtID)
		if err != nil {
			r.log.Warnf("Could not re-read subnet %s: %v", subnet.Describe(), err)
			report.Findings = append(report.Findings, model.Finding{
				Check:       model.CheckMigrationResult,
				Severity:    model.SeverityWarning,
				SubnetExtID: subnet.ExtID,
				SubnetName:  subnet.Name,
				Message:     fmt.Sprintf("Could not confirm the post-migration state of this subnet: %v", err),
			})
			continue
		}

		switch {
		case current.IsAdvancedNetworking:
			r.log.Infof("Subnet %s is on the advanced stack (migration state %s)", current.Describe(), stateOrNone(current.MigrationState))
			verified = append(verified, current)
		default:
			r.log.Errorf("Subnet %s still reports the basic stack after migration (migration state %s)", current.Describe(), stateOrNone(current.MigrationState))
			report.Findings = append(report.Findings, model.Finding{
				Check:       model.CheckMigrationResult,
				Severity:    model.SeverityBlocker,
				SubnetExtID: current.ExtID,
				SubnetName:  current.Name,
				Message: fmt.Sprintf("The migration task finished but this subnet still reports the basic network stack (migration state %s)",
					stateOrNone(current.MigrationState)),
				Remediation: model.RemediationSupportTicket,
			})
		}
	}
	report.VerifiedSubnets = verified

	// Rebuild the MAC index across the whole Prism Central.
	inv, err := prechecks.Gather(ctx, r.pc, nil, r.log, r.opts.Concurrency, migrated)
	if err != nil {
		return nil, fmt.Errorf("re-read inventory for the duplicate MAC postcheck: %w", err)
	}
	duplicates := prechecks.FindDuplicateMACs(inv)
	if len(duplicates) == 0 {
		r.log.Infof("No duplicate MAC address found after the migration")
	} else {
		r.log.Errorf("Found %d duplicate MAC address(es) after the migration", len(duplicates))
	}

	for _, subnet := range migrated {
		for _, finding := range prechecks.CheckDuplicateMACs(subnet, inv, duplicates) {
			finding.Message = "After migration: " + finding.Message
			report.Findings = append(report.Findings, finding)
			r.log.Errorf("Subnet %s: %s -- %s", subnet.Describe(), finding.Message, finding.Remediation)
		}
	}

	model.SortFindings(report.Findings)
	return report, nil
}

func stateOrNone(state model.MigrationState) string {
	if state == model.MigrationStateNone {
		return "not reported"
	}
	return string(state)
}
