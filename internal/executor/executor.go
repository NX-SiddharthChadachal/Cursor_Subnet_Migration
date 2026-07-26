// Package executor implements the Executioner component: it submits the subnet
// migration for the eligible subnets the Controller hands it and returns the
// task UUID for each one.
package executor

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/logging"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/model"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/service"
)

// ErrAborted is returned when the run stopped because a migration failed and
// ContinueOnFailure was not set.
var ErrAborted = errors.New("migration aborted after a failure")

// Options configures the Executioner.
type Options struct {
	// BatchSize is how many subnets go into one migrate-subnets call. The
	// default of one keeps the rollout strictly rolling, so a failure is
	// attributable to a single subnet.
	BatchSize int
	// PollInterval is how often the migration task is polled.
	PollInterval time.Duration
	// TaskTimeout bounds one migration task.
	TaskTimeout time.Duration
	// ContinueOnFailure keeps going after a batch fails.
	ContinueOnFailure bool
}

// TaskObserver is notified as soon as a migration task UUID is known and again
// on every poll. The Controller uses it to keep the run log current, which is
// what makes a long migration legible while it is happening.
type TaskObserver interface {
	TaskSubmitted(subnets []model.Subnet, taskExtID string)
	TaskProgress(subnets []model.Subnet, task model.Task)
}

// Runner is the Executioner component.
type Runner struct {
	pc       service.PrismCentral
	log      *logging.Logger
	opts     Options
	observer TaskObserver
}

// New builds an Executioner.
func New(pc service.PrismCentral, log *logging.Logger, opts Options, observer TaskObserver) *Runner {
	if opts.BatchSize < 1 {
		opts.BatchSize = 1
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 10 * time.Second
	}
	if opts.TaskTimeout <= 0 {
		opts.TaskTimeout = 30 * time.Minute
	}
	return &Runner{pc: pc, log: log.WithPhase("execution"), opts: opts, observer: observer}
}

// Run migrates the eligible subnets in a rolling fashion and polls each task to
// completion before moving on.
func (r *Runner) Run(ctx context.Context, eligible []model.Subnet, vmsBySubnet map[string][]model.VMSummary) (*model.ExecutionReport, error) {
	report := &model.ExecutionReport{StartedAt: time.Now().UTC()}
	defer func() { report.FinishedAt = time.Now().UTC() }()

	if len(eligible) == 0 {
		r.log.Infof("No eligible subnet to migrate")
		return report, nil
	}

	batches := chunk(eligible, r.opts.BatchSize)
	r.log.Infof("Migrating %d subnet(s) in %d batch(es) of up to %d", len(eligible), len(batches), r.opts.BatchSize)

	for i, batch := range batches {
		if err := ctx.Err(); err != nil {
			report.Skipped = append(report.Skipped, flatten(batches[i:])...)
			return report, err
		}

		outcomes := r.migrateBatch(ctx, batch, vmsBySubnet)
		report.Outcomes = append(report.Outcomes, outcomes...)

		if failedAny(outcomes) && !r.opts.ContinueOnFailure {
			remaining := flatten(batches[i+1:])
			report.Skipped = append(report.Skipped, remaining...)
			if len(remaining) > 0 {
				r.log.Errorf("Stopping after the failed batch; %d subnet(s) were not attempted. Re-run with --continue-on-failure to migrate the rest despite a failure.", len(remaining))
			}
			return report, ErrAborted
		}
	}

	return report, nil
}

func (r *Runner) migrateBatch(ctx context.Context, batch []model.Subnet, vmsBySubnet map[string][]model.VMSummary) []model.MigrationOutcome {
	started := time.Now().UTC()
	log := r.log.With(logging.Fields{"subnet": describe(batch)})

	extIDs := make([]string, 0, len(batch))
	for _, s := range batch {
		extIDs = append(extIDs, s.ExtID)
	}

	log.Infof("Submitting the basic-to-advanced migration")
	taskExtID, err := r.pc.MigrateSubnets(ctx, extIDs)
	if err != nil {
		log.Errorf("Submission failed: %v", err)
		return outcomesFor(batch, vmsBySubnet, "", model.TaskFailed, started, err.Error())
	}

	log.Infof("Migration task %s accepted", taskExtID)
	if r.observer != nil {
		r.observer.TaskSubmitted(batch, taskExtID)
	}

	task, err := r.pollTask(ctx, batch, taskExtID, log)
	if err != nil {
		return outcomesFor(batch, vmsBySubnet, taskExtID, model.TaskUnknown, started, err.Error())
	}

	if task.Status == model.TaskSucceeded {
		log.Infof("Migration task %s succeeded", taskExtID)
		return outcomesFor(batch, vmsBySubnet, taskExtID, task.Status, started, "")
	}
	reason := task.FailureReason()
	log.Errorf("Migration task %s ended as %s: %s", taskExtID, task.Status, reason)
	return outcomesFor(batch, vmsBySubnet, taskExtID, task.Status, started, reason)
}

// pollTask watches a migration task until it reaches a terminal state, the
// timeout expires or the context is cancelled.
func (r *Runner) pollTask(ctx context.Context, batch []model.Subnet, taskExtID string, log *logging.Logger) (model.Task, error) {
	deadline := time.Now().Add(r.opts.TaskTimeout)
	ticker := time.NewTicker(r.opts.PollInterval)
	defer ticker.Stop()

	var (
		lastStatus   model.TaskStatus
		lastProgress = -1
		// A handful of consecutive read failures is tolerated: Prism can drop
		// requests while services restart during the migration.
		consecutiveErrors int
	)
	const maxConsecutiveErrors = 5

	for {
		task, err := r.pc.GetTask(ctx, taskExtID)
		if err != nil {
			consecutiveErrors++
			if consecutiveErrors >= maxConsecutiveErrors {
				return model.Task{}, fmt.Errorf("gave up polling task %s after %d consecutive read failures: %w", taskExtID, consecutiveErrors, err)
			}
			log.Warnf("Could not read task %s (attempt %d of %d): %v", taskExtID, consecutiveErrors, maxConsecutiveErrors, err)
		} else {
			consecutiveErrors = 0
			if task.Status != lastStatus || task.ProgressPercentage != lastProgress {
				log.Infof("Task %s is %s (%d%%)", taskExtID, task.Status, task.ProgressPercentage)
				lastStatus, lastProgress = task.Status, task.ProgressPercentage
			}
			if r.observer != nil {
				r.observer.TaskProgress(batch, task)
			}
			if task.Status.IsTerminal() {
				return task, nil
			}
		}

		if time.Now().After(deadline) {
			return model.Task{}, fmt.Errorf("migration task %s did not finish within %s (last known state %s)", taskExtID, r.opts.TaskTimeout, lastStatus)
		}

		select {
		case <-ctx.Done():
			return model.Task{}, fmt.Errorf("stopped polling task %s: %w", taskExtID, ctx.Err())
		case <-ticker.C:
		}
	}
}

func outcomesFor(batch []model.Subnet, vmsBySubnet map[string][]model.VMSummary, taskExtID string, status model.TaskStatus, started time.Time, errMessage string) []model.MigrationOutcome {
	finished := time.Now().UTC()
	out := make([]model.MigrationOutcome, 0, len(batch))
	for _, subnet := range batch {
		out = append(out, model.MigrationOutcome{
			Subnet:     subnet,
			TaskExtID:  taskExtID,
			Status:     status,
			StartedAt:  started,
			FinishedAt: finished,
			Error:      errMessage,
			VMs:        vmsBySubnet[subnet.ExtID],
		})
	}
	return out
}

func failedAny(outcomes []model.MigrationOutcome) bool {
	for _, o := range outcomes {
		if !o.Succeeded() {
			return true
		}
	}
	return false
}

func chunk(subnets []model.Subnet, size int) [][]model.Subnet {
	if size < 1 {
		size = 1
	}
	var out [][]model.Subnet
	for i := 0; i < len(subnets); i += size {
		end := i + size
		if end > len(subnets) {
			end = len(subnets)
		}
		out = append(out, subnets[i:end])
	}
	return out
}

func flatten(batches [][]model.Subnet) []model.Subnet {
	var out []model.Subnet
	for _, b := range batches {
		out = append(out, b...)
	}
	return out
}

func describe(batch []model.Subnet) string {
	if len(batch) == 1 {
		return batch[0].Describe()
	}
	parts := make([]string, 0, len(batch))
	for _, s := range batch {
		parts = append(parts, s.Describe())
	}
	return fmt.Sprintf("batch[%s]", joinComma(parts))
}

func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}
