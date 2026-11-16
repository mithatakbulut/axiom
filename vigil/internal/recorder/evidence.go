package recorder

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

type Requirement struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}
type Contract struct {
	ID           string        `json:"id"`
	Title        string        `json:"title"`
	Requirements []Requirement `json:"requirements"`
}
type Evidence struct {
	Kind         string          `json:"kind"`
	Source       string          `json:"source"`
	Result       string          `json:"result"`
	State        string          `json:"state"`
	EventID      string          `json:"event_id,omitempty"`
	Provenance   string          `json:"provenance"`
	EventSummary json.RawMessage `json:"event_summary,omitempty"`
}
type RequirementStatus struct {
	Requirement
	State    string    `json:"state"`
	Evidence *Evidence `json:"evidence,omitempty"`
}
type Verification struct {
	Session      string              `json:"session_id"`
	Task         string              `json:"task_id"`
	Title        string              `json:"title"`
	Requirements []RequirementStatus `json:"requirements"`
	Complete     bool                `json:"complete"`
	Counts       map[string]int      `json:"counts"`
}

func SafeText(s string, limit int) bool {
	return len(s) > 0 && len(s) <= limit && utf8.ValidString(s) && strings.TrimSpace(s) != "" && !secret.MatchString(s) && !strings.ContainsAny(s, "\x00\r")
}
func ValidID(s string) bool { return clean(s) == s && s != "" && !strings.ContainsAny(s, " /") }

func (s *Store) CreateTask(ctx context.Context, session string, c Contract, now time.Time) error {
	if !ValidID(session) || !ValidID(c.ID) || !SafeText(c.Title, 256) || len(c.Requirements) == 0 || len(c.Requirements) > 100 {
		return errors.New("invalid task contract")
	}
	seen := map[string]bool{}
	for _, req := range c.Requirements {
		if !ValidID(req.ID) || !SafeText(req.Description, 1024) || seen[req.ID] {
			return errors.New("invalid or duplicate requirement")
		}
		seen[req.ID] = true
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `INSERT INTO tasks VALUES(?,?,?,?)`, session, c.ID, c.Title, stamp(now)); err != nil {
		return fmt.Errorf("could not create task: %w", err)
	}
	for _, req := range c.Requirements {
		if _, err = tx.ExecContext(ctx, `INSERT INTO requirements VALUES(?,?,?,?)`, session, c.ID, req.ID, req.Description); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) TaskAt(ctx context.Context, session string, at time.Time) (string, string, error) {
	var id, since string
	err := s.db.QueryRowContext(ctx, `SELECT task_id,created_at FROM tasks WHERE session_id=? AND created_at<=? ORDER BY created_at DESC,rowid DESC LIMIT 1`, session, stamp(at)).Scan(&id, &since)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	return id, since, err
}

func (s *Store) RecordEvidence(ctx context.Context, session, task, requirement string, e Evidence, now time.Time) error {
	if !SafeText(e.Source, 512) || !SafeText(e.Result, 1024) {
		return errors.New("evidence requires a bounded source and result without credentials")
	}
	switch e.Kind {
	case "unit_test", "integration_test", "e2e_test", "browser", "static_analysis", "type_check", "build", "git_diff", "manual":
	default:
		return errors.New("invalid evidence kind")
	}
	switch e.State {
	case "evidenced", "failed":
	case "verified":
		if e.Kind == "git_diff" {
			return errors.New("a diff alone cannot verify a requirement")
		}
		if (e.Kind == "unit_test" || e.Kind == "integration_test" || e.Kind == "e2e_test") && e.EventID == "" {
			return errors.New("verified test evidence requires a linked tool event")
		}
	case "waived":
		if e.Kind != "manual" {
			return errors.New("a waiver requires an explicit manual source and reason")
		}
	default:
		return errors.New("invalid evidence state")
	}
	var event any
	if e.EventID != "" {
		event = e.EventID
	}
	result, err := s.db.ExecContext(ctx, `
 INSERT INTO evidence(session_id,task_id,requirement_id,kind,source,result,state,event_id,recorded_at,event_summary)
 SELECT ?,?,?,?,?,?,?,?,?,(SELECT json_object('event_id',e.event_id,'timestamp',e.timestamp,'exit_code',json_extract(e.data,'$.exit_code'),'is_error',json_extract(e.data,'$.is_error'),'test_passed',json_extract(e.data,'$.test_passed'),'test_failed',json_extract(e.data,'$.test_failed')) FROM events e WHERE e.event_id=?)
 WHERE ? IS NULL OR EXISTS(
  SELECT 1 FROM events e JOIN tasks t ON t.session_id=e.session_id AND t.task_id=?
  WHERE e.event_id=? AND e.session_id=? AND e.event_type='PostToolUse' AND e.timestamp>=t.created_at
  AND (? NOT IN ('unit_test','integration_test','e2e_test') OR json_type(e.data,'$.test_failed') IS 'integer')
  AND (?!='verified' OR (COALESCE(json_extract(e.data,'$.is_error'),0)=0 AND
    (json_extract(e.data,'$.exit_code')=0 OR (json_extract(e.data,'$.exit_code') IS NULL AND json_extract(e.data,'$.test_failed')=0))
    AND COALESCE(json_extract(e.data,'$.test_failed'),0)=0
    AND COALESCE(json_extract(e.data,'$.test_passed'),1)>0)))
 `, session, task, requirement, e.Kind, e.Source, e.Result, e.State, event, stamp(now), event, event, task, event, session, e.Kind, e.State)
	if err != nil {
		return fmt.Errorf("could not attach evidence to requirement: %w", err)
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return errors.New("tool event is missing or does not support this verification state")
	}
	return err
}

func (s *Store) Verification(ctx context.Context, session, task string) (Verification, error) {
	v := Verification{Session: session, Task: task, Requirements: []RequirementStatus{}, Counts: map[string]int{}, Complete: true}
	rows, err := s.db.QueryContext(ctx, `
 SELECT t.title,r.requirement_id,r.description,e.kind,e.source,e.result,e.state,e.event_id,e.event_summary
 FROM tasks t JOIN requirements r USING(session_id,task_id)
 LEFT JOIN evidence e ON e.id=(SELECT max(id) FROM evidence WHERE session_id=r.session_id AND task_id=r.task_id AND requirement_id=r.requirement_id)
 WHERE t.session_id=? AND t.task_id=? ORDER BY r.requirement_id`, session, task)
	if err != nil {
		return v, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var r RequirementStatus
		var kind, source, result, state, event, summary sql.NullString
		if err = rows.Scan(&v.Title, &r.ID, &r.Description, &kind, &source, &result, &state, &event, &summary); err != nil {
			return v, err
		}
		r.State = "pending"
		if state.Valid {
			r.State = state.String
			provenance := "tool_event"
			if kind.String == "manual" || !summary.Valid {
				provenance = "manual_declaration"
			}
			r.Evidence = &Evidence{Kind: kind.String, Source: source.String, Result: result.String, State: state.String, EventID: event.String, Provenance: provenance, EventSummary: json.RawMessage(summary.String)}
		}
		v.Counts[r.State]++
		if r.State != "verified" && r.State != "waived" {
			v.Complete = false
		}
		v.Requirements = append(v.Requirements, r)
	}
	if err = rows.Err(); err != nil {
		return v, err
	}
	if len(v.Requirements) == 0 {
		v.Complete = false
		return v, sql.ErrNoRows
	}
	return v, nil
}
