package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyApprovalBindsManifestAndWorkflow(t *testing.T) {
	root := t.TempDir()
	if _, err := invoke(t, "init", root); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, ".verity", "config.yaml")
	manifest := filepath.Join(root, ".verity", "verification.yaml")
	write := func(command string) {
		t.Helper()
		body := "version: 1\ncommands:\n  check:\n    command: ['" + command + "']\n    required: true\n    timeout_seconds: 5\n"
		if err := os.WriteFile(manifest, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("true")
	sha := strings.Repeat("a", 40)
	out, err := invoke(t, "--config", cfg, "verify", "plan", "workflow", sha, "standard")
	if err != nil {
		t.Fatal(err)
	}
	var plan struct {
		Digest string `json:"approval_digest"`
	}
	if err = json.Unmarshal([]byte(out), &plan); err != nil || len(plan.Digest) != 64 {
		t.Fatalf("invalid plan: %s (%v)", out, err)
	}
	write("false")
	_, err = invoke(t, "--config", cfg, "verify", "run", "workflow", sha, "standard", plan.Digest)
	if err == nil || !strings.Contains(err.Error(), "approval digest") {
		t.Fatalf("changed manifest should invalidate approval: %v", err)
	}
	write("true")
	_, err = invoke(t, "--config", cfg, "verify", "run", "other-workflow", sha, "standard", plan.Digest)
	if err == nil || !strings.Contains(err.Error(), "approval digest") {
		t.Fatalf("different workflow should invalidate approval: %v", err)
	}
}

func TestVerifyRunPersistsCandidateEvidence(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("candidate"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(t, "init", root); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, ".verity", "config.yaml")
	snapshot, err := invoke(t, "--config", cfg, "verify", "snapshot", "file.txt")
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.TrimSpace(snapshot)
	if _, err := invoke(t, "--config", cfg, "workflow", "create", "documentation", "def", "workflow"); err != nil {
		t.Fatal(err)
	}
	manifest := "version: 1\ncommands:\n  check:\n    command: ['true']\n    required: true\n    timeout_seconds: 5\n"
	if err := os.WriteFile(filepath.Join(root, ".verity", "verification.yaml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := invoke(t, "--config", cfg, "verify", "plan", "workflow", sha, "standard")
	if err != nil {
		t.Fatal(err)
	}
	var plan struct {
		Digest string `json:"approval_digest"`
	}
	if err := json.Unmarshal([]byte(out), &plan); err != nil {
		t.Fatal(err)
	}
	out, err = invoke(t, "--config", cfg, "verify", "run", "workflow", sha, "standard", plan.Digest)
	if err != nil {
		t.Fatalf("run: %s: %v", out, err)
	}
	var result struct {
		ID     string `json:"id"`
		State  string `json:"state"`
		Checks []struct {
			Evidence []string `json:"evidence_ids"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.State != "PASS" || len(result.Checks) != 1 || len(result.Checks[0].Evidence) != 1 {
		t.Fatalf("unexpected result: %s", out)
	}
	out, err = invoke(t, "--config", cfg, "proof", "show", result.ID, "--json")
	if err != nil || !strings.Contains(out, result.Checks[0].Evidence[0]) {
		t.Fatalf("persisted result: %s: %v", out, err)
	}
}
