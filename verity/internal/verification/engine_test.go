package verification

import (
	"archive/tar"
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

func TestRootlessContainerPolicyIsExplicitAndImageDigestPinned(t *testing.T) {
	image := "registry.example/verity-checks@sha256:" + strings.Repeat("a", 64)
	manifest, err := ParseManifest([]byte("version: 1\ntrust_level: untrusted\nisolation: rootless_container_no_network\ncontainer_image: " + image + "\ncommands:\n  test:\n    command: [go, test, ./...]\n    required: true\n    timeout_seconds: 10\n"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := manifest.Plan(testSHA(), Standard)
	if err != nil || plan.TrustLevel != TrustUntrusted || plan.IsolationPolicy != IsolationRootlessContainer || plan.ContainerImage != image {
		t.Fatalf("container plan: %#v %v", plan, err)
	}
	if _, err := ParseManifest([]byte("version: 1\nisolation: rootless_container_no_network\ncontainer_image: registry.example/checks:latest\ncommands:\n  test:\n    command: [true]\n    required: true\n    timeout_seconds: 1\n")); err == nil {
		t.Fatal("mutable container image tag accepted")
	}
	if _, err := ParseManifest([]byte("version: 1\ntrust_level: untrusted\ncommands:\n  test:\n    command: [true]\n    required: true\n    timeout_seconds: 1\n")); err == nil {
		t.Fatal("untrusted checks without container isolation accepted")
	}
}

func TestNetworkAllowlistRequiresExactUntrustedDestinations(t *testing.T) {
	base := "version: 1\ntrust_level: untrusted\nisolation: rootless_container_no_network\ncontainer_image: checks@sha256:" + strings.Repeat("a", 64) + "\nnetwork_allowlist:\n  - api.example.com:443\ncommands:\n  test:\n    command: [\"true\"]\n    required: true\n    timeout_seconds: 10\n"
	manifest, err := ParseManifest([]byte(base))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := manifest.Plan(testSHA(), Standard)
	if err != nil || len(plan.NetworkAllowlist) != 1 || plan.NetworkAllowlist[0] != "api.example.com:443" {
		t.Fatalf("allowlist not bound to plan: %#v %v", plan, err)
	}
	for _, value := range []string{"127.0.0.1:443", "localhost:443", "*.example.com:443", "API.example.com:443", "api.example.com:0", "api.example.com:0443"} {
		input := strings.Replace(base, "api.example.com:443", value, 1)
		if _, err := ParseManifest([]byte(input)); err == nil {
			t.Errorf("unsafe allowlist destination accepted: %q", value)
		}
	}
	duplicate := strings.Replace(base, "  - api.example.com:443", "  - api.example.com:443\n  - api.example.com:443", 1)
	if _, err := ParseManifest([]byte(duplicate)); err == nil {
		t.Fatal("duplicate allowlist destination accepted")
	}
	trusted := strings.Replace(base, "trust_level: untrusted", "trust_level: trusted", 1)
	if _, err := ParseManifest([]byte(trusted)); err == nil {
		t.Fatal("trusted manifest accepted network allowlist")
	}
}

func TestUntrustedVerificationCannotFallBackToLocalRunner(t *testing.T) {
	manifest, err := ParseManifest([]byte("version: 1\ntrust_level: untrusted\nisolation: rootless_container_no_network\ncontainer_image: checks@sha256:" + strings.Repeat("b", 64) + "\ncommands:\n  test:\n    command: [echo, ok]\n    required: true\n    timeout_seconds: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	contentSHA := hex.EncodeToString(make([]byte, 32))
	run, err := (Engine{CandidateKind: CandidateContentSource, CandidateRepository: "test-repository"}).Verify(context.Background(), t.TempDir(), "run", "workflow", "", manifest, Standard, contentSHA, true)
	if err != nil || run.State != StateBlocked || run.TrustLevel != TrustUntrusted || run.IsolationPolicy != IsolationRootlessContainer {
		t.Fatalf("untrusted verification did not fail closed: %#v %v", run, err)
	}
	plan := Plan{CandidateSHA: testSHA(), TrustLevel: TrustUntrusted, IsolationPolicy: IsolationLocalProcess, Checks: []PlannedCheck{{ID: "test", Name: "test", Argv: []string{"true"}, Required: true, Timeout: time.Second}}}
	checks, err := (CommandRunner{}).Execute(context.Background(), t.TempDir(), plan, true)
	if err != nil || len(checks) != 1 || checks[0].Outcome != Blocked {
		t.Fatalf("untrusted local runner must block: %#v %v", checks, err)
	}
}

func TestContainerRunnerFailsClosedAndBuildsRestrictedRuntimeArgs(t *testing.T) {
	plan := Plan{CandidateSHA: testSHA(), IsolationPolicy: IsolationRootlessContainer, ContainerImage: "image@sha256:" + strings.Repeat("0", 64), Checks: []PlannedCheck{{ID: "test", Name: "test", Argv: []string{"go", "test", "./..."}, Required: true, Timeout: time.Second}}}
	results, err := (ContainerRunner{RuntimeBinary: filepath.Join(t.TempDir(), "missing-docker")}).Execute(context.Background(), t.TempDir(), plan, true)
	if err != nil || len(results) != 1 || results[0].Outcome != Blocked {
		t.Fatalf("missing runtime must block: %#v %v", results, err)
	}
	args := strings.Join(containerArgs(plan.ContainerImage, "go", []string{"test", "./..."}, "verity-check-test", ""), " ")
	for _, required := range []string{"--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges:true", "--pids-limit=128", "--memory=1g", "--cpus=2", "--pull=never", "/workspace:rw,nosuid,nodev,size=512m", "/tmp:rw,nosuid,nodev,size=128m", "--entrypoint /bin/sh", "tar -x -C /workspace", "--name verity-check-test"} {
		if !strings.Contains(args, required) {
			t.Errorf("restricted container args missing %q: %s", required, args)
		}
	}
	for _, forbidden := range []string{"--privileged", "/var/run/docker.sock", "--mount", os.Getenv("HOME") + ":"} {
		if forbidden != ":" && strings.Contains(args, forbidden) {
			t.Errorf("container args expose forbidden capability %q: %s", forbidden, args)
		}
	}
}

func TestContainerArgsKeepNetworkNoneAndScopeProxyMounts(t *testing.T) {
	args := strings.Join(containerArgsWithEgress("checks@sha256:"+strings.Repeat("a", 64), "curl", []string{"https://api.example.com"}, "verity-check", "", "/usr/bin/verity", "/tmp/egress", "VERITY_TEST_TOKEN"), " ")
	for _, required := range []string{"--network=none", "HTTP_PROXY=http://127.0.0.1:18080", "HTTPS_PROXY=http://127.0.0.1:18080", "/tmp/egress:/verity-egress:ro", "/usr/bin/verity:/verity-agent:ro", "internal egress-relay", "--env VERITY_TEST_TOKEN"} {
		if !strings.Contains(args, required) {
			t.Errorf("egress args missing %q: %s", required, args)
		}
	}
	if strings.Contains(args, "--network=bridge") {
		t.Fatal("allowlist enables unrestricted bridge networking")
	}
}

type staticSnapshotter string

func (s staticSnapshotter) Snapshot(context.Context, string, string) (string, func(), error) {
	return string(s), func() {}, nil
}

func TestContainerRunnerUsesRootlessRuntimeAndRestrictedArguments(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "args.log")
	runtimePath := filepath.Join(dir, "docker")
	quotedLog := "'" + strings.ReplaceAll(log, "'", "'\\''") + "'"
	script := "#!/bin/sh\nif [ \"$1\" = info ]; then echo '[\"name=rootless\"]'; exit 0; fi\nif [ \"$1\" = rm ]; then exit 0; fi\nprintf '%s\\n' \"$@\" > " + quotedLog + "\n"
	if err := os.WriteFile(runtimePath, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	candidateDir := filepath.Join(dir, "candidate")
	if err := os.Mkdir(candidateDir, 0700); err != nil {
		t.Fatal(err)
	}
	plan := Plan{CandidateSHA: testSHA(), IsolationPolicy: IsolationRootlessContainer, ContainerImage: "checks@sha256:" + strings.Repeat("a", 64), Checks: []PlannedCheck{{ID: "test", Name: "test", Argv: []string{"go", "test", "./..."}, Required: true, Timeout: time.Second}}}
	runner := ContainerRunner{RuntimeBinary: runtimePath, Snapshotter: staticSnapshotter(candidateDir), hostOS: "linux", hostUID: 1000}
	results, err := runner.Execute(context.Background(), dir, plan, true)
	if err != nil || len(results) != 1 || results[0].Outcome != Passed {
		t.Fatalf("rootless fake runtime result: %#v %v", results, err)
	}
	args, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"run", "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges:true", "--pids-limit=128", "--memory=1g", "--cpus=2", "--pull=never", "/workspace:rw,nosuid,nodev,size=512m", "--entrypoint", "checks@sha256:"} {
		if !strings.Contains(string(args), required) {
			t.Errorf("Docker invocation missing %q: %s", required, args)
		}
	}
	if strings.Contains(string(args), "--privileged") || strings.Contains(string(args), "/var/run/docker.sock") {
		t.Fatalf("Docker invocation contains forbidden access: %s", args)
	}
}

func TestCandidateArchivePreservesSymlinkWithoutFollowingHostTarget(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside-secret")
	if err := os.WriteFile(outside, []byte("host secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	archive, err := candidateArchive(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = archive.Close(); _ = os.Remove(archive.Name()) }()
	reader := tar.NewReader(archive)
	header, err := reader.Next()
	if err != nil {
		t.Fatal(err)
	}
	if header.Name != "link" || header.Typeflag != tar.TypeSymlink || header.Linkname != outside {
		t.Fatalf("symlink archive entry changed or was followed: %#v", header)
	}
	if _, err := reader.Next(); err == nil {
		t.Fatal("outside symlink target was included in candidate archive")
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

func TestRunnerDiscardsMutatedCandidateSnapshotBetweenChecks(t *testing.T) {
	root, sha := candidateRepo(t)
	m, err := ParseManifest([]byte(`version: 1
commands:
  a_mutate:
    command: [sh, -c, "printf changed > marker"]
    required: true
    timeout_seconds: 2
  b_verify:
    command: [grep, -qx, original, marker]
    required: true
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
	if len(results) != 2 || results[0].Outcome != Passed || results[1].Outcome != Passed {
		t.Fatalf("checks shared a mutated candidate snapshot: %#v", results)
	}
	b, err := os.ReadFile(filepath.Join(root, "marker"))
	if err != nil || string(b) != "original" {
		t.Fatalf("repository changed: %q %v", b, err)
	}
}

func TestEngineRejectsIndexMutationDuringVerification(t *testing.T) {
	repo := initIndexRepo(t)
	writeIndexFile(t, filepath.Join(repo, "tracked.txt"), "base\n")
	gitCmd(t, repo, "add", "tracked.txt")
	gitCmd(t, repo, "commit", "-m", "base")
	candidate, err := CaptureIndex(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	writeIndexFile(t, filepath.Join(repo, "late.txt"), "index mutation\n")
	manifest, err := ParseManifest([]byte("version: 1\ncommands:\n  mutate:\n    command: [git, -C, '" + repo + "', add, late.txt]\n    required: true\n    timeout_seconds: 5\n"))
	if err != nil {
		t.Fatal(err)
	}
	engine := Engine{
		Runner:    CommandRunner{Snapshotter: IndexSnapshotter{}},
		Candidate: IndexCurrent{Repository: repo, Candidate: candidate},
	}
	if _, err := engine.Verify(context.Background(), repo, "run", "workflow", "", manifest, Standard, candidate.Tree, true); !errors.Is(err, ErrCandidateChanged) {
		t.Fatalf("verification should reject changed index: %v", err)
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

func TestRunnerFailsClosedForUnsupportedIsolationPolicy(t *testing.T) {
	root, sha := candidateRepo(t)
	m, err := ParseManifest([]byte("version: 1\ncommands:\n  test:\n    command: ['true']\n    required: true\n    timeout_seconds: 2\n"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := m.Plan(sha, Standard)
	if err != nil {
		t.Fatal(err)
	}
	plan.IsolationPolicy = "container"
	if _, err := (CommandRunner{}).Execute(context.Background(), root, plan, true); !errors.Is(err, ErrUnsupportedIsolation) {
		t.Fatalf("unsupported isolation policy should block: %v", err)
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
