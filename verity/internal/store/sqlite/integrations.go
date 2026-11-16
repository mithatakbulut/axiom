package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"

	"errors"
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
	return tx.Commit()
}

type PullWorkflow struct {
	Repository string `json:"repository"`
	Number     int    `json:"pull_number"`
	HeadSHA    string `json:"head_sha"`
	WorkflowID string `json:"workflow_id"`
}

func (s *Store) PullWorkflows(ctx context.Context, repository string) ([]PullWorkflow, error) {
	if repository == "" {
		return nil, errors.New("repository is required")
	}
	rows, err := s.db.QueryContext(ctx, "SELECT repository,pull_number,head_sha,workflow_id FROM github_pull_workflows WHERE repository=? ORDER BY pull_number,head_sha", repository)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PullWorkflow{}
	for rows.Next() {
		var p PullWorkflow
		if err := rows.Scan(&p.Repository, &p.Number, &p.HeadSHA, &p.WorkflowID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
