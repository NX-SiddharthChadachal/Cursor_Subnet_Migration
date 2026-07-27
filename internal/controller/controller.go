// Package controller implements the Controller component: the brain that runs
// Prechecks, then the Executioner, then Postchecks, polls the migration tasks
// and keeps the run log coherent across all three.
package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/config"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/executor"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/logging"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/model"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/postchecks"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/prechecks"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/preflight"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/version"
)

// Controller owns a run.
type Controller struct {
	cfg   *config.Config
	log   *logging.Logger
	pre   *preflight.Result
	stdin io.Reader
	out   io.Writer
}

// New builds a Controller over an already validated environment.
func New(cfg *config.Config, log *logging.Logger, pre *preflight.Result, stdin io.Reader, out io.Writer) *Controller {
	return &Controller{cfg: cfg, log: log.WithPhase("controller"), pre: pre, stdin: stdin, out: out}
}

// Run executes the three phases in order and returns the complete run report.
// The report is returned even when a phase fails, so that a partial run is still
// auditable.
func (c *Controller) Run(ctx context.Context) (*model.RunReport, error) {
	run := &model.RunReport{
		Tool:        "subnet-migrator",
		Version:     version.Tool,
		StartedAt:   time.Now().UTC(),
		DryRun:      c.cfg.DryRun,
		Environment: c.pre.Environment,
	}
	defer func() { run.FinishedAt = time.Now().UTC() }()

	// Phase 1: prechecks.
	c.log.Infof("Starting prechecks")
	pre := prechecks.New(c.pre.PC, c.pre.PE, c.log, prechecks.Options{
		Include:     c.cfg.SubnetFilter,
		Exclude:     c.cfg.ExcludeSubnets,
		Concurrency: c.cfg.Concurrency,
	})
	precheckReport, err := pre.Run(ctx)
	if err != nil {
		run.AbortReason = fmt.Sprintf("prechecks failed: %v", err)
		return run, err
	}
	run.Prechecks = precheckReport

	eligible := precheckReport.Eligible()
	vmsBySubnet := vmsBySubnet(precheckReport)

	for _, assessment := range precheckReport.Ineligible() {
		c.log.Warnf("Subnet %s will not be migrated: %s", assessment.Subnet.Describe(), summarize(assessment.Blockers()))
	}

	if len(eligible) == 0 {
		c.log.Infof("No subnet is eligible for migration; stopping after the prechecks")
		run.AbortReason = "no eligible subnet after prechecks"
		return run, nil
	}

	if c.cfg.DryRun {
		c.log.Infof("Dry run: %d subnet(s) would be migrated, but nothing was changed", len(eligible))
		run.AbortReason = "dry run, migration not executed"
		return run, nil
	}

	if !c.cfg.AssumeYes {
		ok, err := c.confirm(eligible)
		if err != nil {
			run.AbortReason = fmt.Sprintf("could not read the confirmation: %v", err)
			return run, err
		}
		if !ok {
			c.log.Infof("Operator declined; nothing was migrated")
			run.AbortReason = "operator declined the migration"
			return run, nil
		}
	}

	// Phase 2: execution.
	c.log.Infof("Starting the migration of %d eligible subnet(s)", len(eligible))
	exec := executor.New(c.pre.PC, c.log, executor.Options{
		BatchSize:         c.cfg.BatchSize,
		PollInterval:      c.cfg.PollInterval,
		TaskTimeout:       c.cfg.TaskTimeout,
		ContinueOnFailure: c.cfg.ContinueOnFailure,
	}, c)
	executionReport, execErr := exec.Run(ctx, eligible, vmsBySubnet)
	run.Execution = executionReport

	migrated := make([]model.Subnet, 0, len(executionReport.Outcomes))
	for _, outcome := range executionReport.Migrated() {
		migrated = append(migrated, outcome.Subnet)
	}
	c.log.Infof("Migration finished: %d succeeded, %d failed, %d not attempted",
		len(migrated), len(executionReport.Failed()), len(executionReport.Skipped))

	// Phase 3: postchecks. These run even after a partial failure, because the
	// subnets that did migrate still need to be verified.
	if len(migrated) > 0 {
		c.log.Infof("Starting postchecks over %d migrated subnet(s)", len(migrated))
		post := postchecks.New(c.pre.PC, c.log, postchecks.Options{Concurrency: c.cfg.Concurrency})
		postReport, postErr := post.Run(ctx, migrated)
		run.Postchecks = postReport
		if postErr != nil {
			c.log.Errorf("Postchecks did not complete: %v", postErr)
			if execErr == nil {
				execErr = postErr
			}
		} else if postReport.HasBlockers() {
			c.log.Errorf("Postchecks found conditions that need a Nutanix support ticket")
		}
	} else {
		c.log.Warnf("Nothing migrated successfully, so the postchecks were skipped")
	}

	if execErr != nil {
		if errors.Is(execErr, executor.ErrAborted) {
			run.AbortReason = "migration stopped after a subnet failed; re-run with --continue-on-failure to process the rest"
		} else {
			run.AbortReason = execErr.Error()
		}
	}
	return run, execErr
}

// TaskSubmitted records a migration task UUID in the run log as soon as the
// Executioner has it, which is what an operator needs to follow the task in
// Prism.
func (c *Controller) TaskSubmitted(subnets []model.Subnet, taskExtID string) {
	for _, subnet := range subnets {
		c.log.With(logging.Fields{"subnet": subnet.Describe(), "task": taskExtID}).
			Infof("Migration task submitted")
	}
}

// TaskProgress records each state change of a migration task per subnet.
func (c *Controller) TaskProgress(subnets []model.Subnet, task model.Task) {
	for _, subnet := range subnets {
		log := c.log.With(logging.Fields{"subnet": subnet.Describe(), "task": task.ExtID})
		if task.Status.IsTerminal() {
			if task.Status == model.TaskSucceeded {
				log.Infof("Subnet migrated to the advanced stack")
			} else {
				log.Errorf("Subnet migration ended as %s: %s", task.Status, task.FailureReason())
			}
			continue
		}
		log.Debugf("Subnet migration is %s (%d%%)", task.Status, task.ProgressPercentage)
	}
}

func (c *Controller) confirm(eligible []model.Subnet) (bool, error) {
	fmt.Fprintf(c.out, "\nThe following %d subnet(s) will be migrated from the basic to the advanced VLAN stack:\n", len(eligible))
	for _, subnet := range eligible {
		fmt.Fprintf(c.out, "  - %s (uuid %s)\n", subnet.Describe(), subnet.ExtID)
	}
	fmt.Fprintln(c.out)
	return config.Confirm("Proceed with the migration?", c.stdin, c.out)
}

func vmsBySubnet(report *model.PrecheckReport) map[string][]model.VMSummary {
	out := make(map[string][]model.VMSummary, len(report.Assessments))
	for _, assessment := range report.Assessments {
		out[assessment.Subnet.ExtID] = assessment.VMs
	}
	return out
}

func summarize(findings []model.Finding) string {
	if len(findings) == 0 {
		return "no reason recorded"
	}
	out := ""
	for i, f := range findings {
		if i > 0 {
			out += "; "
		}
		out += string(f.Check) + ": " + f.Message
	}
	return out
}
