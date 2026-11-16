package verification

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testSHA() string { return hex.EncodeToString(make([]byte, 20)) }

func TestManifestValidationPlanAndExplicitApproval(t *testing.T) {
	m, err := ParseManifest([]byte(`version: 1
criteria:
  - id: c1
    description: behavior works
commands:
  test:
    command: [sh, -c, "exit 0"]
    required: true
    timeout_seconds: 2
    criteria: [c1]
profiles:
  standard: [test]
`))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := m.Plan(testSHA(), Standard)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Checks) != 1 || plan.Checks[0].ID != "test" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	if _, err := (CommandRunner{}).Execute(context.Background(), t.TempDir(), plan, false); !errors.Is(err, ErrNotApproved) {
		t.Fatalf("Execute without approval: %v", err)
	}
	if _, err := ParseManifest([]byte("version: 1\ncommands:\n  test:\n    command: 'go test ./...; rm -rf /'\n    required: true\n    timeout_seconds: 2\n")); !errors.Is(err, ErrUnsafeCommand) {
		t.Fatalf("shell command accepted: %v", err)
	}
}

func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	b, e := c.CombinedOutput()
	if e != nil {
		t.Fatalf("git %v: %v: %s", args, e, b)
	}
	return string(b)
}
func candidateRepo(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	gitCmd(t, root, "init")
	gitCmd(t, root, "config", "user.email", "test@example.invalid")
	gitCmd(t, root, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(root, "marker"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, root, "add", "marker")
	gitCmd(t, root, "commit", "-m", "candidate")
	return root, stringTrim(gitCmd(t, root, "rev-parse", "HEAD"))
}
func stringTrim(s string) string { return strings.TrimSpace(s) }

func TestRunnerUsesCandidateSnapshotAndRecordsCheckOutcomes(t *testing.T) {
	root, sha := candidateRepo(t)
	m, err := ParseManifest([]byte(`version: 1
commands:
  ok:
    command: [sh, -c, "test -f marker && echo yes"]
    required: true
    timeout_seconds: 2
  fail:
    command: [sh, -c, "exit 9"]
    required: false
    timeout_seconds: 2
`))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := m.Plan(sha, Custom)
	if err != nil {
		t.Fatal(err)
	}
	results, err := (CommandRunner{}).Execute(context.Background(), root, plan, true)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]CheckResult{}
	for _, result := range results {
		byID[result.ID] = result
	}
	if byID["ok"].Outcome != Passed || byID["ok"].Output != "yes\n" {
		t.Fatalf("ok result: %#v", byID["ok"])
	}
	if byID["fail"].Outcome != Failed || byID["fail"].ExitCode == nil || *byID["fail"].ExitCode != 9 {
		t.Fatalf("fail result: %#v", byID["fail"])
	}
	b, err := os.ReadFile(filepath.Join(root, "marker"))
	if err != nil || string(b) != "original" {
		t.Fatalf("candidate was modified: %q %v", b, err)
	}
}

func TestRunnerTimeoutAndCancellationAreNonPassing(t *testing.T) {
	root, sha := candidateRepo(t)
	m, err := ParseManifest([]byte(`version: 1
commands:
  slow:
    command: [sh, -c, "sleep 1"]
    required: true
    timeout_seconds: 1
`))
	if err != nil {
		t.Fatal(err)
	}
	plan, _ := m.Plan(sha, Standard)
	plan.Checks[0].Timeout = 10 * time.Millisecond
	results, err := (CommandRunner{}).Execute(context.Background(), root, plan, true)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Outcome != TimedOut {
		t.Fatalf("outcome %s", results[0].Outcome)
	}
}

func TestEvaluateEveryStateAndNoMissingCheckPass(t *testing.T) {
	base := Run{Checks: []CheckResult{{ID: "c", Name: "test", Required: true, Outcome: Passed, CriteriaIDs: []string{"criterion"}}}, Criteria: []Criterion{{ID: "criterion"}}}
	cases := []struct {
		name   string
		run    Run
		review bool
		want   State
	}{
		{"pass", base, false, StatePass},
		{"fail", Run{Checks: []CheckResult{{ID: "c", Name: "x", Required: true, Outcome: Failed}}}, false, StateFail},
		{"blocked", Run{Checks: []CheckResult{{ID: "c", Name: "x", Required: true, Outcome: Blocked}}}, false, StateBlocked},
		{"partial", Run{Checks: []CheckResult{{ID: "c", Name: "x", Required: true, Outcome: Skipped}}, Criteria: base.Criteria}, false, StatePartial},
		{"missing criteria", Run{Checks: []CheckResult{{ID: "c", Name: "x", Required: true, Outcome: Passed}}, Criteria: base.Criteria}, false, StatePartial},
		{"needs human", base, true, StateNeedsHuman},
		{"stale", Run{State: StateStale, Checks: base.Checks, Criteria: base.Criteria}, false, StateStale},
		{"cancelled", Run{State: StateCancelled, Checks: []CheckResult{{ID: "c", Name: "x", Required: true, Outcome: Blocked, Diagnostic: "verification cancelled"}}}, false, StateCancelled},
		{"no checks", Run{}, false, StateBlocked},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Evaluate(tc.run, tc.review); got != tc.want {
				t.Fatalf("state %s want %s", got, tc.want)
			}
		})
	}
}

type staticCandidate string

func (s staticCandidate) Current(context.Context) (string, error) { return string(s), nil }

type failingCandidate struct{ calls, failAt int }

func (c *failingCandidate) Current(context.Context) (string, error) {
	c.calls++
	if c.calls == c.failAt {
		return "", errors.New("candidate unavailable")
	}
	return testSHA(), nil
}

func TestCandidateResolutionFailureStopsVerification(t *testing.T) {
	repo, sha := candidateRepo(t)
	m := Manifest{Version: 1, Commands: map[string]CommandCheck{"x": {Command: []string{"true"}, Required: true, TimeoutSeconds: 1}}}
	for _, failAt := range []int{1, 2} {
		c := &failingCandidate{failAt: failAt}
		// The fixture repository's actual SHA must be returned on successful lookups.
		engine := Engine{Candidate: candidateWithFailure{sha: sha, failingCandidate: c}}
		if _, err := engine.Verify(context.Background(), repo, "run", "workflow", "", m, Standard, sha, true); err == nil {
			t.Fatalf("lookup %d: expected error", failAt)
		}
		if c.calls != failAt {
			t.Fatalf("lookup count %d, want %d", c.calls, failAt)
		}
	}
}

type candidateWithFailure struct {
	sha string
	*failingCandidate
}

func (c candidateWithFailure) Current(ctx context.Context) (string, error) {
	_, err := c.failingCandidate.Current(ctx)
	return c.sha, err
}
func TestStaleInvalidationAndCurrentCandidateCheck(t *testing.T) {
	run := Run{CandidateSHA: testSHA()}
	changed := testSHA()
	changed = changed[:39] + "1"
	if got := MarkStale(run, changed).State; got != StateStale {
		t.Fatal(got)
	}
	engine := Engine{Candidate: staticCandidate(changed)}
	m := Manifest{Version: 1, Commands: map[string]CommandCheck{"x": {Command: []string{"true"}, Required: true, TimeoutSeconds: 1}}}
	// Candidate mismatch is reported stale even if a runner cannot be started.
	got, err := engine.Verify(context.Background(), t.TempDir(), "run", "workflow", "", m, Standard, testSHA(), true)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateStale {
		t.Fatal(got.State)
	}
}

func TestReviewIsAdvisoryUntilExplicitHumanDecisionAndPassNeedsRequiredCheck(t *testing.T) {
	run := Run{Checks: []CheckResult{{ID: "optional", Name: "advisory", Outcome: Passed, Required: false}}}
	if got := Evaluate(run, false); got != StateBlocked {
		t.Fatalf("optional-only checks produced %s", got)
	}
	run.Checks[0].Required = true
	if got := Evaluate(run, true); got != StateNeedsHuman {
		t.Fatalf("advisory review produced %s", got)
	}
	completed := ReviewResult{Completed: true, Advisory: "looks good"}
	run.Review = &completed
	if got := Evaluate(run, true); got != StateNeedsHuman {
		t.Fatalf("review without human decision produced %s", got)
	}
	approved := true
	completed.HumanApproved = &approved
	if got := Evaluate(run, true); got != StateNeedsHuman {
		t.Fatalf("unattributed decision produced %s", got)
	}
	completed.HumanActor = "reviewer@example.test"
	if got := Evaluate(run, true); got != StatePass {
		t.Fatalf("approved review state %s", got)
	}
}

func TestRegistryIncludesOnlyDeclaredChecks(t *testing.T) {
	manifest, err := ParseManifest([]byte("version: 1\ncommands:\n  format:\n    command: [gofmt, -d, .]\n    required: true\n    timeout_seconds: 60\n"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := manifest.Plan(testSHA(), Minimal)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Checks) != 1 {
		t.Fatalf("got %d checks", len(plan.Checks))
	}
}
