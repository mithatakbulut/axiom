package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"verity/internal/execution"
)

// CreateExecutionAttempt durably records invocation intent before a runtime is called.
func (s *Store) CreateExecutionAttempt(ctx context.Context, a execution.DurableAttempt) error {
	if err := a.Request.Validate(); err != nil {
		return err
	}
	if a.Provider == "" {
		return execution.ErrInvalid
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	a.State = execution.AttemptRunning
	a.Result = nil
	body, err := json.Marshal(a)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var previous int
	var previousState execution.AttemptState
	checkErr := tx.QueryRowContext(ctx, `SELECT a.attempt,e.state FROM execution_attempts a JOIN execution_attempt_events e ON e.attempt_id=a.id WHERE a.workflow_id=? AND a.task_id=? AND e.id=(SELECT MAX(x.id) FROM execution_attempt_events x WHERE x.attempt_id=a.id) ORDER BY a.attempt DESC LIMIT 1`, a.Request.WorkflowID, a.Request.TaskID).Scan(&previous, &previousState)
	if checkErr != nil && checkErr != sql.ErrNoRows {
		return checkErr
	}
	if checkErr == nil {
		if previousState == execution.AttemptUnknown || previousState == execution.AttemptRunning {
			return fmt.Errorf("previous task attempt is %s; resolve it before retry", previousState)
		}
		if a.Request.Attempt != previous+1 {
			return fmt.Errorf("attempt number must be %d", previous+1)
		}
	} else if a.Request.Attempt != 1 {
		return errors.New("first task attempt number must be 1")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO execution_attempts(id,workflow_id,task_id,attempt,provider,body,created_at) VALUES(?,?,?,?,?,?,?)`, a.Request.ID, a.Request.WorkflowID, a.Request.TaskID, a.Request.Attempt, a.Provider, string(body), a.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	if err = appendExecutionEvent(ctx, tx, a.Request.ID, "STARTED", execution.AttemptRunning, nil, "runtime", "invocation intent persisted", a.CreatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

// RecordExecutionResult appends the observed provider result. Identical retries are idempotent.
func (s *Store) RecordExecutionResult(ctx context.Context, id string, result execution.Result) error {
	if err := result.Validate(); err != nil {
		return err
	}
	b, _ := json.Marshal(result)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	state, old, err := latestExecutionEvent(ctx, tx, id)
	if err != nil {
		return err
	}
	if state == execution.AttemptFinished || state == execution.AttemptUnknown {
		if string(old) == string(b) {
			return tx.Commit()
		}
		return errors.New("conflicting execution result")
	}
	if state != execution.AttemptRunning {
		return fmt.Errorf("attempt is %s; reconcile before recording result", state)
	}
	nextState, kind := execution.AttemptFinished, "RESULT"
	if result.Outcome == execution.Unknown {
		nextState, kind = execution.AttemptUnknown, "UNKNOWN_RESULT"
	}
	if err = appendExecutionEvent(ctx, tx, id, kind, nextState, b, "runtime", "observed provider result", result.FinishedAt); err != nil {
		return err
	}
	return tx.Commit()
}

// RecoverRunningAttempts marks every still-running attempt UNKNOWN, preserving invocation intent.
func (s *Store) RecoverRunningAttempts(ctx context.Context, at time.Time) ([]string, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT a.id FROM execution_attempts a JOIN execution_attempt_events e ON e.attempt_id=a.id WHERE e.id=(SELECT MAX(x.id) FROM execution_attempt_events x WHERE x.attempt_id=a.id) AND e.state=? ORDER BY a.created_at`, execution.AttemptRunning)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for _, id := range ids {
		if err = appendExecutionEvent(ctx, tx, id, "RECOVERED_UNKNOWN", execution.AttemptUnknown, nil, "recovery", "process restarted while attempt was in flight", at); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return ids, nil
}

// ReconcileExecutionAttempt resolves only an UNKNOWN attempt with an explicit operator record.
func (s *Store) ReconcileExecutionAttempt(ctx context.Context, id string, r execution.Reconciliation) error {
	if err := r.Validate(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	state, old, err := latestExecutionEvent(ctx, tx, id)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(r)
	if state == execution.AttemptFinished || state == execution.AttemptAbandoned {
		if string(old) == string(b) {
			return tx.Commit()
		}
		return errors.New("attempt already resolved with a different decision")
	}
	if state != execution.AttemptUnknown {
		return fmt.Errorf("attempt is %s; only UNKNOWN attempts can be reconciled", state)
	}
	if err = appendExecutionEvent(ctx, tx, id, "RECONCILED", execution.AttemptFinished, b, r.Actor, r.Reason, r.At); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) AbandonExecutionAttempt(ctx context.Context, id, actor, reason string, at time.Time) error {
	if actor == "" || reason == "" {
		return execution.ErrInvalid
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	state, _, err := latestExecutionEvent(ctx, tx, id)
	if err != nil {
		return err
	}
	if state != execution.AttemptUnknown {
		return fmt.Errorf("attempt is %s; only UNKNOWN attempts can be abandoned", state)
	}
	payload, _ := json.Marshal(map[string]string{"actor": actor, "reason": reason})
	if err = appendExecutionEvent(ctx, tx, id, "ABANDONED", execution.AttemptAbandoned, payload, actor, reason, at); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ExecutionAttempt(ctx context.Context, id string) (execution.DurableAttempt, error) {
	var a execution.DurableAttempt
	var body string
	err := s.db.QueryRowContext(ctx, `SELECT body FROM execution_attempts WHERE id=?`, id).Scan(&body)
	if err != nil {
		return a, err
	}
	if err = json.Unmarshal([]byte(body), &a); err != nil {
		return a, err
	}
	state, payload, err := latestExecutionEvent(ctx, s.db, id)
	if err != nil {
		return a, err
	}
	a.State = state
	if state == execution.AttemptFinished {
		var result execution.Result
		var shape map[string]json.RawMessage
		_ = json.Unmarshal(payload, &shape)
		if _, reconciled := shape["actor"]; reconciled {
			var rec execution.Reconciliation
			if err = json.Unmarshal(payload, &rec); err != nil {
				return a, err
			}
			result = execution.Result{Outcome: rec.Outcome, CandidateSHA: rec.CandidateSHA, Summary: rec.Reason, StartedAt: a.CreatedAt, FinishedAt: rec.At}
		} else if err = json.Unmarshal(payload, &result); err != nil {
			return a, err
		}
		a.Result = &result
	}
	return a, nil
}

type executionQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func latestExecutionEvent(ctx context.Context, q executionQuerier, id string) (execution.AttemptState, []byte, error) {
	var state execution.AttemptState
	var payload string
	err := q.QueryRowContext(ctx, `SELECT state,payload FROM execution_attempt_events WHERE attempt_id=? ORDER BY id DESC LIMIT 1`, id).Scan(&state, &payload)
	return state, []byte(payload), err
}
func appendExecutionEvent(ctx context.Context, tx *sql.Tx, id, kind string, state execution.AttemptState, payload []byte, actor, reason string, at time.Time) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO execution_attempt_events(attempt_id,kind,state,payload,actor,reason,created_at) VALUES(?,?,?,?,?,?,?)`, id, kind, state, string(payload), actor, reason, at.UTC().Format(time.RFC3339Nano))
	return err
}

// MarkExecutionUnknown records that invocation began but no trustworthy result was returned.
func (s *Store) MarkExecutionUnknown(ctx context.Context, id, reason string, at time.Time) error {
	if reason == "" {
		return execution.ErrInvalid
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	state, _, err := latestExecutionEvent(ctx, tx, id)
	if err != nil {
		return err
	}
	if state == execution.AttemptUnknown {
		return tx.Commit()
	}
	if state != execution.AttemptRunning {
		return fmt.Errorf("attempt is %s", state)
	}
	if err = appendExecutionEvent(ctx, tx, id, "UNKNOWN", execution.AttemptUnknown, nil, "runtime", reason, at); err != nil {
		return err
	}
	return tx.Commit()
}
