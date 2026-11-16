package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
