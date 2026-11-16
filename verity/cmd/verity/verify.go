package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
	candidate := strings.TrimPrefix(args[3], "snapshot:")
	contentSnapshot := strings.HasPrefix(args[3], "snapshot:")
	if contentSnapshot && len(candidate) != 64 {
		return verification.ErrInvalidCandidate
	}
	plan, err := manifest.Plan(candidate, verification.Profile(args[4]))
	if err != nil {
		return err
	}
	// Bind the approval to the workflow and the complete manifest as well as
	// the executable plan, since criteria and review settings affect results.
	approval := struct {
		Workflow string                `json:"workflow"`
		Source   string                `json:"source"`
		Plan     verification.Plan     `json:"plan"`
		Manifest verification.Manifest `json:"manifest"`
	}{args[2], args[3], plan, manifest}
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
	engine := verification.Engine{}
	if contentSnapshot {
		engine.Runner.Snapshotter = verification.ContentSnapshotter{Root: snapshotRoot}
	}
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
