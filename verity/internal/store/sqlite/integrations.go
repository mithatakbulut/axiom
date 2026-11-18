package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"

	"errors"
	"fmt"
	"strings"
	"time"
	"verity/internal/process/control"
)

// ClaimWebhookDelivery atomically records a provider delivery. It returns
// false for an already-seen delivery so retries cannot start duplicate runs.
func (s *Store) ClaimWebhookDelivery(ctx context.Context, id, event string) (bool, error) {
	if id == "" || event == "" {
		return false, errors.New("delivery id and event type are required")
	}
	res, err := s.db.ExecContext(ctx, "INSERT OR IGNORE INTO github_webhook_events(delivery_id,event_type,received_at) VALUES(?,?,?)", id, event, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (s *Store) WebhookDeliveryExists(ctx context.Context, id string) (bool, error) {
	var found int
	err := s.db.QueryRowContext(ctx, "SELECT 1 FROM github_webhook_events WHERE delivery_id=?", id).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func validPullSHA(sha string) bool {
	if len(sha) != 40 && len(sha) != 64 {
		return false
	}
	for _, c := range sha {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// ReleaseWebhookDelivery allows GitHub to retry when downstream processing fails.
func (s *Store) ReleaseWebhookDelivery(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM github_webhook_events WHERE delivery_id=?", id)
	return err
}

// AssociatePullWorkflow reuses one workflow association for a pull request at
// an exact head SHA. A new SHA gets a distinct association, leaving stale
// candidate results tied to their original revision.
func (s *Store) AssociatePullWorkflow(ctx context.Context, repository string, number int, sha, workflowID string) (string, error) {
	if repository == "" || number <= 0 || len(sha) != 40 && len(sha) != 64 || workflowID == "" {
		return "", errors.New("repository, pull number, full SHA, and workflow id are required")
	}
	for _, r := range sha {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return "", errors.New("invalid candidate SHA")
		}
	}
	_, err := s.db.ExecContext(ctx, "INSERT OR IGNORE INTO github_pull_workflows(repository,pull_number,head_sha,workflow_id,updated_at) VALUES(?,?,?,?,?)", repository, number, sha, workflowID, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return "", err
	}
	var existing string
	err = s.db.QueryRowContext(ctx, "SELECT workflow_id FROM github_pull_workflows WHERE repository=? AND pull_number=? AND head_sha=?", repository, number, sha).Scan(&existing)
	return existing, err
}

// CreatePullWorkflowForDelivery commits the delivery, exact-revision association,
// and workflow together so a crash cannot acknowledge a missing workflow.
func (s *Store) CreatePullWorkflowForDelivery(ctx context.Context, delivery, event, repository string, number int, sha string, run control.Run) error {
	return s.createPullWorkflowForDelivery(ctx, delivery, event, repository, number, sha, "", run)
}

// CreatePullWorkflowJobForDelivery atomically records the delivery, candidate
// workflow, and durable worker job.
func (s *Store) CreatePullWorkflowJobForDelivery(ctx context.Context, delivery, event, repository string, number int, sha, payload string, run control.Run) error {
	if payload == "" {
		return errors.New("normalized pull request event payload is required")
	}
	return s.createPullWorkflowForDelivery(ctx, delivery, event, repository, number, sha, payload, run)
}

// CreateManualPullWorkflowJob records an authorized maintainer retrigger as a
// fresh exact-SHA run. It refuses to create parallel work while the current
// association still has a pending or leased job.
func (s *Store) CreateManualPullWorkflowJob(ctx context.Context, delivery, actor, repository string, number int, sha, payload string, run control.Run) error {
	if delivery == "" || strings.TrimSpace(actor) == "" || repository == "" || number <= 0 || !validPullSHA(sha) || payload == "" || run.ID == "" {
		return errors.New("manual pull retrigger requires delivery, actor, repository, pull number, SHA, payload, and workflow")
	}
	if err := run.Definition.Validate(); err != nil {
		return err
	}
	runBody, err := json.Marshal(run)
	if err != nil {
		return err
	}
	definition, err := json.Marshal(run.Definition)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO github_webhook_events(delivery_id,event_type,received_at) VALUES(?,'manual_retrigger',?)", delivery, stamp)
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if inserted == 0 {
		var priorID, priorRepo, priorSHA, priorPayload string
		var priorNumber int
		if err = tx.QueryRowContext(ctx, `SELECT workflow_id,repository,pull_number,head_sha,payload FROM github_pull_jobs WHERE delivery_id=?`, delivery).Scan(&priorID, &priorRepo, &priorNumber, &priorSHA, &priorPayload); err != nil {
			return fmt.Errorf("manual delivery %s was already recorded without a matching pull job: %w", delivery, err)
		}
		if priorID != run.ID || priorRepo != repository || priorNumber != number || priorSHA != sha || priorPayload != payload {
			return errors.New("manual delivery ID was reused with different trigger facts")
		}
		return tx.Commit()
	}
	var prior string
	err = tx.QueryRowContext(ctx, "SELECT workflow_id FROM github_pull_workflows WHERE repository=? AND pull_number=? AND head_sha=?", repository, number, sha).Scan(&prior)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		var state string
		if err = tx.QueryRowContext(ctx, "SELECT state FROM github_pull_jobs WHERE workflow_id=?", prior).Scan(&state); err != nil {
			return err
		}
		switch state {
		case "COMPLETED", "FAILED", "CANCELLED", "SUPERSEDED":
		default:
			return fmt.Errorf("cannot manually retrigger while existing pull job is %s", state)
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO process_definitions(id,version,body) VALUES(?,?,?)", run.DefinitionID, run.DefinitionVersion, string(definition)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO process_runs(id,definition_id,definition_version,body) VALUES(?,?,?,?)", run.ID, run.DefinitionID, run.DefinitionVersion, string(runBody)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO github_pull_workflows(repository,pull_number,head_sha,workflow_id,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(repository,pull_number,head_sha) DO UPDATE SET workflow_id=excluded.workflow_id,updated_at=excluded.updated_at`, repository, number, sha, run.ID, stamp); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO github_manual_triggers(delivery_id,actor,repository,pull_number,head_sha,workflow_id,created_at) VALUES(?,?,?,?,?,?,?)`, delivery, strings.TrimSpace(actor), repository, number, sha, run.ID, stamp); err != nil {
		return err
	}
	if err = insertPullJob(ctx, tx, run.ID, repository, number, sha, delivery, payload); err != nil {
		return err
	}
	return tx.Commit()
}

// ClosePullWorkflowForDelivery records a PR close event and cancels queued or
// leased work for that PR in the same transaction.
func (s *Store) ClosePullWorkflowForDelivery(ctx context.Context, delivery, repository string, number int) error {
	if delivery == "" || repository == "" || number <= 0 {
		return errors.New("invalid pull request close event")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO github_webhook_events(delivery_id,event_type,received_at) VALUES(?,'pull_request',?)`, delivery, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return tx.Commit()
	}
	_, err = tx.ExecContext(ctx, `UPDATE github_pull_jobs SET state='CANCELLED',updated_at=?,last_error='pull request closed' WHERE repository=? AND pull_number=? AND state IN ('PENDING','RUNNING')`, time.Now().UTC().Format(time.RFC3339Nano), repository, number)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) createPullWorkflowForDelivery(ctx context.Context, delivery, event, repository string, number int, sha, payload string, run control.Run) error {
	if delivery == "" || event != "pull_request" || repository == "" || number <= 0 || run.ID == "" || len(sha) != 40 && len(sha) != 64 {
		return errors.New("invalid pull workflow")
	}
	for _, c := range sha {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return errors.New("invalid candidate SHA")
		}
	}
	if err := run.Definition.Validate(); err != nil {
		return err
	}
	body, err := json.Marshal(run)
	if err != nil {
		return err
	}
	definition, err := json.Marshal(run.Definition)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO github_webhook_events(delivery_id,event_type,received_at) VALUES(?,?,?)", delivery, event, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	var existing string
	err = tx.QueryRowContext(ctx, "SELECT workflow_id FROM github_pull_workflows WHERE repository=? AND pull_number=? AND head_sha=?", repository, number, sha).Scan(&existing)
	if err == nil {
		var count int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM process_runs WHERE id=?", existing).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return errors.New("existing pull workflow association has no run")
		}
		if n == 1 && payload != "" {
			if err = insertPullJob(ctx, tx, existing, repository, number, sha, delivery, payload); err != nil {
				return err
			}
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if n == 0 {
		return errors.New("delivery already recorded without this pull workflow; operator reconciliation required")
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO process_definitions(id,version,body) VALUES(?,?,?)", run.DefinitionID, run.DefinitionVersion, string(definition)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO process_runs(id,definition_id,definition_version,body) VALUES(?,?,?,?)", run.ID, run.DefinitionID, run.DefinitionVersion, string(body)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO github_pull_workflows(repository,pull_number,head_sha,workflow_id,updated_at) VALUES(?,?,?,?,?)", repository, number, sha, run.ID, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if payload != "" {
		if err = insertPullJob(ctx, tx, run.ID, repository, number, sha, delivery, payload); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func insertPullJob(ctx context.Context, tx *sql.Tx, workflowID, repository string, number int, sha, delivery, payload string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `UPDATE github_pull_jobs SET state='SUPERSEDED',updated_at=? WHERE repository=? AND pull_number=? AND head_sha<>? AND state IN ('PENDING','RUNNING')`, now, repository, number, sha); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO github_pull_jobs(workflow_id,repository,pull_number,head_sha,delivery_id,payload,state,attempts,started_at,next_attempt,last_error,created_at,updated_at) VALUES(?,?,?,?,?,?,'PENDING',0,NULL,?,'',?,?)`, workflowID, repository, number, sha, delivery, payload, now, now, now)
	return err
}

type PullJob struct {
	WorkflowID  string    `json:"workflow_id"`
	Repository  string    `json:"repository"`
	Number      int       `json:"pull_number"`
	HeadSHA     string    `json:"head_sha"`
	DeliveryID  string    `json:"delivery_id"`
	Payload     string    `json:"payload"`
	State       string    `json:"state"`
	Attempts    int       `json:"attempts"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	NextAttempt time.Time `json:"next_attempt"`
	LastError   string    `json:"last_error,omitempty"`
}

var ErrNoPullJob = errors.New("no GitHub pull request job is ready")

// ClaimNextPullJob leases the oldest eligible job. Expired RUNNING leases are
// reclaimed, while attempts remain bounded and visible.
func (s *Store) ClaimNextPullJob(ctx context.Context, now time.Time, lease time.Duration, maxAttempts int) (PullJob, error) {
	return s.claimNextPullJob(ctx, now, lease, maxAttempts, "")
}

// ClaimNextPullJobForRepository scopes a worker to one explicitly configured repository.
func (s *Store) ClaimNextPullJobForRepository(ctx context.Context, now time.Time, lease time.Duration, maxAttempts int, repository string) (PullJob, error) {
	if repository == "" {
		return PullJob{}, errors.New("repository scope is required")
	}
	return s.claimNextPullJob(ctx, now, lease, maxAttempts, repository)
}

func (s *Store) claimNextPullJob(ctx context.Context, now time.Time, lease time.Duration, maxAttempts int, repository string) (PullJob, error) {
	if lease <= 0 || maxAttempts < 1 {
		return PullJob{}, errors.New("pull job lease and attempt limit are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PullJob{}, err
	}
	defer tx.Rollback()
	now = now.UTC()
	stamp := now.Format(time.RFC3339Nano)
	expired := now.Add(-lease).Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `UPDATE github_pull_jobs SET state=CASE WHEN attempts>=? THEN 'FAILED' ELSE 'PENDING' END,started_at=NULL,next_attempt=?,last_error='worker lease expired',updated_at=? WHERE state='RUNNING' AND started_at<=?`, maxAttempts, stamp, stamp, expired); err != nil {
		return PullJob{}, err
	}
	var job PullJob
	var started, next string
	query := `SELECT workflow_id,repository,pull_number,head_sha,delivery_id,payload,state,attempts,COALESCE(started_at,''),next_attempt,last_error FROM github_pull_jobs WHERE state='PENDING' AND next_attempt<=?`
	args := []any{stamp}
	if repository != "" {
		query += ` AND repository=?`
		args = append(args, repository)
	}
	query += ` ORDER BY created_at LIMIT 1`
	err = tx.QueryRowContext(ctx, query, args...).Scan(&job.WorkflowID, &job.Repository, &job.Number, &job.HeadSHA, &job.DeliveryID, &job.Payload, &job.State, &job.Attempts, &started, &next, &job.LastError)
	if errors.Is(err, sql.ErrNoRows) {
		if commitErr := tx.Commit(); commitErr != nil {
			return PullJob{}, commitErr
		}
		return PullJob{}, ErrNoPullJob
	}
	if err != nil {
		return PullJob{}, err
	}
	if job.Attempts >= maxAttempts {
		if _, err = tx.ExecContext(ctx, "UPDATE github_pull_jobs SET state='FAILED',updated_at=? WHERE workflow_id=?", stamp, job.WorkflowID); err != nil {
			return PullJob{}, err
		}
		if err = tx.Commit(); err != nil {
			return PullJob{}, err
		}
		return PullJob{}, ErrNoPullJob
	}
	job.Attempts++
	if _, err = tx.ExecContext(ctx, `UPDATE github_pull_jobs SET state='RUNNING',attempts=?,started_at=?,updated_at=? WHERE workflow_id=? AND state='PENDING'`, job.Attempts, stamp, stamp, job.WorkflowID); err != nil {
		return PullJob{}, err
	}
	job.State = "RUNNING"
	job.StartedAt, job.NextAttempt = now, parseTime(next)
	if err := tx.Commit(); err != nil {
		return PullJob{}, err
	}
	return job, nil
}

func (s *Store) CompletePullJob(ctx context.Context, workflowID string, now time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE github_pull_jobs SET state='COMPLETED',updated_at=?,last_error='' WHERE workflow_id=? AND state='RUNNING'`, now.UTC().Format(time.RFC3339Nano), workflowID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var state string
		if err := s.db.QueryRowContext(ctx, "SELECT state FROM github_pull_jobs WHERE workflow_id=?", workflowID).Scan(&state); err != nil {
			return err
		}
		if state == "COMPLETED" {
			return nil
		}
		return fmt.Errorf("pull job %s cannot complete from %s", workflowID, state)
	}
	return nil
}

func (s *Store) RetryPullJob(ctx context.Context, workflowID string, cause error, next time.Time, maxAttempts int) error {
	if cause == nil || maxAttempts < 1 {
		return errors.New("pull job error and attempt limit are required")
	}
	message := strings.TrimSpace(cause.Error())
	if len(message) > 2000 {
		message = message[:2000]
	}
	state := "PENDING"
	res, err := s.db.ExecContext(ctx, `UPDATE github_pull_jobs SET state=CASE WHEN attempts>=? THEN 'FAILED' ELSE ? END,started_at=NULL,next_attempt=?,last_error=?,updated_at=? WHERE workflow_id=? AND state='RUNNING'`, maxAttempts, state, next.UTC().Format(time.RFC3339Nano), message, time.Now().UTC().Format(time.RFC3339Nano), workflowID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("pull job is not running")
	}
	return nil
}

func (s *Store) PullJob(ctx context.Context, workflowID string) (PullJob, error) {
	var job PullJob
	var started, next string
	err := s.db.QueryRowContext(ctx, `SELECT workflow_id,repository,pull_number,head_sha,delivery_id,payload,state,attempts,COALESCE(started_at,''),next_attempt,last_error FROM github_pull_jobs WHERE workflow_id=?`, workflowID).Scan(&job.WorkflowID, &job.Repository, &job.Number, &job.HeadSHA, &job.DeliveryID, &job.Payload, &job.State, &job.Attempts, &started, &next, &job.LastError)
	if err != nil {
		return PullJob{}, err
	}
	job.StartedAt, job.NextAttempt = parseTime(started), parseTime(next)
	return job, nil
}

func parseTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, value)
	return t
}

type PullWorkflow struct {
	Repository  string `json:"repository"`
	Number      int    `json:"pull_number"`
	HeadSHA     string `json:"head_sha"`
	WorkflowID  string `json:"workflow_id"`
	ManualActor string `json:"manual_actor,omitempty"`
	TriggeredAt string `json:"manual_triggered_at,omitempty"`
}

func (s *Store) PullWorkflows(ctx context.Context, repository string) ([]PullWorkflow, error) {
	if repository == "" {
		return nil, errors.New("repository is required")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT p.repository,p.pull_number,p.head_sha,p.workflow_id,COALESCE(m.actor,''),COALESCE(m.created_at,'') FROM github_pull_workflows p LEFT JOIN github_manual_triggers m ON m.workflow_id=p.workflow_id WHERE p.repository=? ORDER BY p.pull_number,p.head_sha`, repository)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PullWorkflow{}
	for rows.Next() {
		var p PullWorkflow
		if err := rows.Scan(&p.Repository, &p.Number, &p.HeadSHA, &p.WorkflowID, &p.ManualActor, &p.TriggeredAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
