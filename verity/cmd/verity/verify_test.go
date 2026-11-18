package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyIndexUsesApprovedStagedCandidate(t *testing.T) {
	root := t.TempDir()
	if _, err := invoke(t, "init", root); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")
	file := filepath.Join(root, "file.txt")
	if err := os.WriteFile(file, []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "file.txt")
	git("commit", "-m", "base")
	base := git("rev-parse", "HEAD")
	if err := os.WriteFile(file, []byte("staged\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "file.txt")
	indexPath := filepath.Join(root, ".git", "index")
	indexBefore, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("unstaged\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := "version: 1\ncommands:\n  staged:\n    command: ['grep', '-qx', 'staged', 'file.txt']\n    required: true\n    timeout_seconds: 5\n"
	if err := os.WriteFile(filepath.Join(root, ".verity", "verification.yaml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, ".verity", "config.yaml")
	if _, err := invoke(t, "--config", cfg, "workflow", "create", "documentation", "def", "workflow"); err != nil {
		t.Fatal(err)
	}
	out, err := invoke(t, "--config", cfg, "verify", "index", "plan", "workflow", "standard")
	if err != nil {
		t.Fatal(err)
	}
	var planned struct {
		Candidate struct {
			Tree         string `json:"tree"`
			Base         string `json:"base"`
			GitCommonDir string `json:"git_common_dir"`
		} `json:"candidate"`
		Kind   string `json:"candidate_kind"`
		Digest string `json:"approval_digest"`
	}
	if err := json.Unmarshal([]byte(out), &planned); err != nil {
		t.Fatal(err)
	}
	if planned.Kind != "git_index" || planned.Candidate.Base != base || planned.Digest == "" {
		t.Fatalf("unexpected staged plan: %s", out)
	}
	out, err = invoke(t, "--config", cfg, "verify", "index", "run", "workflow", "standard", planned.Digest)
	if err != nil {
		t.Fatalf("verify index run: %s: %v", out, err)
	}
	var result struct {
		ID            string `json:"id"`
		State         string `json:"state"`
		CandidateKind string `json:"candidate_kind"`
		CandidateSHA  string `json:"candidate_sha"`
		CandidateBase string `json:"candidate_base"`
		CandidateRepo string `json:"candidate_repository"`
		ManifestSHA   string `json:"manifest_sha256"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.State != "PASS" || result.CandidateKind != "git_index" || result.CandidateSHA != planned.Candidate.Tree || result.CandidateBase != base || result.CandidateRepo != planned.Candidate.GitCommonDir || len(result.ManifestSHA) != 64 {
		t.Fatalf("unexpected staged verification: %s", out)
	}
	proofPath := filepath.Join(root, "index-proof.json")
	if _, err := invoke(t, "--config", cfg, "proof", "export", result.ID, proofPath); err != nil {
		t.Fatal(err)
	}
	proofBytes, err := os.ReadFile(proofPath)
	if err != nil {
		t.Fatal(err)
	}
	secondProofPath := filepath.Join(root, "index-proof-repeat.json")
	if _, err := invoke(t, "--config", cfg, "proof", "export", result.ID, secondProofPath); err != nil {
		t.Fatal(err)
	}
	repeatedProof, err := os.ReadFile(secondProofPath)
	if err != nil || string(repeatedProof) != string(proofBytes) {
		t.Fatalf("proof export is not reproducible: %v", err)
	}
	var bundle struct {
		CandidateKind  string `json:"candidate_kind"`
		CandidateSHA   string `json:"candidate_sha"`
		CandidateBase  string `json:"candidate_base"`
		CandidateRepo  string `json:"candidate_repository"`
		ManifestSHA256 string `json:"manifest_sha256"`
		Isolation      string `json:"isolation_policy"`
		Artifacts      []struct {
			Digest string `json:"sha256"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(proofBytes, &bundle); err != nil {
		t.Fatal(err)
	}
	if bundle.CandidateKind != "git_index" || bundle.CandidateSHA != planned.Candidate.Tree || bundle.CandidateBase != base || bundle.CandidateRepo != planned.Candidate.GitCommonDir || len(bundle.ManifestSHA256) != 64 || bundle.Isolation != "local_process_reduced_env" {
		t.Fatalf("proof omitted staged candidate provenance: %s", proofBytes)
	}
	if len(bundle.Artifacts) == 0 {
		t.Fatal("exported proof has no artifacts")
	}
	artifactPath := filepath.Join(root, ".verity", "artifacts", bundle.Artifacts[0].Digest[:2], bundle.Artifacts[0].Digest[2:])
	if err := os.WriteFile(artifactPath, []byte("corrupt artifact"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(t, "--config", cfg, "proof", "export", result.ID, filepath.Join(root, "corrupt-proof.json")); err == nil || !strings.Contains(err.Error(), "verify proof artifacts") {
		t.Fatalf("proof export accepted a corrupted artifact: %v", err)
	}
	if err := os.Remove(artifactPath); err != nil {
		t.Fatal(err)
	}
	missingProofPath := filepath.Join(root, "missing-proof.json")
	if _, err := invoke(t, "--config", cfg, "proof", "export", result.ID, missingProofPath); err == nil || !strings.Contains(err.Error(), "verify proof artifacts") {
		t.Fatalf("proof export accepted a missing artifact: %v", err)
	}
	if _, err := os.Stat(missingProofPath); !os.IsNotExist(err) {
		t.Fatalf("failed proof export left an output file behind: %v", err)
	}
	indexAfter, err := os.ReadFile(indexPath)
	if err != nil || string(indexBefore) != string(indexAfter) {
		t.Fatalf("verification changed the Git index: %v", err)
	}
	worktreeBytes, err := os.ReadFile(file)
	if err != nil || string(worktreeBytes) != "unstaged\n" {
		t.Fatalf("verification changed the working tree: %q %v", worktreeBytes, err)
	}
	if err := os.WriteFile(file, []byte("changed staged tree\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "file.txt")
	_, err = invoke(t, "--config", cfg, "verify", "index", "run", "workflow", "standard", planned.Digest)
	if err == nil || !strings.Contains(err.Error(), "approval digest") {
		t.Fatalf("old approval must not authorize changed staged tree: %v", err)
	}
	git("add", "-f", ".verity/verification.yaml")
	if err := rejectCandidateControlFiles(context.Background(), root, git("write-tree"), cfg); err == nil {
		t.Fatal("direct candidate control check did not reject staged policy")
	}
	out, err = invoke(t, "--config", cfg, "verify", "index", "plan", "workflow", "standard")
	if err == nil || !strings.Contains(err.Error(), "verification policy") {
		t.Fatalf("candidate-controlled policy should be rejected: %v; staged=%q tree=%q output=%s", err, git("ls-files", "-s", ".verity/verification.yaml"), git("ls-tree", "-r", git("write-tree"), "--", ":(literal).verity/verification.yaml"), out)
	}
}

func TestVerifyApprovalBindsManifestAndWorkflow(t *testing.T) {
	root := t.TempDir()
	if _, err := invoke(t, "init", root); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(root, "source.txt"), []byte("candidate\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "source.txt")
	git("commit", "-m", "candidate")
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
	sha := git("rev-parse", "HEAD")
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
	out, err = invoke(t, "--config", cfg, "verify", "plan", "workflow", sha, "standard")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &plan); err != nil {
		t.Fatal(err)
	}
	configBytes, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, append(configBytes, []byte("# changed trusted config bytes\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = invoke(t, "--config", cfg, "verify", "run", "workflow", sha, "standard", plan.Digest)
	if err == nil || !strings.Contains(err.Error(), "approval digest") {
		t.Fatalf("changed trusted config should invalidate approval: %v", err)
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
	proofPath := filepath.Join(root, "proof.json")
	if _, err = invoke(t, "--config", cfg, "proof", "export", result.ID, proofPath); err != nil {
		t.Fatal(err)
	}
	proofBytes, err := os.ReadFile(proofPath)
	if err != nil {
		t.Fatal(err)
	}
	var bundle struct {
		CandidateKind       string `json:"candidate_kind"`
		CandidateRepository string `json:"candidate_repository"`
		ManifestSHA256      string `json:"manifest_sha256"`
	}
	if err = json.Unmarshal(proofBytes, &bundle); err != nil {
		t.Fatal(err)
	}
	if bundle.CandidateKind != "content_snapshot" || bundle.CandidateRepository == "" || len(bundle.ManifestSHA256) != 64 {
		t.Fatalf("proof omitted typed candidate provenance: %s", proofBytes)
	}
}
