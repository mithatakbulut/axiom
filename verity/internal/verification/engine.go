package verification

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	candidatepkg "verity/internal/candidate"
)

// ReviewResult is advisory input. It cannot turn a failed or missing check into a pass.
type ReviewResult struct {
	Completed     bool     `json:"completed"`
	Findings      []string `json:"findings,omitempty"`
	Advisory      string   `json:"advisory,omitempty"`
	HumanApproved *bool    `json:"human_approved,omitempty"`
	HumanActor    string   `json:"human_actor,omitempty"`
}

type ReviewRequest struct {
	CandidateSHA string
	Criteria     []Criterion
	SnapshotPath string
}
type Reviewer interface {
	Review(context.Context, ReviewRequest) (ReviewResult, error)
}

type CurrentCandidate interface {
	Current(context.Context) (string, error)
}
type Engine struct {
	Runner              CheckRunner
	Candidate           CurrentCandidate
	CandidateKind       CandidateKind
	CandidateBase       string
	CandidateRepository string
	Now                 func() time.Time
}

// Verify executes an explicitly approved plan and evaluates evidence against
// acceptance criteria. The returned state is derived only from check records,
// candidate identity, criteria coverage, and whether human review remains.
func (e Engine) Verify(ctx context.Context, repository, runID, workflowID, taskID string, m Manifest, profile Profile, candidate string, approved bool) (Run, error) {
	plan, err := m.Plan(candidate, profile)
	if err != nil {
		return Run{}, err
	}
	if !approved {
		return Run{}, ErrNotApproved
	}
	now := e.Now
	if now == nil {
		now = time.Now
	}
	startedAt := now().UTC()
	run := Run{ID: runID, SchemaVersion: CurrentRunSchemaVersion, WorkflowID: workflowID, TaskID: taskID, CandidateKind: e.CandidateKind, CandidateSHA: candidate, CandidateBase: e.CandidateBase, CandidateRepository: e.CandidateRepository, TrustLevel: plan.TrustLevel, IsolationPolicy: plan.IsolationPolicy, ContainerImage: plan.ContainerImage, NetworkAllowlist: append([]string(nil), plan.NetworkAllowlist...), Profile: profile, Criteria: append([]Criterion(nil), m.Criteria...), StartedAt: startedAt}
	if e.CandidateKind != "" {
		run.Candidate = candidatepkg.Identity{SchemaVersion: 1, Kind: e.CandidateKind, ID: candidate, Repository: e.CandidateRepository, BaseRevision: e.CandidateBase, CapturedAt: startedAt, Provenance: "verification-engine:" + runID}
	}
	manifestJSON, err := json.Marshal(m)
	if err != nil {
		return Run{}, fmt.Errorf("serialize verification manifest: %w", err)
	}
	manifestDigest := sha256.Sum256(manifestJSON)
	run.ManifestSHA256 = hex.EncodeToString(manifestDigest[:])
	if e.Candidate != nil {
		current, resolveErr := e.Candidate.Current(ctx)
		if resolveErr != nil {
			return Run{}, fmt.Errorf("resolve current candidate before verification: %w", resolveErr)
		}
		if current != candidate {
			run.State = StateStale
			run.FinishedAt = now().UTC()
			return run, nil
		}
	}
	runner := e.Runner
	if runner == nil {
		runner = CommandRunner{}
	}
	var checks []CheckResult
	if plan.TrustLevel == TrustUntrusted || plan.IsolationPolicy == IsolationRootlessContainer {
		switch runner.(type) {
		case CommandRunner, *CommandRunner, ContainerRunner, *ContainerRunner:
			checks, err = runner.Execute(ctx, repository, plan, true)
		default:
			checks = blockedChecks(plan, "configured verification runner does not provide rootless container isolation")
		}
	} else {
		checks, err = runner.Execute(ctx, repository, plan, true)
	}
	if err != nil {
		return Run{}, err
	}
	type checkPolicy struct {
		credentials []PlannedCredential
		network     []string
	}
	checkPolicies := make(map[string]checkPolicy, len(plan.Checks))
	for _, check := range plan.Checks {
		network := plan.NetworkAllowlist
		if len(check.NetworkAllowlist) > 0 {
			network = check.NetworkAllowlist
		}
		checkPolicies[check.ID] = checkPolicy{credentials: append([]PlannedCredential(nil), check.Credentials...), network: append([]string(nil), network...)}
	}
	for i := range checks {
		policy := checkPolicies[checks[i].ID]
		checks[i].CredentialBindings = policy.credentials
		checks[i].NetworkAllowlist = policy.network
	}
	run.Checks = checks
	if e.Candidate != nil {
		current, resolveErr := e.Candidate.Current(ctx)
		if resolveErr != nil {
			return Run{}, fmt.Errorf("resolve current candidate after verification: %w", resolveErr)
		}
		if current != candidate {
			run.State = StateStale
		}
	}
	if ctx.Err() != nil {
		run.State = StateCancelled
	} else if run.State != StateStale {
		run.State = Evaluate(run, m.Review)
	}
	run.FinishedAt = now().UTC()
	if err := run.Validate(); err != nil {
		return Run{}, err
	}
	return run, nil
}

func Evaluate(run Run, reviewRequired bool) State {
	if run.State == StateStale {
		return StateStale
	}
	if run.State == StateCancelled {
		return StateCancelled
	}
	if len(run.Checks) == 0 {
		return StateBlocked
	}
	failed, blocked, partial := false, false, false
	covered := map[string]bool{}
	for _, check := range run.Checks {
		for _, id := range check.CriteriaIDs {
			if check.Outcome == Passed {
				covered[id] = true
			}
		}
		if !check.Required {
			continue
		}
		switch check.Outcome {
		case Failed:
			failed = true
		case Blocked, TimedOut:
			blocked = true
		case Skipped:
			partial = true
		}
	}
	for _, criterion := range run.Criteria {
		if !covered[criterion.ID] {
			partial = true
		}
	}
	if failed {
		return StateFail
	}
	if blocked {
		return StateBlocked
	}
	if partial {
		return StatePartial
	}
	if reviewRequired {
		if run.Review == nil || !run.Review.Completed || run.Review.HumanApproved == nil || strings.TrimSpace(run.Review.HumanActor) == "" {
			return StateNeedsHuman
		}
		if !*run.Review.HumanApproved {
			return StateFail
		}
	}
	// An empty criteria list is permitted, but every required check still needs
	// an explicit PASS observation. Optional checks cannot establish acceptance.
	requiredCount := 0
	for _, check := range run.Checks {
		if check.Required {
			requiredCount++
		}
		if check.Required && check.Outcome != Passed {
			return StateBlocked
		}
	}
	if requiredCount == 0 {
		return StateBlocked
	}
	return StatePass
}

// MarkStale invalidates a completed run when its candidate identity changes.
func MarkStale(run Run, candidate string) Run {
	if candidate != run.CandidateSHA {
		run.State = StateStale
	}
	return run
}
