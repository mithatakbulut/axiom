package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDashboardSummaryCLI(t *testing.T) {
	root := t.TempDir()
	if _, err := invoke(t, "init", root); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, ".verity", "config.yaml")
	out, err := invoke(t, "dashboard", "summary", "--config", cfg, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var summary struct {
		SchemaVersion int               `json:"schema_version"`
		ProjectName   string            `json:"project_name"`
		Runs          []json.RawMessage `json:"runs"`
	}
	if err := json.Unmarshal([]byte(out), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.SchemaVersion != 1 || summary.ProjectName != filepath.Base(root) || summary.Runs == nil {
		t.Fatalf("unexpected summary: %s", out)
	}
	if err := os.Remove(filepath.Join(root, ".verity", "verity.db")); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(t, "dashboard", "summary", "--config", cfg, "--json"); err == nil || !strings.Contains(err.Error(), "dashboard summary") {
		t.Fatalf("missing DB: %v", err)
	}
}
