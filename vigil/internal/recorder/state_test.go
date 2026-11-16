package recorder

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAdvisorReservationsAcrossConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()
	var succeeded atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := a
			if i%2 == 0 {
				s = b
			}
			id, err := s.Reserve(context.Background(), Reservation{Session: "s", Task: "task", Turn: "turn", Model: "model", DefaultMode: "autonomous", Automatic: true, PerTurn: 1, PerTask: 2, Parallel: 2, Lease: time.Minute}, time.Now())
			if err != nil {
				t.Error(err)
			}
			if id != 0 {
				succeeded.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if succeeded.Load() != 1 {
		t.Fatalf("advisor slots reserved: %d", succeeded.Load())
	}
}

func TestEvidenceAndBackup(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	now := time.Now()
	contract := Contract{ID: "task", Title: "Ship a fix", Requirements: []Requirement{{"unit", "Focused tests pass"}, {"manual", "Review behavior"}}}
	if err = s.CreateTask(ctx, "s", contract, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	for i, counts := range []string{"1 passed, 1 failed", "2 passed, 0 failed"} {
		raw := fmt.Sprintf(`{"hook_event_name":"PostToolUse","session_id":"s","turn_id":"t","tool_name":"Bash","tool_use_id":"%d","exit_code":%d,"tool_response":"Tests: %s"}`, i, 1-i, counts)
		e, err := Parse(strings.NewReader(raw), now)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Record(ctx, e, 30); err != nil {
			t.Fatal(err)
		}
		record := Evidence{Kind: "unit_test", Source: "fixture_test.go", Result: "Focused check", State: "verified", EventID: e.ID}
		if err = s.RecordEvidence(ctx, "s", "task", "unit", record, now); (err != nil) != (i == 0) {
			t.Fatalf("verify test %d: %v", i, err)
		}
	}
	v, err := s.Verification(ctx, "s", "task")
	if err != nil || v.Complete || v.Counts["verified"] != 1 || v.Counts["pending"] != 1 {
		t.Fatalf("evidence: %+v %v", v, err)
	}
	if err = s.RecordEvidence(ctx, "s", "task", "manual", Evidence{Kind: "manual", Source: "Review ticket", Result: "Explicitly waived", State: "waived"}, now); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "backup.db")
	if err = s.Backup(ctx, dest); err != nil {
		t.Fatal(err)
	}
	if err = s.Backup(ctx, dest); err == nil {
		t.Fatal("overwrote backup")
	}
	copy, err := Open(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = copy.Close() }()
	v, err = copy.Verification(ctx, "s", "task")
	if err != nil || !v.Complete || v.Counts["waived"] != 1 {
		t.Fatalf("backup evidence: %+v %v", v, err)
	}
}

func TestAmbiguousTestSummariesCannotVerify(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	now := time.Now()
	if err := s.CreateTask(ctx, "s", Contract{ID: "task", Title: "Fix", Requirements: []Requirement{{"r", "Tests pass"}}}, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	raw := `{"hook_event_name":"PostToolUse","session_id":"s","tool_name":"Bash","tool_use_id":"1","exit_code":0,"tool_response":"Tests: 5 passed, 0 failed\nTests: 4 passed, 1 failed"}`
	e, err := Parse(strings.NewReader(raw), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Record(ctx, e, 30); err != nil {
		t.Fatal(err)
	}
	err = s.RecordEvidence(ctx, "s", "task", "r", Evidence{Kind: "unit_test", Source: "tests", Result: "passed", State: "verified", EventID: e.ID}, now)
	if err == nil {
		t.Fatal("ambiguous summaries were accepted as verified")
	}
}

func TestVerifiedTestEvidenceRequiresEvent(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	now := time.Now()
	if err := s.CreateTask(ctx, "s", Contract{ID: "task", Title: "Fix", Requirements: []Requirement{{"r", "Tests pass"}}}, now); err != nil {
		t.Fatal(err)
	}
	err = s.RecordEvidence(ctx, "s", "task", "r", Evidence{Kind: "unit_test", Source: "operator", Result: "passed", State: "verified"}, now)
	if err == nil {
		t.Fatal("accepted verified test evidence without event")
	}
}

func TestEvidenceSummarySurvivesRetention(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	now := time.Now().UTC()
	if err := s.CreateTask(ctx, "s", Contract{ID: "task", Title: "Fix", Requirements: []Requirement{{"r", "Tests pass"}}}, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	e, err := Parse(strings.NewReader(`{"hook_event_name":"PostToolUse","session_id":"s","tool_name":"Bash","exit_code":0,"tool_response":"Tests: 2 passed, 0 failed"}`), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Record(ctx, e, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordEvidence(ctx, "s", "task", "r", Evidence{Kind: "unit_test", Source: "test", Result: "pass", State: "verified", EventID: e.ID}, now); err != nil {
		t.Fatal(err)
	}
	later, err := Parse(strings.NewReader(`{"hook_event_name":"Stop","session_id":"s"}`), now.AddDate(0, 0, 2))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Record(ctx, later, 1); err != nil {
		t.Fatal(err)
	}
	v, err := s.Verification(ctx, "s", "task")
	if err != nil {
		t.Fatal(err)
	}
	ev := v.Requirements[0].Evidence
	if !v.Complete || ev.EventID != "" || ev.Provenance != "tool_event" || !strings.Contains(string(ev.EventSummary), `"test_passed":2`) || !strings.Contains(string(ev.EventSummary), e.ID) {
		t.Fatalf("lost retained evidence: %+v", ev)
	}
}

func TestMigrationDemotesUnrecoverableTestEvidence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := s.CreateTask(ctx, "s", Contract{ID: "task", Title: "Fix", Requirements: []Requirement{{"r", "Tests pass"}}}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO evidence(session_id,task_id,requirement_id,kind,source,result,state,recorded_at) VALUES('s','task','r','unit_test','old','pass','verified',?)`, stamp(now)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`ALTER TABLE evidence DROP COLUMN event_summary; DELETE FROM schema_migrations WHERE version=4`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	v, err := s.Verification(ctx, "s", "task")
	if err != nil {
		t.Fatal(err)
	}
	if v.Complete || v.Requirements[0].State != "evidenced" {
		t.Fatalf("orphan test evidence accepted: %+v", v)
	}
}
