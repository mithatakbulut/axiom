package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"verity/internal/domain"
	"verity/internal/proof"
	"verity/internal/store/sqlite"
	"verity/internal/verification"
)

// verifyCommand requires approval of the precise serialized plan, including
// the candidate and check arguments, before running project-owned commands.
func verifyCommand(args []string, opts options) error {
	if args[1] == "index" {
		return verifyIndexCommand(args, opts)
	}
	path, err := filepath.Abs(opts.configPath())
	if err != nil {
		return err
	}
	cfg, err := loadConfig(path)
	if err != nil {
		return err
	}
	root := filepath.Dir(filepath.Dir(path))
	artifactRoot := cfg.DataDir
	if !filepath.IsAbs(artifactRoot) {
		artifactRoot = filepath.Join(root, artifactRoot)
	}
	snapshotRoot := filepath.Join(artifactRoot, "snapshots")
	if args[1] == "snapshot" {
		digest, err := verification.CreateContentSnapshot(context.Background(), root, snapshotRoot, args[2:])
		if err != nil {
			return err
		}
		fmt.Println("snapshot:" + digest)
		return nil
	}
	manifest, err := verification.LoadManifest(filepath.Join(root, verification.ManifestPath))
	if err != nil {
		return fmt.Errorf("load verification manifest: %w", err)
	}
	configDigest, err := trustedFileDigest(path)
	if err != nil {
		return err
	}
	candidate := strings.TrimPrefix(args[3], "snapshot:")
	contentSnapshot := strings.HasPrefix(args[3], "snapshot:")
	if contentSnapshot && len(candidate) != 64 {
		return verification.ErrInvalidCandidate
	}
	if !contentSnapshot {
		if err := rejectCandidateControlFiles(context.Background(), root, candidate, path); err != nil {
			return err
		}
	}
	plan, err := manifest.Plan(candidate, verification.Profile(args[4]))
	if err != nil {
		return err
	}
	// Bind the approval to the workflow and the complete manifest as well as
	// the executable plan, since criteria and review settings affect results.
	approval := struct {
		Workflow     string                     `json:"workflow"`
		Source       string                     `json:"source"`
		Kind         verification.CandidateKind `json:"candidate_kind"`
		Repo         string                     `json:"candidate_repository"`
		ConfigSHA256 string                     `json:"config_sha256"`
		Plan         verification.Plan          `json:"plan"`
		Manifest     verification.Manifest      `json:"manifest"`
	}{args[2], args[3], candidateKind(contentSnapshot), root, configDigest, plan, manifest}
	data, err := json.Marshal(approval)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	if args[1] == "plan" {
		return json.NewEncoder(os.Stdout).Encode(struct {
			Plan           verification.Plan     `json:"plan"`
			Manifest       verification.Manifest `json:"manifest"`
			ApprovalDigest string                `json:"approval_digest"`
		}{plan, manifest, digest})
	}
	if args[5] != digest {
		return errors.New("approval digest does not match the current plan")
	}
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(root, cfg.Database))
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err = db.GetRun(ctx, args[2]); err != nil {
		return fmt.Errorf("load workflow: %w", err)
	}
	id, err := domain.NewID("vr")
	if err != nil {
		return err
	}
	runner := verification.CommandRunner{}
	if contentSnapshot {
		runner.Snapshotter = verification.ContentSnapshotter{Root: snapshotRoot}
	}
	engine := verification.Engine{Runner: runner, CandidateKind: candidateKind(contentSnapshot), CandidateRepository: root}
	run, err := engine.Verify(ctx, root, string(id), args[2], "", manifest, verification.Profile(args[4]), candidate, true)
	if err != nil {
		return err
	}
	run, err = proof.AttachCheckEvidence(ctx, run, proof.LocalArtifactStore{Root: filepath.Join(artifactRoot, "artifacts")}, db, time.Now().UTC())
	if err != nil {
		return err
	}
	if err = db.SaveVerification(ctx, run); err != nil {
		return err
	}
	if err = json.NewEncoder(os.Stdout).Encode(run); err != nil {
		return err
	}
	if run.State != verification.StatePass {
		return fmt.Errorf("verification result: %s", run.State)
	}
	return nil
}

func candidateKind(contentSnapshot bool) verification.CandidateKind {
	if contentSnapshot {
		return verification.CandidateContentSource
	}
	return verification.CandidateGitCommit
}

func trustedFileDigest(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read trusted configuration: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func rejectCandidateControlFiles(ctx context.Context, repository, candidate, configPath string) error {
	prefixOutput, err := exec.CommandContext(ctx, "git", "-C", repository, "rev-parse", "--show-prefix").Output()
	if err != nil {
		return fmt.Errorf("resolve candidate repository prefix: %w", err)
	}
	prefix := strings.Trim(strings.TrimSpace(string(prefixOutput)), "/")
	projectRoot := filepath.Dir(filepath.Dir(configPath))
	controlFiles := []string{configPath, filepath.Join(projectRoot, verification.ManifestPath)}
	paths := make([]string, 0, len(controlFiles))
	for _, controlFile := range controlFiles {
		rel, err := filepath.Rel(projectRoot, controlFile)
		if err != nil {
			return err
		}
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			paths = append(paths, path.Join(prefix, filepath.ToSlash(rel)))
		}
	}
	seen := make(map[string]bool, len(paths))
	for _, name := range paths {
		if seen[name] {
			continue
		}
		seen[name] = true
		if err := verification.RejectCandidatePolicyPath(ctx, repository, candidate, name); err != nil {
			return err
		}
	}
	return nil
}

// verifyIndexCommand plans and runs verification against the complete staged
// Git tree. The plan approval binds tree, base, manifest, profile, and checks.
func verifyIndexCommand(args []string, opts options) error {
	if len(args) < 5 || (args[2] != "plan" && args[2] != "run") || (args[2] == "plan" && len(args) != 5) || (args[2] == "run" && len(args) != 6) {
		return errors.New("usage: verity verify index plan <workflow-id> <profile> | verify index run <workflow-id> <profile> <approval-digest>")
	}
	path, err := filepath.Abs(opts.configPath())
	if err != nil {
		return err
	}
	cfg, err := loadConfig(path)
	if err != nil {
		return err
	}
	root := filepath.Dir(filepath.Dir(path))
	candidate, err := verification.CaptureIndex(context.Background(), root)
	if err != nil {
		return fmt.Errorf("capture staged Git candidate: %w", err)
	}
	if err := rejectCandidateControlFiles(context.Background(), root, candidate.Tree, path); err != nil {
		return err
	}
	manifest, err := verification.LoadManifest(filepath.Join(root, verification.ManifestPath))
	if err != nil {
		return fmt.Errorf("load verification manifest: %w", err)
	}
	configDigest, err := trustedFileDigest(path)
	if err != nil {
		return err
	}
	profile := verification.Profile(args[4])
	plan, err := manifest.Plan(candidate.Tree, profile)
	if err != nil {
		return err
	}
	digest, err := indexPlanApprovalDigest(args[3], candidate, configDigest, plan, manifest)
	if err != nil {
		return err
	}
	if args[2] == "plan" {
		return json.NewEncoder(os.Stdout).Encode(struct {
			Candidate verification.IndexCandidate `json:"candidate"`
			Kind      string                      `json:"candidate_kind"`
			Plan      verification.Plan           `json:"plan"`
			Manifest  verification.Manifest       `json:"manifest"`
			Digest    string                      `json:"approval_digest"`
		}{candidate, "git_index", plan, manifest, digest})
	}
	if args[5] != digest {
		return errors.New("approval digest does not match the current staged candidate plan")
	}
	ctx := context.Background()
	dbPath := cfg.Database
	if !filepath.IsAbs(dbPath) {
		dbPath = filepath.Join(root, dbPath)
	}
	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err = db.GetRun(ctx, args[3]); err != nil {
		return fmt.Errorf("load workflow: %w", err)
	}
	id, err := domain.NewID("vr")
	if err != nil {
		return err
	}
	engine := verification.Engine{
		Runner:              verification.CommandRunner{Snapshotter: verification.IndexSnapshotter{}},
		Candidate:           verification.IndexCurrent{Repository: candidate.RepositoryRoot, Candidate: candidate},
		CandidateKind:       verification.CandidateGitIndex,
		CandidateBase:       candidate.Base,
		CandidateRepository: candidate.GitCommonDir,
	}
	run, err := engine.Verify(ctx, candidate.RepositoryRoot, string(id), args[3], "", manifest, profile, candidate.Tree, true)
	if err != nil {
		return err
	}
	artifactRoot := cfg.DataDir
	if !filepath.IsAbs(artifactRoot) {
		artifactRoot = filepath.Join(root, artifactRoot)
	}
	run, err = proof.AttachCheckEvidence(ctx, run, proof.LocalArtifactStore{Root: filepath.Join(artifactRoot, "artifacts")}, db, time.Now().UTC())
	if err != nil {
		return err
	}
	if err = db.SaveVerification(ctx, run); err != nil {
		return err
	}
	if err = json.NewEncoder(os.Stdout).Encode(run); err != nil {
		return err
	}
	if run.State != verification.StatePass {
		return fmt.Errorf("verification result: %s", run.State)
	}
	return nil
}

func indexPlanApprovalDigest(workflow string, candidate verification.IndexCandidate, configSHA256 string, plan verification.Plan, manifest verification.Manifest) (string, error) {
	approval := struct {
		Workflow     string                     `json:"workflow"`
		Kind         verification.CandidateKind `json:"candidate_kind"`
		Tree         string                     `json:"candidate_tree"`
		Base         string                     `json:"candidate_base"`
		Repo         string                     `json:"candidate_repository"`
		ConfigSHA256 string                     `json:"config_sha256"`
		Plan         verification.Plan          `json:"plan"`
		Manifest     verification.Manifest      `json:"manifest"`
	}{workflow, verification.CandidateGitIndex, candidate.Tree, candidate.Base, candidate.GitCommonDir, configSHA256, plan, manifest}
	data, err := json.Marshal(approval)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
