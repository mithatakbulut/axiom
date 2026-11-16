package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedWorkRunsAndPersistsVerification(t *testing.T) {
	root := t.TempDir()
	if _, err := invoke(t, "init", root); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, ".verity", "config.yaml")
	manifest := "version: 1\ncommands:\n  check:\n    command: ['true']\n    required: true\n    timeout_seconds: 5\n"
	if err := os.WriteFile(filepath.Join(root, ".verity", "verification.yaml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source"), []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	instruction := filepath.Join(root, "instruction.txt")
	if err := os.WriteFile(instruction, []byte("perform the fixture task"), 0600); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	script := "#!/bin/sh\ncat >/dev/null\nprintf '%s\\n' '{\"type\":\"thread.started\",\"thread_id\":\"fixture\"}' '{\"type\":\"turn.completed\"}'\n"
	if err := os.WriteFile(filepath.Join(binDir, "codex"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := invoke(t, "--config", cfg, "work", "run", instruction, "source")
	if err != nil {
		t.Fatalf("work: %s: %v", out, err)
	}
	var result struct {
		Workflow     string `json:"workflow_id"`
		Verification string `json:"verification_id"`
		State        string `json:"state"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.State != "SUCCEEDED" || result.Workflow == "" || result.Verification == "" {
		t.Fatalf("unexpected result: %s", out)
	}
	out, err = invoke(t, "--config", cfg, "proof", "show", result.Verification, "--json")
	if err != nil || !strings.Contains(out, `"PASS"`) {
		t.Fatalf("missing verification: %s: %v", out, err)
	}
}

func TestManagedWorkDoesNotSucceedWithoutCodexTerminalEvent(t *testing.T) {
	root := t.TempDir()
	if _, err := invoke(t, "init", root); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, ".verity", "config.yaml")
	manifest := "version: 1\ncommands:\n  check:\n    command: ['true']\n    required: true\n    timeout_seconds: 5\n"
	if err := os.WriteFile(filepath.Join(root, ".verity", "verification.yaml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source"), []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	instruction := filepath.Join(root, "instruction.txt")
	if err := os.WriteFile(instruction, []byte("perform the fixture task"), 0600); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "codex"), []byte("#!/bin/sh\nprintf '%s\\n' '{\"type\":\"thread.started\",\"thread_id\":\"fixture\"}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := invoke(t, "--config", cfg, "work", "run", instruction, "source"); err == nil {
		t.Fatal("missing terminal event accepted")
	}
	out, err := invoke(t, "--config", cfg, "workflow", "list", "--json")
	if err != nil || !strings.Contains(out, `"state": "PAUSED"`) {
		t.Fatalf("workflow should be paused: %s: %v", out, err)
	}
}
