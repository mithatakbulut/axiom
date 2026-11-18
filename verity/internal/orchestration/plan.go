package orchestration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	candidatepkg "verity/internal/candidate"
	"verity/internal/domain"
	"verity/internal/execution"
	codexruntime "verity/internal/execution/codex"
	vigilbridge "verity/internal/integrations/vigil"
	"verity/internal/process/control"
	"verity/internal/proof"
	"verity/internal/store/sqlite"
	"verity/internal/verification"
)

// planLock prevents two local schedulers from invoking the same task concurrently.
// Unlike a timed lease, it cannot expire while a Codex process is still running.
func Lock(root, id string) (func(), error) {
	dir := filepath.Join(root, ".verity", "plan-locks")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	key := sha256.Sum256([]byte(id))
	f, err := os.OpenFile(filepath.Join(dir, fmt.Sprintf("%x.lock", key)), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("plan is already being managed: %w", err)
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}

func Drive(ctx context.Context, root, dataDir, model string, db *sqlite.Store, id string, provider execution.AgentRuntime, output io.Writer) error {
	if output == nil {
		output = io.Discard
	}
	unlock, err := Lock(root, id)
	if err != nil {
		return err
	}
	defer unlock()
	snapshots := filepath.Join(dataDir, "snapshots")
	for {
		run, err := db.GetRun(ctx, id)
		if err != nil {
			return err
		}
		if err = run.Definition.ValidatePlan(); err != nil {
			return err
		}
		if run.State != control.Running {
			return json.NewEncoder(output).Encode(run)
		}
		manifest, err := verification.ParseManifest([]byte(run.Definition.VerificationManifest))
		if err != nil {
			return fmt.Errorf("captured manifest: %w", err)
		}
		var task *control.Task
		for i := range run.Tasks {
			if run.Tasks[i].State == control.TaskRunning {
				task = &run.Tasks[i]
				break
			}
		}
		if task == nil {
			before := run.Transitions
			taskID, err := run.ScheduleOne()
			if err != nil {
				return err
			}
			if taskID == "" {
				return errors.New("plan has no runnable task")
			}
			// Persist scheduling and invocation-start transitions separately.
			if run.State == control.Running && run.Transitions == before {
				if err = run.BeginTask(taskID); err != nil {
					return err
				}
			}
			if err = db.UpdateRun(ctx, run, before); err != nil {
				return err
			}
			if run.State == control.Waiting {
				return json.NewEncoder(output).Encode(run)
			}
			continue
		}
		candidate := ""
		candidateWorkspace, workspaceOwner := "", ""
		if task.ID == "_plan_verify" {
			candidate, err = latestPlanCandidate(ctx, db, root, snapshots, run, len(run.Tasks))
			if err != nil {
				return err
			}
		} else {
			attempt, e := db.LatestExecutionAttempt(ctx, run.ID, task.ID)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			if errors.Is(e, sql.ErrNoRows) || attempt.Request.Attempt < task.Attempts {
				base, e := latestPlanCandidate(ctx, db, root, snapshots, run, taskIndex(run.Tasks, task.ID))
				if e != nil {
					return e
				}
				aid, e := domain.NewID("attempt")
				if e != nil {
					return e
				}
				workspacePath, e := createOwnedWorkspace(ctx, snapshots, base, string(aid))
				if e != nil {
					return e
				}
				// The portable continuation contains persisted state and proof references.
				history := []any{}
				for _, previous := range run.Tasks {
					if previous.State != control.TaskCompleted {
						continue
					}
					v, e := db.TaskVerifications(ctx, run.ID, previous.ID)
					if e != nil {
						return e
					}
					var result *execution.Result
					previousAttempt, e := db.LatestExecutionAttempt(ctx, run.ID, previous.ID)
					if e != nil && !errors.Is(e, sql.ErrNoRows) {
						return e
					}
					if e == nil {
						result = previousAttempt.Result
					}
					type proofRef struct {
						ID        string             `json:"id"`
						Candidate string             `json:"candidate"`
						State     verification.State `json:"state"`
						Evidence  []string           `json:"evidence"`
					}
					refs := []proofRef{}
					for _, check := range v {
						ref := proofRef{ID: check.ID, Candidate: check.CandidateSHA, State: check.State}
						for _, item := range check.Checks {
							ref.Evidence = append(ref.Evidence, item.EvidenceIDs...)
						}
						refs = append(refs, ref)
					}
					history = append(history, struct {
						Task          control.Task      `json:"task"`
						Verifications []proofRef        `json:"verifications"`
						Result        *execution.Result `json:"result,omitempty"`
					}{previous, refs, result})
				}
				packet, e := json.Marshal(struct {
					PlanID, Goal string
					Criteria     []string
					Completed    []any
					Task         control.Task
				}{run.ID, run.Definition.Goal, run.Definition.AcceptanceCriteria, history, *task})
				if e != nil {
					return e
				}
				if len(packet) > 256*1024 {
					return errors.New("continuation packet exceeds 256 KiB")
				}
				baseIdentity := candidatepkg.Identity{SchemaVersion: 1, Kind: candidatepkg.ContentSource, ID: base, Repository: root, CapturedAt: time.Now().UTC(), Provenance: "verity-plan:immutable-prior-candidate"}
				req := execution.Request{ID: string(aid), WorkflowID: run.ID, TaskID: task.ID, Workspace: workspacePath, BaseCandidate: baseIdentity, Attempt: task.Attempts, BaseSHA: base, Timeout: time.Duration(task.TimeoutSeconds) * time.Second, Instruction: "Continue this Verity plan. Work only on the current task and preserve existing user changes. Do not initialize Git, create branches, commit or push. Verification is performed independently.\n\n" + string(packet)}
				runtime := provider
				if runtime == nil {
					binary, e := exec.LookPath("codex")
					if e != nil {
						return fmt.Errorf("Codex CLI unavailable: %w", e)
					}
					runtime = codexruntime.Runtime{Binary: binary, Model: model, SnapshotRoot: snapshots, SourcePaths: run.Definition.SourcePaths}
				}
				if _, e = execution.RunDurable(ctx, runtime, req, db); e != nil {
					return fmt.Errorf("execution requires inspection before continuing: %w", e)
				}
				continue
			}
			if attempt.State == execution.AttemptRunning {
				if e = db.MarkExecutionUnknown(ctx, attempt.Request.ID, "scheduler restarted while invocation was in flight", time.Now().UTC()); e != nil {
					return e
				}
				return fmt.Errorf("attempt %s is UNKNOWN; reconcile or abandon it before resuming", attempt.Request.ID)
			}
			if attempt.State == execution.AttemptUnknown {
				return fmt.Errorf("attempt %s is UNKNOWN; reconcile or abandon it before resuming", attempt.Request.ID)
			}
			if attempt.Result != nil && attempt.Result.Outcome == execution.Blocked {
				return fmt.Errorf("task %s is blocked: %s", task.ID, attempt.Result.Summary)
			}
			if attempt.State == execution.AttemptAbandoned || attempt.Result == nil || attempt.Result.Outcome != execution.Succeeded {
				before := run.Transitions
				if e = run.FinishTask(task.ID, false); e != nil {
					return e
				}
				if e = db.UpdateRun(ctx, run, before); e != nil {
					return e
				}
				if run.State == control.Failed {
					return fmt.Errorf("task %s exhausted its attempts", task.ID)
				}
				continue
			}
			candidate = attempt.Result.CandidateSHA
			candidateWorkspace = attempt.Request.Workspace
			workspaceOwner = attempt.Request.ID
			persistVigilCorrelation(ctx, db, run.ID, task.ID, attempt, candidate)
		}
		if task.ID != "_plan_verify" {
			current, e := workspaceCandidate(ctx, candidateWorkspace, snapshots, run.Definition.SourcePaths, candidate)
			if e != nil {
				return fmt.Errorf("capture task workspace: %w", e)
			}
			if current != candidate {
				return fmt.Errorf("task %s workspace differs from candidate %s; resolve before resuming", task.ID, candidate)
			}
		}
		checked, err := db.TaskVerifications(ctx, run.ID, task.ID)
		if err != nil {
			return err
		}
		passed := false
		for _, v := range checked {
			if v.CandidateSHA == candidate && v.State == verification.StatePass {
				if err := EvidenceValid(ctx, db, dataDir, v); err != nil {
					return fmt.Errorf("task %s evidence: %w", task.ID, err)
				}
				passed = true
			}
		}
		if !passed {
			// Limit criteria coverage to this task while retaining all required checks.
			wanted := map[string]bool{}
			for _, c := range task.AcceptanceCriteria {
				wanted[c] = true
			}
			selected := manifest.Criteria[:0]
			ids := map[string]bool{}
			for _, c := range manifest.Criteria {
				if wanted[c.Description] {
					selected = append(selected, c)
					ids[c.ID] = true
				}
			}
			manifest.Criteria = selected
			for name, c := range manifest.Commands {
				kept := []string{}
				for _, id := range c.Criteria {
					if ids[id] {
						kept = append(kept, id)
					}
				}
				c.Criteria = kept
				manifest.Commands[name] = c
			}
			if manifest.Browser != nil {
				for i := range manifest.Browser.Scenarios {
					c := &manifest.Browser.Scenarios[i]
					kept := []string{}
					for _, id := range c.Criteria {
						if ids[id] {
							kept = append(kept, id)
						}
					}
					c.Criteria = kept
				}
			}
			id, e := domain.NewID("vr")
			if e != nil {
				return e
			}
			engine := verification.Engine{Runner: verification.CommandRunner{Snapshotter: verification.ContentSnapshotter{Root: snapshots}}, CandidateKind: verification.CandidateContentSource, CandidateRepository: root}
			v, e := engine.Verify(ctx, root, string(id), run.ID, task.ID, manifest, verification.Standard, candidate, true)
			if e != nil {
				return e
			}
			if task.ID != "_plan_verify" {
				current, e := workspaceCandidate(ctx, candidateWorkspace, snapshots, run.Definition.SourcePaths, candidate)
				if e != nil {
					return e
				}
				if current != candidate {
					v = verification.MarkStale(v, current)
				}
			}
			v, e = proof.AttachCheckEvidence(ctx, v, proof.LocalArtifactStore{Root: filepath.Join(dataDir, "artifacts")}, db, time.Now().UTC())
			if e != nil {
				return e
			}
			if e = db.SaveVerification(ctx, v); e != nil {
				return e
			}
			if v.State != verification.StatePass {
				return fmt.Errorf("task %s verification %s: %s", task.ID, v.ID, v.State)
			}
			if err = EvidenceValid(ctx, db, dataDir, v); err != nil {
				return fmt.Errorf("task %s evidence: %w", task.ID, err)
			}
		}
		if task.ID == "_plan_verify" {
			for _, prior := range run.Tasks {
				if prior.ID == task.ID {
					continue
				}
				if prior.State != control.TaskCompleted {
					return fmt.Errorf("task %s is not completed", prior.ID)
				}
				if prior.Approval {
					if prior.ApprovalActor == "" || prior.ApprovalReason == "" {
						return fmt.Errorf("approval %s has no actor or reason", prior.ID)
					}
					continue
				}
				attempt, e := db.LatestExecutionAttempt(ctx, run.ID, prior.ID)
				if e != nil {
					return e
				}
				if attempt.State != execution.AttemptFinished || attempt.Result == nil || attempt.Result.Outcome != execution.Succeeded {
					return fmt.Errorf("task %s has no successful attempt", prior.ID)
				}
				vs, e := db.TaskVerifications(ctx, run.ID, prior.ID)
				if e != nil {
					return e
				}
				found := false
				for _, v := range vs {
					if v.CandidateSHA == attempt.Result.CandidateSHA && v.State == verification.StatePass {
						if e = EvidenceValid(ctx, db, dataDir, v); e != nil {
							return e
						}
						found = true
					}
				}
				if !found {
					return fmt.Errorf("task %s has no passing evidence", prior.ID)
				}
			}
		}
		// Reload before finishing: a concurrent pause must be respected.
		fresh, err := db.GetRun(ctx, run.ID)
		if err != nil {
			return err
		}
		if fresh.State != control.Running {
			return json.NewEncoder(output).Encode(fresh)
		}
		before := fresh.Transitions
		if err = fresh.FinishTask(task.ID, true); err != nil {
			return err
		}
		if err = db.UpdateRun(ctx, fresh, before); err != nil {
			return err
		}
		if task.ID != "_plan_verify" && candidateWorkspace != "" {
			if err = removeOwnedWorkspace(candidateWorkspace, workspaceOwner); err != nil {
				return err
			}
		}
	}
}

func taskIndex(tasks []control.Task, id string) int {
	for i := range tasks {
		if tasks[i].ID == id {
			return i
		}
	}
	return len(tasks)
}

func latestPlanCandidate(ctx context.Context, db *sqlite.Store, root, snapshots string, run control.Run, before int) (string, error) {
	if before > len(run.Tasks) {
		before = len(run.Tasks)
	}
	for i := before - 1; i >= 0; i-- {
		task := run.Tasks[i]
		if task.State != control.TaskCompleted || task.Approval || task.ID == "_plan_verify" {
			continue
		}
		attempt, err := db.LatestExecutionAttempt(ctx, run.ID, task.ID)
		if err != nil {
			return "", err
		}
		if attempt.State == execution.AttemptFinished && attempt.Result != nil && attempt.Result.Outcome == execution.Succeeded {
			return attempt.Result.CandidateSHA, nil
		}
	}
	return verification.CreateContentSnapshot(ctx, root, snapshots, run.Definition.SourcePaths)
}

func createOwnedWorkspace(ctx context.Context, snapshots, candidate, owner string) (string, error) {
	source, cleanup, err := (verification.ContentSnapshotter{Root: snapshots}).Snapshot(ctx, "", candidate)
	if err != nil {
		return "", err
	}
	defer cleanup()
	workspace, err := os.MkdirTemp("", "verity-plan-owned-*")
	if err != nil {
		return "", err
	}
	if err = os.Remove(workspace); err != nil {
		return "", err
	}
	if err = os.Rename(source, workspace); err != nil {
		return "", err
	}
	if err = os.WriteFile(workspace+".owner", []byte(owner+"\n"), 0600); err != nil {
		_ = os.RemoveAll(workspace)
		return "", err
	}
	return workspace, nil
}

func removeOwnedWorkspace(path, owner string) error {
	if owner == "" {
		return nil
	}
	root, err := filepath.Abs(os.TempDir())
	if err != nil {
		return err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(path, filepath.Join(root, "verity-plan-owned-")) {
		return errors.New("refusing to remove workspace outside Verity temp root")
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if markerErr := os.Remove(path + ".owner"); markerErr != nil && !errors.Is(markerErr, os.ErrNotExist) {
			return markerErr
		}
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("refusing to remove non-directory workspace")
	}
	marker, err := os.ReadFile(path + ".owner")
	if err != nil || string(marker) != owner+"\n" {
		return errors.New("workspace ownership marker is missing or changed")
	}
	if err = os.RemoveAll(path); err != nil {
		return err
	}
	return os.Remove(path + ".owner")
}

func persistVigilCorrelation(ctx context.Context, db *sqlite.Store, workflowID, taskID string, attempt execution.DurableAttempt, candidate string) {
	binary := os.Getenv("VERITY_VIGIL_BINARY")
	if binary == "" || attempt.Result == nil || attempt.Result.SessionID == "" || attempt.Request.ID == "" {
		return
	}
	args := []string{"mcp"}
	if config := os.Getenv("VERITY_VIGIL_CONFIG"); config != "" {
		args = append(args, "--config", config)
	}
	observations, err := (vigilbridge.MCPClient{Command: binary, Args: args}).CorrelateSession(ctx, attempt.Result.SessionID, attempt.Result.StartedAt, attempt.Result.FinishedAt)
	if err != nil || len(observations) != 1 {
		return
	}
	for _, link := range mustVigilLinks(ctx, db, workflowID, candidate) {
		if link.AttemptID == attempt.Request.ID {
			return
		}
	}
	observation := observations[0]
	link := vigilbridge.Link{SchemaVersion: vigilbridge.SchemaVersion, WorkflowID: workflowID, TaskID: taskID, AttemptID: attempt.Request.ID, CorrelationID: attempt.Request.ID, Candidate: attempt.Result.Candidate, BaseSHA: attempt.Request.BaseSHA, CandidateSHA: candidate, SessionID: observation.SessionID, TurnID: observation.TurnID, Status: vigilbridge.CandidateReady, VigilVersion: observation.VigilVersion, CapturedAt: observation.ObservedAt, Summary: "Provider session and hook metadata matched within the execution window; observation only."}
	if link.Validate() == nil {
		_ = db.SaveVigilLink(ctx, link)
	}
}

func mustVigilLinks(ctx context.Context, db *sqlite.Store, workflowID, candidate string) []vigilbridge.Link {
	links, _ := db.VigilLinks(ctx, workflowID, candidate)
	return links
}

func workspaceCandidate(ctx context.Context, workspace, snapshots string, paths []string, candidate string) (string, error) {
	current, err := verification.CreateContentSnapshot(ctx, workspace, snapshots, paths)
	if err == nil {
		return current, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	// A crash may occur after proof persistence and safe workspace cleanup but
	// before the workflow transition. The immutable candidate archive remains
	// authoritative and can be independently revalidated by its digest.
	materialized, cleanup, snapshotErr := (verification.ContentSnapshotter{Root: snapshots}).Snapshot(ctx, "", candidate)
	if snapshotErr != nil {
		return "", snapshotErr
	}
	cleanup()
	_ = materialized
	return candidate, nil
}

func EvidenceValid(ctx context.Context, db *sqlite.Store, dataDir string, v verification.Run) error {
	if v.State != verification.StatePass && v.State != verification.StateNeedsHuman {
		return errors.New("verification did not pass")
	}
	evidence, err := db.Evidence(ctx, v.WorkflowID, v.CandidateSHA)
	if err != nil {
		return err
	}
	byID := map[string]proof.Evidence{}
	for _, e := range evidence {
		byID[e.ID] = e
	}
	store := proof.LocalArtifactStore{Root: filepath.Join(dataDir, "artifacts")}
	for _, check := range v.Checks {
		if !check.Required {
			continue
		}
		if len(check.EvidenceIDs) == 0 {
			return fmt.Errorf("check %s has no evidence", check.ID)
		}
		for _, id := range check.EvidenceIDs {
			e, ok := byID[id]
			if !ok || e.Source != "verification:"+check.ID {
				return fmt.Errorf("check %s evidence %s is missing", check.ID, id)
			}
			content, err := store.Get(ctx, e.ContentSHA256)
			if err != nil {
				return err
			}
			if int64(len(content)) != e.Size {
				return errors.New("evidence size mismatch")
			}
		}
	}
	return nil
}
