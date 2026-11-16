package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"verity/internal/domain"
	"verity/internal/execution"
	codexruntime "verity/internal/execution/codex"
	"verity/internal/process/control"
	"verity/internal/proof"
	"verity/internal/store/sqlite"
	"verity/internal/verification"
)

func workCommand(args []string, opts options) error {
	ctx := context.Background()
	configPath, err := filepath.Abs(opts.configPath())
	if err != nil {
		return err
	}
	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}
	root := filepath.Dir(filepath.Dir(configPath))
	instruction, err := os.ReadFile(args[2])
	if err != nil {
		return err
	}
	if len(instruction) == 0 || len(instruction) > 64*1024 || strings.TrimSpace(string(instruction)) == "" {
		return errors.New("instruction must contain between 1 and 65536 bytes")
	}
	manifest, err := verification.LoadManifest(filepath.Join(root, verification.ManifestPath))
	if err != nil {
		return err
	}
	binary, err := exec.LookPath("codex")
	if err != nil {
		return fmt.Errorf("Codex CLI unavailable: %w", err)
	}
	dataDir := cfg.DataDir
	if !filepath.IsAbs(dataDir) {
		dataDir = filepath.Join(root, dataDir)
	}
	snapshotRoot := filepath.Join(dataDir, "snapshots")
	base, err := verification.CreateContentSnapshot(ctx, root, snapshotRoot, args[3:])
	if err != nil {
		return err
	}
	if _, err := manifest.Plan(base, verification.Standard); err != nil {
		return err
	}
	db, err := sqlite.Open(ctx, filepath.Join(root, cfg.Database))
	if err != nil {
		return err
	}
	defer db.Close()
	id, err := domain.NewID("work")
	if err != nil {
		return err
	}
	def := control.Definition{SchemaVersion: 1, ID: string(id), Name: "Codex work", Version: 1, MaxTasks: 2, MaxAttempts: 1, MaxTransitions: 32, Tasks: []control.Task{
		{ID: "implement", Title: "Codex implementation", MaxAttempts: 1},
		{ID: "verify", Title: "Independent verification", DependsOn: []string{"implement"}, MaxAttempts: 1},
	}}
	if err = db.SaveDefinition(ctx, def); err != nil {
		return err
	}
	run, err := control.NewRun(string(id), def, time.Now())
	if err != nil {
		return err
	}
	if err = db.CreateRun(ctx, run); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Verity workflow: %s\n", run.ID)
	update := func(action func() error) error {
		before := run.Transitions
		if err := action(); err != nil {
			return err
		}
		return db.UpdateRun(ctx, run, before)
	}
	if err = update(run.Start); err != nil {
		return err
	}
	if err = update(func() error { _, e := run.Schedule(); return e }); err != nil {
		return err
	}
	if err = update(func() error { return run.BeginTask("implement") }); err != nil {
		return err
	}
	attemptID, err := domain.NewID("attempt")
	if err != nil {
		return err
	}
	request := execution.Request{ID: string(attemptID), WorkflowID: run.ID, TaskID: "implement", Workspace: root, Attempt: 1, BaseSHA: base, Timeout: 30 * time.Minute, Instruction: string(instruction) + "\n\nPreserve existing user changes. Do not initialize Git, create branches, commit, or push."}
	runtime := codexruntime.Runtime{Binary: binary, Model: cfg.CodexModel, SnapshotRoot: snapshotRoot, SourcePaths: args[3:]}
	result, err := execution.RunDurable(ctx, runtime, request, db)
	if err != nil || result.Outcome != execution.Succeeded {
		pauseErr := update(run.Pause)
		if err != nil {
			return errors.Join(err, pauseErr)
		}
		return errors.Join(fmt.Errorf("Codex outcome %s; workflow paused: %s", result.Outcome, result.Summary), pauseErr)
	}
	if err = update(func() error { return run.FinishTask("implement", true) }); err != nil {
		return err
	}
	if err = update(func() error { _, e := run.Schedule(); return e }); err != nil {
		return err
	}
	if err = update(func() error { return run.BeginTask("verify") }); err != nil {
		return err
	}
	verificationID, err := domain.NewID("vr")
	if err != nil {
		return err
	}
	engine := verification.Engine{Runner: verification.CommandRunner{Snapshotter: verification.ContentSnapshotter{Root: snapshotRoot}}}
	checked, err := engine.Verify(ctx, root, string(verificationID), run.ID, "verify", manifest, verification.Standard, result.CandidateSHA, true)
	if err != nil {
		return errors.Join(err, update(run.Pause))
	}
	current, err := verification.CreateContentSnapshot(ctx, root, snapshotRoot, args[3:])
	if err != nil {
		return errors.Join(err, update(run.Pause))
	}
	if current != result.CandidateSHA {
		checked = verification.MarkStale(checked, current)
	}
	checked, err = proof.AttachCheckEvidence(ctx, checked, proof.LocalArtifactStore{Root: filepath.Join(dataDir, "artifacts")}, db, time.Now().UTC())
	if err != nil {
		return errors.Join(err, update(run.Pause))
	}
	if err = db.SaveVerification(ctx, checked); err != nil {
		return errors.Join(err, update(run.Pause))
	}
	if checked.State != verification.StatePass {
		if err = update(run.Pause); err != nil {
			return err
		}
		return fmt.Errorf("verification result %s; workflow paused; verification %s", checked.State, checked.ID)
	}
	if err = update(func() error { return run.FinishTask("verify", true) }); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		Workflow     string                `json:"workflow_id"`
		Attempt      string                `json:"attempt_id"`
		Verification string                `json:"verification_id"`
		Candidate    string                `json:"candidate_sha256"`
		State        control.WorkflowState `json:"state"`
	}{run.ID, string(attemptID), checked.ID, result.CandidateSHA, run.State})
}
