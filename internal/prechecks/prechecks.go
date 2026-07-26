// Package prechecks implements the Prechecks component. It decides which basic
// VLAN subnets are safe to migrate and, for the ones that are not, says why and
// what the operator should do about it.
package prechecks

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/logging"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/model"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/parallel"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/service"
)

// Options configures a precheck run.
type Options struct {
	// Include restricts the candidates to these subnet names or UUIDs.
	Include []string
	// Exclude removes subnet names or UUIDs from the candidates.
	Exclude []string
	// Concurrency bounds the parallel API calls.
	Concurrency int
}

// Runner is the Prechecks component.
type Runner struct {
	pc   service.PrismCentral
	pe   service.PrismElement
	log  *logging.Logger
	opts Options
}

// New builds a Prechecks component. pe may be nil, in which case the protection
// domain and file server network checks are reported as incomplete rather than
// silently passing.
func New(pc service.PrismCentral, pe service.PrismElement, log *logging.Logger, opts Options) *Runner {
	if opts.Concurrency < 1 {
		opts.Concurrency = 8
	}
	return &Runner{pc: pc, pe: pe, log: log.WithPhase("prechecks"), opts: opts}
}

// Run gathers the inventory once, then evaluates the three checks concurrently
// across the candidate subnets.
func (r *Runner) Run(ctx context.Context) (*model.PrecheckReport, error) {
	report := &model.PrecheckReport{StartedAt: time.Now().UTC()}

	all, err := r.pc.ListSubnets(ctx)
	if err != nil {
		return nil, fmt.Errorf("list subnets: %w", err)
	}
	r.log.Infof("Prism Central reports %d subnets", len(all))

	candidates, global := r.selectCandidates(all)
	report.Global = append(report.Global, global...)
	if len(candidates) == 0 {
		r.log.Warnf("No basic VLAN subnet matched the selection, so there is nothing to migrate")
		report.FinishedAt = time.Now().UTC()
		return report, nil
	}
	r.log.Infof("Evaluating %d basic VLAN subnet(s): %s", len(candidates), describeSubnets(candidates))

	inv, err := Gather(ctx, r.pc, r.pe, r.log, r.opts.Concurrency, candidates)
	if err != nil {
		return nil, fmt.Errorf("gather inventory: %w", err)
	}

	// The duplicate MAC index and the dependency index are both global, so they
	// are built once, in parallel, before the per-subnet evaluation.
	var (
		duplicates []DuplicateMAC
		depIndex   *DependencyIndex
	)
	if err := parallel.Group(
		func() error {
			duplicates = FindDuplicateMACs(inv)
			r.log.Infof("Found %d MAC address(es) used by more than one vNIC across Prism Central", len(duplicates))
			return nil
		},
		func() error {
			depIndex = BuildDependencyIndex(ctx, r.pc, inv, candidates, r.log, r.opts.Concurrency)
			return nil
		},
	); err != nil {
		return nil, err
	}
	report.Global = append(report.Global, depIndex.Incomplete...)

	var (
		mu          sync.Mutex
		assessments = make([]model.SubnetAssessment, len(candidates))
	)
	err = parallel.ForEach(ctx, r.opts.Concurrency, indexes(len(candidates)), func(ctx context.Context, i int) error {
		subnet := candidates[i]
		log := r.log.With(logging.Fields{"subnet": subnet.Describe()})

		var (
			trunkFindings []model.Finding
			macFindings   []model.Finding
			depFindings   []model.Finding
		)
		// The three checks are independent reads over the shared inventory, so
		// they run concurrently for each subnet as well.
		if err := parallel.Group(
			func() error { trunkFindings = CheckTrunkedVnics(subnet, inv); return nil },
			func() error { macFindings = CheckDuplicateMACs(subnet, inv, duplicates); return nil },
			func() error { depFindings = CheckDependencies(subnet, inv, depIndex); return nil },
		); err != nil {
			return err
		}

		findings := append([]model.Finding{}, trunkFindings...)
		findings = append(findings, macFindings...)
		findings = append(findings, depFindings...)
		findings = append(findings, checkSubnetState(subnet)...)
		model.SortFindings(findings)

		assessment := model.SubnetAssessment{
			Subnet:   subnet,
			Findings: findings,
			VMs:      inv.VMSummaries(subnet.ExtID),
		}
		assessment.Eligible = len(assessment.Blockers()) == 0

		if assessment.Eligible {
			log.Infof("Eligible: %d VM(s) attached, no blocking condition found", len(assessment.VMs))
		} else {
			log.Warnf("Not eligible: %d blocking condition(s)", len(assessment.Blockers()))
			for _, f := range assessment.Blockers() {
				log.Warnf("  %s -- %s", f.Message, f.Remediation)
			}
		}
		for _, f := range assessment.Warnings() {
			log.Warnf("  %s", f.Message)
		}

		mu.Lock()
		assessments[i] = assessment
		mu.Unlock()
		return nil
	})
	if err != nil {
		return nil, err
	}

	report.Assessments = assessments
	report.FinishedAt = time.Now().UTC()

	eligible := report.Eligible()
	r.log.Infof("Prechecks complete: %d of %d subnet(s) eligible for migration", len(eligible), len(candidates))
	return report, nil
}

// selectCandidates narrows the full subnet list down to the basic VLAN subnets
// the operator asked for, explaining every exclusion.
func (r *Runner) selectCandidates(all []model.Subnet) ([]model.Subnet, []model.Finding) {
	include := newMatcher(r.opts.Include)
	exclude := newMatcher(r.opts.Exclude)

	var (
		candidates []model.Subnet
		global     []model.Finding
	)
	for _, subnet := range all {
		switch {
		case !include.empty() && !include.matches(subnet):
			continue
		case exclude.matches(subnet):
			r.log.Infof("Skipping %s because it is on the exclusion list", subnet.Describe())
			global = append(global, model.Finding{
				Check:       model.CheckSubnetState,
				Severity:    model.SeverityInfo,
				SubnetExtID: subnet.ExtID,
				SubnetName:  subnet.Name,
				Message:     "Excluded from this migration at the operator's request",
			})
			continue
		case subnet.Type != model.SubnetTypeVLAN:
			r.log.Debugf("Skipping %s because it is not a VLAN subnet", subnet.Describe())
			continue
		case subnet.IsAdvancedNetworking:
			r.log.Debugf("Skipping %s because it is already on the advanced stack", subnet.Describe())
			continue
		}
		candidates = append(candidates, subnet)
	}

	// Tell the operator when a name they asked for does not exist, rather than
	// silently migrating fewer subnets than they expected.
	for _, wanted := range r.opts.Include {
		if !matchesAny(wanted, candidates) {
			global = append(global, model.Finding{
				Check:    model.CheckSubnetState,
				Severity: model.SeverityWarning,
				Message:  fmt.Sprintf("Requested subnet %q is not a basic VLAN subnet on this Prism Central, so it was not considered", wanted),
			})
			r.log.Warnf("Requested subnet %q did not match any basic VLAN subnet", wanted)
		}
	}
	return candidates, global
}

// checkSubnetState flags subnets that are mid-migration or in a failed migration
// state, which the migrate action would reject anyway.
func checkSubnetState(subnet model.Subnet) []model.Finding {
	switch subnet.MigrationState {
	case model.MigrationStateInProgress:
		return []model.Finding{{
			Check:       model.CheckSubnetState,
			Severity:    model.SeverityBlocker,
			SubnetExtID: subnet.ExtID,
			SubnetName:  subnet.Name,
			Message:     "A subnet migration is already in progress for this subnet",
			Remediation: model.RemediationRetryLater,
		}}
	case model.MigrationStateFailed:
		return []model.Finding{{
			Check:       model.CheckSubnetState,
			Severity:    model.SeverityWarning,
			SubnetExtID: subnet.ExtID,
			SubnetName:  subnet.Name,
			Message:     "A previous migration attempt for this subnet failed; retrying may fail for the same reason",
			Remediation: model.RemediationNone,
		}}
	default:
		return nil
	}
}

type matcher struct {
	values map[string]struct{}
}

func newMatcher(values []string) matcher {
	m := matcher{values: make(map[string]struct{}, len(values))}
	for _, v := range values {
		m.values[strings.ToLower(strings.TrimSpace(v))] = struct{}{}
	}
	return m
}

func (m matcher) empty() bool { return len(m.values) == 0 }

func (m matcher) matches(subnet model.Subnet) bool {
	if m.empty() {
		return false
	}
	if _, ok := m.values[strings.ToLower(subnet.ExtID)]; ok {
		return true
	}
	if _, ok := m.values[strings.ToLower(subnet.Name)]; ok {
		return true
	}
	return false
}

func matchesAny(wanted string, subnets []model.Subnet) bool {
	wanted = strings.ToLower(strings.TrimSpace(wanted))
	for _, s := range subnets {
		if strings.ToLower(s.ExtID) == wanted || strings.ToLower(s.Name) == wanted {
			return true
		}
	}
	return false
}

func describeSubnets(subnets []model.Subnet) string {
	parts := make([]string, 0, len(subnets))
	for _, s := range subnets {
		parts = append(parts, s.Describe())
	}
	return strings.Join(parts, ", ")
}

func indexes(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}
