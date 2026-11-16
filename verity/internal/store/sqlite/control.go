package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"verity/internal/process/control"
)

func (s *Store) SaveDefinition(ctx context.Context, d control.Definition) error {
	if err := d.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO process_definitions(id,version,body) VALUES(?,?,?)", d.ID, d.Version, string(b))
	return err
}
func (s *Store) GetDefinition(ctx context.Context, id string, version int) (control.Definition, error) {
	var d control.Definition
	var b string
	err := s.db.QueryRowContext(ctx, "SELECT body FROM process_definitions WHERE id=? AND version=?", id, version).Scan(&b)
	if err != nil {
		return d, err
	}
	err = json.Unmarshal([]byte(b), &d)
	return d, err
}
func (s *Store) CreateRun(ctx context.Context, r control.Run) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO process_runs VALUES(?,?,?,?)", r.ID, r.DefinitionID, r.DefinitionVersion, string(b))
	return err
}
func (s *Store) GetRun(ctx context.Context, id string) (control.Run, error) {
	var r control.Run
	var b string
	err := s.db.QueryRowContext(ctx, "SELECT body FROM process_runs WHERE id=?", id).Scan(&b)
	if err != nil {
		return r, err
	}
	err = json.Unmarshal([]byte(b), &r)
	return r, err
}

// UpdateRun uses compare-and-swap against the previous transition count to prevent lost scheduler updates.
func (s *Store) UpdateRun(ctx context.Context, r control.Run, expectedTransitions int) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, "UPDATE process_runs SET body=? WHERE id=? AND json_extract(body,'$.transitions')=?", string(b), r.ID, expectedTransitions)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return fmt.Errorf("run changed concurrently or not found")
	}
	return nil
}
func (s *Store) AcquireLease(ctx context.Context, runID, owner string, now time.Time, ttl time.Duration) (control.Lease, error) {
	if owner == "" || ttl <= 0 {
		return control.Lease{}, fmt.Errorf("owner and positive lease duration required")
	}
	until := now.UTC().Add(ttl)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return control.Lease{}, err
	}
	defer tx.Rollback()
	// UTC RFC3339Nano omits the fractional part at whole seconds. Removing
	// the trailing Z makes whole seconds sort before their fractional values.
	_, err = tx.ExecContext(ctx, "INSERT INTO scheduler_leases(run_id,owner,expires_at) VALUES(?,?,?) ON CONFLICT(run_id) DO UPDATE SET owner=excluded.owner,expires_at=excluded.expires_at WHERE rtrim(scheduler_leases.expires_at,'Z')<=rtrim(?,'Z') OR scheduler_leases.owner=?", runID, owner, until.Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano), owner)
	if err != nil {
		return control.Lease{}, err
	}
	var got control.Lease
	var exp string
	err = tx.QueryRowContext(ctx, "SELECT run_id,owner,expires_at FROM scheduler_leases WHERE run_id=?", runID).Scan(&got.RunID, &got.Owner, &exp)
	if err != nil {
		return got, err
	}
	got.ExpiresAt, _ = time.Parse(time.RFC3339Nano, exp)
	if got.Owner != owner {
		return control.Lease{}, fmt.Errorf("scheduler lease held by %s", got.Owner)
	}
	if err = tx.Commit(); err != nil {
		return control.Lease{}, err
	}
	return got, nil
}
func (s *Store) ListRuns(ctx context.Context) ([]control.Run, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT body FROM process_runs ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []control.Run{}
	for rows.Next() {
		var b string
		var r control.Run
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(b), &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

var _ = sql.ErrNoRows
