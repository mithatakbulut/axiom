package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChatWorkflowRequiresRealVerification(t *testing.T) {
	root := t.TempDir()
	if _, err := invoke(t, "init", root); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, ".verity", "config.yaml")
	if err := os.WriteFile(filepath.Join(root, "source"), []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := "version: 1\ncommands:\n  check:\n    command: ['true']\n    required: true\n    timeout_seconds: 5\n"
	if err := os.WriteFile(filepath.Join(root, ".verity", "verification.yaml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := invoke(t, "--config", cfg, "chat", "start", "feature")
	if err != nil {
		t.Fatal(err)
	}
	var run struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &run); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"begin", "spec"}, {"finish", "spec"}, {"begin", "implement"}, {"finish", "implement"}, {"begin", "verify"}} {
		if _, err := invoke(t, "--config", cfg, "chat", args[0], run.ID, args[1]); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if _, err := invoke(t, "--config", cfg, "chat", "finish", run.ID, "verify", "fake"); err == nil {
		t.Fatal("verification bypassed")
	}
	out, err = invoke(t, "--config", cfg, "chat", "verify", run.ID, "source")
	if err != nil || !strings.Contains(out, `"state":"PASS"`) {
		t.Fatalf("verify: %s %v", out, err)
	}
	out, err = invoke(t, "--config", cfg, "workflow", "show", run.ID, "--json")
	if err != nil || !strings.Contains(out, `"state": "WAITING_APPROVAL"`) {
		t.Fatalf("approval not preserved: %s %v", out, err)
	}
}
