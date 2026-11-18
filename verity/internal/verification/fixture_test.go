package verification

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExecutionIntegrityFixtureGenerator(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is unavailable")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	script, err := filepath.Abs("../../testdata/execution-integrity/create_fixture.py")
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "fixture")
	cmd := exec.Command("python3", script, root)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate fixture: %v\n%s", err, output)
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if count := git("rev-list", "--count", "HEAD"); count != "2" {
		t.Fatalf("fixture commits=%s, want 2", count)
	}
	if staged := git("diff", "--cached", "--name-only"); !strings.Contains(staged, "staged.txt") {
		t.Fatalf("staged candidate missing staged file: %q", staged)
	}
	if unstaged := git("diff", "--name-only"); !strings.Contains(unstaged, "README.md") {
		t.Fatalf("unstaged candidate missing app modification: %q", unstaged)
	}
	if status := git("status", "--porcelain"); !strings.Contains(status, "?? untracked.txt") {
		t.Fatalf("fixture missing untracked file: %q", status)
	}
	tests := exec.Command("python3", "-m", "unittest", "discover", "-s", "tests")
	tests.Dir = root
	if output, err := tests.CombinedOutput(); err != nil {
		t.Fatalf("baseline fixture tests: %v\n%s", err, output)
	}
	failing := exec.Command("python3", "-m", "unittest", "discover", "-s", "tests")
	failing.Dir = root
	failing.Env = append(os.Environ(), "VERITY_FIXTURE_INCLUDE_FAIL=1")
	if output, err := failing.CombinedOutput(); err == nil || !strings.Contains(string(output), "deterministic fixture failure") {
		t.Fatalf("opt-in failure scenario did not fail deterministically: err=%v output=%s", err, output)
	}
	slow := exec.Command("python3", "-m", "unittest", "discover", "-s", "tests")
	slow.Dir = root
	slow.Env = append(os.Environ(), "VERITY_FIXTURE_INCLUDE_SLOW=1")
	slowStarted := time.Now()
	if output, err := slow.CombinedOutput(); err != nil {
		t.Fatalf("slow fixture scenario: %v\n%s", err, output)
	}
	if elapsed := time.Since(slowStarted); elapsed < 1500*time.Millisecond || elapsed > 15*time.Second {
		t.Fatalf("slow scenario duration %s, want bounded ~2s execution", elapsed)
	}
	artifactDir := filepath.Join(t.TempDir(), "artifacts")
	artifact := exec.Command("python3", "-m", "unittest", "discover", "-s", "tests")
	artifact.Dir = root
	artifact.Env = append(os.Environ(), "VERITY_FIXTURE_ARTIFACT_DIR="+artifactDir)
	if output, err := artifact.CombinedOutput(); err != nil {
		t.Fatalf("artifact fixture scenario: %v\n%s", err, output)
	}
	artifactBytes, err := os.ReadFile(filepath.Join(artifactDir, "fixture.txt"))
	if err != nil || string(artifactBytes) != "fixture artifact\n" || len(artifactBytes) > 1024 {
		t.Fatalf("artifact fixture output = %q, %v", artifactBytes, err)
	}
	if os.Getenv("VERITY_FIXTURE_PLAYWRIGHT") == "1" {
		playwright := os.Getenv("PLAYWRIGHT_CLI")
		if playwright == "" {
			playwright, err = exec.LookPath("playwright")
			if err != nil {
				t.Fatal("VERITY_FIXTURE_PLAYWRIGHT=1 requires PLAYWRIGHT_CLI or playwright on PATH")
			}
		}
		browser := exec.Command(playwright, "test", "playwright/smoke.spec.js")
		browser.Dir = root
		if output, err := browser.CombinedOutput(); err != nil {
			t.Fatalf("Playwright fixture smoke: %v\n%s", err, output)
		}
	}
}
