package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"verity/internal/domain"
	"verity/internal/process/control"
	"verity/internal/proof"
	"verity/internal/store/sqlite"
	"verity/internal/verification"
)

// chatCommand records explicit agent actions; no state is inferred from hooks.
func chatCommand(args []string, opts options) error {
	ctx := context.Background()
	path, err := filepath.Abs(opts.configPath())
	if err != nil {
		return err
	}
	cfg, err := loadConfig(path)
	if err != nil {
		return err
	}
	root := filepath.Dir(filepath.Dir(path))
	db, err := sqlite.Open(ctx, filepath.Join(root, cfg.Database))
	if err != nil {
		return err
	}
	defer db.Close()
	if args[1] == "start" {
		if args[2] != "feature" && args[2] != "bugfix" && args[2] != "documentation" {
			return errors.New("unknown workflow template")
		}
		id, err := domain.NewID("chat")
		if err != nil {
			return err
		}
		def := control.Template(args[2], string(id))
		if err = db.SaveDefinition(ctx, def); err != nil {
			return err
		}
		run, err := control.NewRun(string(id), def, time.Now())
		if err != nil {
			return err
		}
		if err = run.Start(); err != nil {
			return err
		}
		if _, err = run.Schedule(); err != nil {
			return err
		}
		if err = db.CreateRun(ctx, run); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(run)
	}
	run, err := db.GetRun(ctx, args[2])
	if err != nil {
		return err
	}
	before := run.Transitions
	if args[1] == "verify" {
		task := ""
		for _, candidate := range run.Tasks {
			if (candidate.ID == "verify" || candidate.ID == "regression") && candidate.State == control.TaskRunning {
				task = candidate.ID
			}
		}
		if task == "" {
			return errors.New("begin the workflow verification task first")
		}
		manifest, err := verification.LoadManifest(filepath.Join(root, verification.ManifestPath))
		if err != nil {
			return err
		}
		dataDir := cfg.DataDir
		if !filepath.IsAbs(dataDir) {
			dataDir = filepath.Join(root, dataDir)
		}
		snapshots := filepath.Join(dataDir, "snapshots")
		digest, err := verification.CreateContentSnapshot(ctx, root, snapshots, args[3:])
		if err != nil {
			return err
		}
		id, err := domain.NewID("vr")
		if err != nil {
			return err
		}
		engine := verification.Engine{Runner: verification.CommandRunner{Snapshotter: verification.ContentSnapshotter{Root: snapshots}}}
		checked, err := engine.Verify(ctx, root, string(id), run.ID, task, manifest, verification.Standard, digest, true)
		if err != nil {
			return err
		}
		current, err := verification.CreateContentSnapshot(ctx, root, snapshots, args[3:])
		if err != nil {
			return err
		}
		if current != digest {
			checked = verification.MarkStale(checked, current)
		}
		checked, err = proof.AttachCheckEvidence(ctx, checked, proof.LocalArtifactStore{Root: filepath.Join(dataDir, "artifacts")}, db, time.Now().UTC())
		if err != nil {
			return err
		}
		if err = db.SaveVerification(ctx, checked); err != nil {
			return err
		}
		if err = json.NewEncoder(os.Stdout).Encode(checked); err != nil {
			return err
		}
		if checked.State != verification.StatePass {
			return fmt.Errorf("verification result: %s", checked.State)
		}
		if err = run.FinishTask(task, true); err != nil {
			return err
		}
		if run.State == control.Running {
			if _, err = run.Schedule(); err != nil {
				return err
			}
		}
		return db.UpdateRun(ctx, run, before)
	}
	if args[1] == "begin" {
		if err = run.BeginTask(args[3]); err != nil {
			return err
		}
	} else {
		if args[3] == "verify" || args[3] == "regression" {
			return errors.New("use chat verify to run fresh verification")
		}
		if len(args) == 5 {
			return errors.New("unexpected verification ID")
		}
		if err = run.FinishTask(args[3], true); err != nil {
			return err
		}
		if run.State == control.Running {
			if _, err = run.Schedule(); err != nil {
				return err
			}
		}
	}
	if err = db.UpdateRun(ctx, run, before); err != nil {
		return fmt.Errorf("save workflow transition: %w", err)
	}
	return json.NewEncoder(os.Stdout).Encode(run)
}
