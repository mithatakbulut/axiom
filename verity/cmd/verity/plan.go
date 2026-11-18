package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"verity/internal/domain"
	"verity/internal/execution"
	"verity/internal/orchestration"
	"verity/internal/process/control"
	"verity/internal/store/sqlite"
	"verity/internal/verification"
)

func planCommand(args []string, opts options) error {
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
	db, err := sqlite.Open(ctx, filepath.Join(root, cfg.Database))
	if err != nil {
		return err
	}
	defer db.Close()
	if args[1] != "create" && args[1] != "show" && args[1] != "history" {
		return planAction(ctx, root, cfg.DataDir, cfg.CodexModel, db, args)
	}
	if args[1] == "show" || args[1] == "history" {
		run, err := db.GetRun(ctx, args[2])
		if err != nil {
			return err
		}
		if err = run.Definition.ValidatePlan(); err != nil {
			return fmt.Errorf("not a plan: %w", err)
		}
		if args[1] == "history" {
			history, e := db.RunHistory(ctx, run.ID)
			if e != nil {
				return e
			}
			return json.NewEncoder(os.Stdout).Encode(history)
		}
		type detail struct {
			Task          string                     `json:"task"`
			Attempts      []execution.DurableAttempt `json:"attempts,omitempty"`
			Verifications []verification.Run         `json:"verifications,omitempty"`
		}
		var details []detail
		for _, task := range run.Tasks {
			a, e := db.TaskExecutionAttempts(ctx, run.ID, task.ID)
			if e != nil {
				return e
			}
			v, e := db.TaskVerifications(ctx, run.ID, task.ID)
			if e != nil {
				return e
			}
			d := detail{Task: task.ID, Verifications: v, Attempts: a}
			details = append(details, d)
		}
		return json.NewEncoder(os.Stdout).Encode(struct {
			Run     control.Run `json:"run"`
			Details []detail    `json:"details"`
		}{run, details})
	}
	f, err := os.Open(args[2])
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return errors.New("plan exceeds 1 MiB")
	}
	var d control.Definition
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&d); err != nil {
		return fmt.Errorf("decode plan: %w", err)
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		return errors.New("plan must contain exactly one JSON object")
	}
	if d.ID != "" {
		return errors.New("plan ID is assigned by Verity")
	}
	if d.VerificationManifest != "" {
		return errors.New("verification manifest is captured from the project")
	}
	for _, task := range d.Tasks {
		if task.State != "" || task.Attempts != 0 || task.ApprovalActor != "" || task.ApprovalReason != "" {
			return errors.New("plan cannot supply task state or attempts")
		}
	}
	id, err := domain.NewID("plan")
	if err != nil {
		return err
	}
	d.ID = string(id)
	if err = d.ValidatePlan(); err != nil {
		return err
	}
	manifest, err := os.ReadFile(filepath.Join(root, verification.ManifestPath))
	if err != nil {
		return err
	}
	m, err := verification.ParseManifest(manifest)
	if err != nil {
		return err
	}
	criteria := map[string]bool{}
	for _, c := range m.Criteria {
		criteria[c.Description] = true
	}
	for _, c := range d.AcceptanceCriteria {
		if !criteria[c] {
			return fmt.Errorf("plan criterion %q is absent from the verification manifest", c)
		}
	}
	deps := make([]string, 0, len(d.Tasks))
	for _, task := range d.Tasks {
		if task.ID == "_plan_verify" {
			return errors.New("reserved task ID: _plan_verify")
		}
		for _, c := range task.AcceptanceCriteria {
			if !task.Approval && !criteria[c] {
				return fmt.Errorf("task %s criterion %q is absent from the verification manifest", task.ID, c)
			}
		}
		deps = append(deps, task.ID)
	}
	d.VerificationManifest = string(manifest)
	d.Tasks = append(d.Tasks, control.Task{ID: "_plan_verify", Title: "Overall verification", Description: "Verify the complete plan", AcceptanceCriteria: d.AcceptanceCriteria, SourcePaths: d.SourcePaths, DependsOn: deps, MaxAttempts: 1, TimeoutSeconds: 3600})
	d.MaxTasks++
	run, err := control.NewRun(d.ID, d, time.Now())
	if err != nil {
		return err
	}
	if err = db.CreatePlan(ctx, d, run); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(run)
}

func planAction(ctx context.Context, root, dataDir, model string, db *sqlite.Store, args []string) error {
	if !filepath.IsAbs(dataDir) {
		dataDir = filepath.Join(root, dataDir)
	}
	run, err := db.GetRun(ctx, args[2])
	if err != nil {
		return err
	}
	if err = run.Definition.ValidatePlan(); err != nil {
		return err
	}
	if run.Definition.VerificationManifest == "" {
		return errors.New("plan has no captured verification manifest")
	}
	switch args[1] {
	case "start", "resume":
		if args[1] == "start" && run.State != control.Ready {
			return errors.New("plan is not ready; use resume")
		}
		if args[1] == "resume" && run.State != control.Paused && run.State != control.Running {
			return errors.New("plan is not resumable")
		}
		if run.State != control.Running {
			before := run.Transitions
			if err = run.Start(); err != nil {
				return err
			}
			if err = db.UpdateRun(ctx, run, before); err != nil {
				return err
			}
		}
		return planDrive(ctx, root, dataDir, model, db, run.ID, nil)
	case "pause":
		before := run.Transitions
		if err = run.Pause(); err != nil {
			return err
		}
		if err = db.UpdateRun(ctx, run, before); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(run)
	case "approve", "reject":
		if strings.TrimSpace(args[4]) == "" || strings.TrimSpace(args[5]) == "" {
			return errors.New("actor and reason are required")
		}
		unlock, e := orchestration.Lock(root, run.ID)
		if e != nil {
			return e
		}
		defer unlock()
		run, err = db.GetRun(ctx, run.ID)
		if err != nil {
			return err
		}
		before := run.Transitions
		if err = run.Approve(args[3], args[1] == "approve"); err != nil {
			return err
		}
		for i := range run.Tasks {
			if run.Tasks[i].ID == args[3] {
				run.Tasks[i].ApprovalActor = args[4]
				run.Tasks[i].ApprovalReason = args[5]
			}
		}
		if err = db.UpdateRun(ctx, run, before); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(run)
	case "abandon", "reconcile":
		unlock, e := orchestration.Lock(root, run.ID)
		if e != nil {
			return e
		}
		defer unlock()
		a, e := db.LatestExecutionAttempt(ctx, run.ID, args[3])
		if e != nil {
			return e
		}
		if args[1] == "abandon" {
			return db.AbandonExecutionAttempt(ctx, a.Request.ID, args[4], args[5], time.Now().UTC())
		}
		r := execution.Reconciliation{Outcome: execution.Outcome(args[4]), CandidateSHA: args[5], Actor: args[6], Reason: args[7], At: time.Now().UTC()}
		return db.ReconcileExecutionAttempt(ctx, a.Request.ID, r)
	case "retry":
		unlock, e := orchestration.Lock(root, run.ID)
		if e != nil {
			return e
		}
		defer unlock()
		run, err = db.GetRun(ctx, run.ID)
		if err != nil {
			return err
		}
		if args[3] == "_plan_verify" {
			return errors.New("resume to rerun overall verification")
		}
		a, e := db.LatestExecutionAttempt(ctx, run.ID, args[3])
		if e != nil {
			return e
		}
		if a.State != execution.AttemptFinished && a.State != execution.AttemptAbandoned {
			return errors.New("resolve the attempt before retrying")
		}
		before := run.Transitions
		if err = run.FinishTask(args[3], false); err != nil {
			return err
		}
		if err = db.UpdateRun(ctx, run, before); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(run)
	case "review":
		if strings.TrimSpace(args[4]) == "" || strings.TrimSpace(args[5]) == "" {
			return errors.New("actor and reason are required")
		}
		unlock, e := orchestration.Lock(root, run.ID)
		if e != nil {
			return e
		}
		defer unlock()
		run, err = db.GetRun(ctx, run.ID)
		if err != nil {
			return err
		}
		v, e := db.Verification(ctx, args[3])
		if e != nil {
			return e
		}
		if v.WorkflowID != run.ID || v.State != verification.StateNeedsHuman || verification.Evaluate(v, false) != verification.StatePass {
			return errors.New("verification is not eligible for human review")
		}
		var paths []string
		for _, t := range run.Tasks {
			if t.ID == v.TaskID && t.State == control.TaskRunning {
				paths = t.SourcePaths
			}
		}
		if len(paths) == 0 {
			return errors.New("verification does not belong to the running task")
		}
		candidate, e := verification.CreateContentSnapshot(ctx, root, filepath.Join(dataDir, "snapshots"), paths)
		if e != nil {
			return e
		}
		if candidate != v.CandidateSHA {
			return errors.New("verification candidate is stale")
		}
		if e = planEvidence(ctx, db, dataDir, v); e != nil {
			return e
		}
		id, e := domain.NewID("vr")
		if e != nil {
			return e
		}
		approved := true
		v.ID = string(id)
		v.Review = &verification.ReviewResult{Completed: true, HumanApproved: &approved, HumanActor: args[4], Advisory: args[5]}
		v.State = verification.Evaluate(v, true)
		if v.State != verification.StatePass {
			return errors.New("review cannot approve failing verification")
		}
		if e = db.SaveVerification(ctx, v); e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(v)
	default:
		return errors.New("unknown plan action")
	}
}
