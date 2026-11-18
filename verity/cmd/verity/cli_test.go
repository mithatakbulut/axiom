package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"verity/internal/hooks"
	githubbridge "verity/internal/integrations/github"
	"verity/internal/store/sqlite"
	"verity/internal/verification"
)

func invoke(t *testing.T, args ...string) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	err = run(args)
	w.Close()
	os.Stdout = old
	b, readErr := io.ReadAll(r)
	r.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	return string(b), err
}

type githubRetriggerRoundTrip func(*http.Request) (*http.Response, error)

func (f githubRetriggerRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestPreCommitVerificationUsesStagedTreeAndBlocksFailures(t *testing.T) {
	root := t.TempDir()
	if _, err := invoke(t, "init", root); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "init")
	gitIn(t, root, "config", "user.email", "test@example.com")
	gitIn(t, root, "config", "user.name", "Test")
	source := filepath.Join(root, "source.txt")
	if err := os.WriteFile(source, []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "add", "source.txt")
	gitIn(t, root, "commit", "-m", "base")
	if err := os.WriteFile(source, []byte("staged\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "add", "source.txt")
	if err := os.WriteFile(source, []byte("unstaged working copy\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := "version: 1\ncommands:\n  staged:\n    command: [grep, -qx, staged, source.txt]\n    required: true\n    timeout_seconds: 5\n  fail:\n    command: [grep, -q, never-present, source.txt]\n    required: true\n    timeout_seconds: 5\nprofiles:\n  pre-commit: [staged]\n"
	if err := os.WriteFile(filepath.Join(root, ".verity", "verification.yaml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, ".verity", "config.yaml")
	if _, err := invoke(t, "--config", cfg, "hooks", "run"); err != nil {
		t.Fatalf("staged candidate verification: %v", err)
	}
	candidate, err := verification.CaptureIndex(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "commit", "-m", "verified")
	if _, err := invoke(t, "--config", cfg, "hooks", "post-commit"); err != nil {
		t.Fatalf("post-commit binding: %v", err)
	}
	store, err := sqlite.Open(context.Background(), filepath.Join(root, ".verity", "verity.db"))
	if err != nil {
		t.Fatal(err)
	}
	proofRun, found, err := store.LatestPassingIndexVerification(context.Background(), candidate.GitCommonDir, candidate.Tree)
	if err != nil || !found {
		store.Close()
		t.Fatalf("passing candidate lookup: found=%t err=%v", found, err)
	}
	audit, err := store.ProofAudit(context.Background(), proofRun.WorkflowID)
	store.Close()
	if err != nil || len(audit) != 1 || audit[0].Type != "commit_tree_bound" || audit[0].Details["verification_id"] != proofRun.ID {
		t.Fatalf("commit proof binding not recorded: %#v %v", audit, err)
	}
	if err := os.WriteFile(filepath.Join(root, "another.txt"), []byte("unverified\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "add", "another.txt")
	gitIn(t, root, "commit", "-m", "unverified tree")
	if _, err := invoke(t, "--config", cfg, "hooks", "post-commit"); err != nil {
		t.Fatalf("mismatched post-commit binding should remain non-blocking: %v", err)
	}
	store, err = sqlite.Open(context.Background(), filepath.Join(root, ".verity", "verity.db"))
	if err != nil {
		t.Fatal(err)
	}
	audit, err = store.ProofAudit(context.Background(), proofRun.WorkflowID)
	store.Close()
	if err != nil || len(audit) != 1 {
		t.Fatalf("mismatched tree must not create a binding: %#v %v", audit, err)
	}
	manifest = strings.Replace(manifest, "pre-commit: [staged]", "pre-commit: [fail]", 1)
	if err := os.WriteFile(filepath.Join(root, ".verity", "verification.yaml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(t, "--config", cfg, "hooks", "run"); err == nil || !strings.Contains(err.Error(), "pre-commit verification") {
		t.Fatalf("failing profile should block: %v", err)
	}
	if err := os.WriteFile(source, []byte("staged before verification\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "add", "source.txt")
	if err := os.WriteFile(source, []byte("worktree mutation during verification\n"), 0600); err != nil {
		t.Fatal(err)
	}
	mutator := filepath.Join(root, ".verity", "mutate-index.py")
	rootJSON, _ := json.Marshal(root)
	script := "import subprocess\nsubprocess.run(['git', '-C', " + string(rootJSON) + ", 'add', 'source.txt'], check=True)\n"
	if err := os.WriteFile(mutator, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	command, _ := json.Marshal([]string{"python3", mutator})
	manifest = "version: 1\ncommands:\n  mutate:\n    command: " + string(command) + "\n    required: true\n    timeout_seconds: 5\nprofiles:\n  pre-commit: [mutate]\n"
	if err := os.WriteFile(filepath.Join(root, ".verity", "verification.yaml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(t, "--config", cfg, "hooks", "run"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "stale") {
		t.Fatalf("index mutation during pre-commit verification should be rejected as STALE: %v", err)
	}
}

func TestManagedGitHooksGateRealCommitsAndBindPassingTree(t *testing.T) {
	root := t.TempDir()
	if _, err := invoke(t, "init", root); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "init")
	gitIn(t, root, "config", "user.email", "test@example.com")
	gitIn(t, root, "config", "user.name", "Test")
	source := filepath.Join(root, "source.txt")
	if err := os.WriteFile(source, []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "add", "source.txt")
	gitIn(t, root, "commit", "-m", "base")
	manifest := "version: 1\ncommands:\n  check:\n    command: [grep, -qx, pass, source.txt]\n    required: true\n    timeout_seconds: 5\n    criteria: [passes]\ncriteria:\n  - id: passes\n    description: Passing staged content\nprofiles:\n  pre-commit: [check]\n"
	if err := os.WriteFile(filepath.Join(root, ".verity", "verification.yaml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path for CLI build")
	}
	moduleRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	binary := filepath.Join(t.TempDir(), "verity")
	build := exec.Command("go", "build", "-o", binary, "./cmd/verity")
	build.Dir = moduleRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build Verity CLI: %v\n%s", err, output)
	}
	cfg := filepath.Join(root, ".verity", "config.yaml")
	install := exec.Command(binary, "--config", cfg, "hooks", "install")
	if output, err := install.CombinedOutput(); err != nil {
		t.Fatalf("install managed Git hooks: %v\n%s", err, output)
	}

	if err := os.WriteFile(source, []byte("pass\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "add", "source.txt")
	candidate, err := verification.CaptureIndex(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	commit := exec.Command("git", "-C", root, "commit", "-m", "verified staged tree")
	if output, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("passing managed pre-commit should allow commit: %v\n%s", err, output)
	}
	store, err := sqlite.Open(context.Background(), filepath.Join(root, ".verity", "verity.db"))
	if err != nil {
		t.Fatal(err)
	}
	proofRun, found, err := store.LatestPassingIndexVerification(context.Background(), candidate.GitCommonDir, candidate.Tree)
	if err != nil || !found {
		store.Close()
		t.Fatalf("passing staged-tree proof missing: found=%t err=%v", found, err)
	}
	audit, err := store.ProofAudit(context.Background(), proofRun.WorkflowID)
	store.Close()
	if err != nil || len(audit) != 1 || audit[0].Type != "commit_tree_bound" || audit[0].Details["verification_id"] != proofRun.ID {
		t.Fatalf("real post-commit hook did not bind exact passing tree: audit=%#v err=%v", audit, err)
	}
	previousHead := gitIn(t, root, "rev-parse", "HEAD")
	if err := os.WriteFile(source, []byte("fail\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "add", "source.txt")
	failedCommit := exec.Command("git", "-C", root, "commit", "-m", "failing staged tree")
	if output, err := failedCommit.CombinedOutput(); err == nil {
		t.Fatalf("failing managed pre-commit unexpectedly allowed commit: %s", output)
	}
	if current := gitIn(t, root, "rev-parse", "HEAD"); current != previousHead {
		t.Fatalf("blocked commit changed HEAD from %s to %s", previousHead, current)
	}
}

func TestGitNoVerifyBypassDoesNotCreatePassingVerification(t *testing.T) {
	root := t.TempDir()
	if _, err := invoke(t, "init", root); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "init")
	gitIn(t, root, "config", "user.email", "test@example.com")
	gitIn(t, root, "config", "user.name", "Test")
	file := filepath.Join(root, "source.txt")
	if err := os.WriteFile(file, []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "add", "source.txt")
	gitIn(t, root, "commit", "-m", "base")
	if err := os.WriteFile(file, []byte("unverified\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "add", "source.txt")
	manifest := "version: 1\ncommands:\n  fail:\n    command: [grep, -q, never-present, source.txt]\n    required: true\n    timeout_seconds: 5\nprofiles:\n  pre-commit: [fail]\n"
	if err := os.WriteFile(filepath.Join(root, ".verity", "verification.yaml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, ".verity", "config.yaml")
	if _, err := invoke(t, "--config", cfg, "hooks", "run"); err == nil {
		t.Fatal("failing staged verification unexpectedly passed")
	}
	candidate, err := verification.CaptureIndex(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	preMarker := filepath.Join(root, "pre-commit-ran")
	postMarker := filepath.Join(root, "post-commit-ran")
	binary := filepath.Join(root, "verity-hook-stub")
	stub := "#!/bin/sh\ncase \"$2\" in\n  run) touch " + preMarker + "; exit 1 ;;\n  post-commit) touch " + postMarker + "; exit 0 ;;\n  *) exit 2 ;;\nesac\n"
	if err := os.WriteFile(binary, []byte(stub), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := hooks.Install(context.Background(), root, binary, false); err != nil {
		t.Fatal(err)
	}
	if _, err := hooks.InstallPostCommit(context.Background(), root, binary, false); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "-c", "core.hooksPath="+filepath.Join(root, ".git", "hooks"), "commit", "--no-verify", "-m", "bypassed")
	if _, err := os.Stat(preMarker); !os.IsNotExist(err) {
		t.Fatalf("Git bypass unexpectedly invoked pre-commit wrapper: stat err=%v", err)
	}
	if _, err := os.Stat(postMarker); err != nil {
		t.Fatalf("post-commit observation did not run: %v", err)
	}
	store, err := sqlite.Open(context.Background(), filepath.Join(root, ".verity", "verity.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, found, err := store.LatestPassingIndexVerification(context.Background(), candidate.GitCommonDir, candidate.Tree)
	store.Close()
	if err != nil || found {
		t.Fatalf("bypassed commit must not have passing staged verification: found=%t err=%v", found, err)
	}
}

func TestCLIProjectAndConfig(t *testing.T) {
	root := t.TempDir()
	if _, err := invoke(t, "init", root); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, ".verity", "config.yaml")
	if _, err := invoke(t, "--config", cfg, "workflow", "create", "feature", "definition", "run-1"); err != nil {
		t.Fatal(err)
	}
	out, err := invoke(t, "workflow", "list", "--config", cfg)
	if err != nil || !strings.Contains(out, "run-1") {
		t.Fatalf("list: %s, %v", out, err)
	}
	out, err = invoke(t, "workflow", "show", "run-1", "--config", cfg, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var run struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &run); err != nil || run.ID != "run-1" {
		t.Fatalf("JSON: %s, %v", out, err)
	}
	out, err = invoke(t, "task", "list", "run-1", "--config", cfg)
	if err != nil || !strings.Contains(out, "spec") {
		t.Fatalf("tasks: %s, %v", out, err)
	}
	if _, err := invoke(t, "workflow", "create", "bugfix", "definition-2", "run-2", cfg); err != nil {
		t.Fatalf("positional config: %v", err)
	}
}
func TestCLIHelpAndValidation(t *testing.T) {
	out, err := invoke(t, "workflow", "create", "--help")
	if err != nil || !strings.Contains(out, "definition-id") {
		t.Fatalf("help: %s, %v", out, err)
	}
	_, err = invoke(t, "workflow", "show")
	if err == nil || !strings.Contains(err.Error(), "Usage:") {
		t.Fatalf("expected usage error, got %v", err)
	}
	_, err = invoke(t, "--config", filepath.Join(t.TempDir(), "missing"), "doctor")
	if err == nil || !strings.Contains(err.Error(), "verity init") {
		t.Fatalf("expected init hint, got %v", err)
	}
	args, opts, err := parseOptions([]string{"vigil", "inspect", "vigil", "session", "--", "--config", "vigil.yaml"})
	if err != nil || opts.config != "" || len(args) != 6 || args[4] != "--config" {
		t.Fatalf("Vigil passthrough: %v, %+v, %v", args, opts, err)
	}
}

func TestGitHubReceiveSignedDelivery(t *testing.T) {
	root := t.TempDir()
	if _, err := invoke(t, "init", root); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, ".verity", "config.yaml")
	t.Setenv("VERITY_GITHUB_WEBHOOK_SECRET", "fixture-secret")
	body := `{"action":"opened","number":1,"repository":{"full_name":"acme/app"},"pull_request":{"head":{"sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}`
	mac := hmac.New(sha256.New, []byte("fixture-secret"))
	_, _ = mac.Write([]byte(body))
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	path := filepath.Join(root, "event.json")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	old := os.Stdin
	os.Stdin = f
	defer func() { os.Stdin = old }()
	if _, err := invoke(t, "--config", cfg, "github", "receive", "acme/app", "delivery", sig); err != nil {
		t.Fatal(err)
	}
	out, err := invoke(t, "--config", cfg, "workflow", "list", "--json")
	if err != nil || !strings.Contains(out, "github-") {
		t.Fatalf("workflow missing: %s %v", out, err)
	}
	out, err = invoke(t, "--config", cfg, "github", "workflows", "acme/app")
	if err != nil || !strings.Contains(out, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") {
		t.Fatalf("association missing: %s %v", out, err)
	}
}

func TestGitHubManualRetriggerResolvesAuthorizedLiveHead(t *testing.T) {
	root := t.TempDir()
	if _, err := invoke(t, "init", root); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("d", 40)
	oldClientFactory := newGitHubRetriggerClient
	newGitHubRetriggerClient = func(baseURL, token string) githubbridge.Client {
		if token != "fixture-token" {
			t.Errorf("token=%q", token)
		}
		return githubbridge.Client{BaseURL: baseURL, Token: token, HTTP: &http.Client{Transport: githubRetriggerRoundTrip(func(r *http.Request) (*http.Response, error) {
			var body string
			switch r.URL.Path {
			case "/user":
				body = `{"login":"maintainer"}`
			case "/repos/acme/app/collaborators/maintainer/permission":
				body = `{"permission":"write"}`
			case "/repos/acme/app/pulls/12":
				body = `{"head":{"sha":"` + sha + `"}}`
			default:
				return nil, fmt.Errorf("unexpected API path %s", r.URL.Path)
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		})}}
	}
	t.Cleanup(func() { newGitHubRetriggerClient = oldClientFactory })
	t.Setenv("VERITY_GITHUB_TOKEN", "fixture-token")
	t.Setenv("VERITY_GITHUB_API_URL", "https://api.example.test")
	cfg := filepath.Join(root, ".verity", "config.yaml")
	out, err := invoke(t, "--config", cfg, "github", "retrigger", "acme/app", "12")
	if err != nil || !strings.Contains(out, sha) || !strings.Contains(out, "maintainer") {
		t.Fatalf("manual retrigger output=%q err=%v", out, err)
	}
	store, err := sqlite.Open(context.Background(), filepath.Join(root, ".verity", "verity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	links, err := store.PullWorkflows(context.Background(), "acme/app")
	if err != nil || len(links) != 1 || !strings.HasPrefix(links[0].WorkflowID, "github-manual-") {
		t.Fatalf("manual association=%#v err=%v", links, err)
	}
	job, err := store.PullJob(context.Background(), links[0].WorkflowID)
	if err != nil || job.HeadSHA != sha || !strings.Contains(job.Payload, `"actor":"maintainer"`) {
		t.Fatalf("manual job=%#v err=%v", job, err)
	}
	if _, err = invoke(t, "--config", cfg, "github", "retrigger", "acme/app", "12"); err == nil || !strings.Contains(err.Error(), "PENDING") {
		t.Fatalf("parallel retrigger should be refused, got %v", err)
	}
}
