package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"verity/internal/proof"
	"verity/internal/verification"
)

func (s *Store) SaveEvidence(ctx context.Context, e proof.Evidence) error {
	if err := e.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO proof_evidence(id,workflow_id,candidate_sha,body) VALUES(?,?,?,?)", e.ID, e.WorkflowID, e.CandidateSHA, string(b))
	return err
}
func (s *Store) Evidence(ctx context.Context, wf, candidate string) ([]proof.Evidence, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT body FROM proof_evidence WHERE workflow_id=? AND candidate_sha=? ORDER BY id", wf, candidate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []proof.Evidence{}
	for rows.Next() {
		var b string
		var e proof.Evidence
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(b), &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *Store) AppendProofAudit(ctx context.Context, e proof.AuditEvent) error {
	if err := e.Validate(); err != nil {
		return err
	}
	var next int64
	if err := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(sequence),0)+1 FROM proof_audit WHERE workflow_id=?", e.WorkflowID).Scan(&next); err != nil {
		return err
	}
	if e.Sequence != next {
		return errors.New("proof audit sequence conflict")
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO proof_audit(workflow_id,sequence,body) VALUES(?,?,?)", e.WorkflowID, e.Sequence, string(b))
	return err
}
func (s *Store) ProofAudit(ctx context.Context, wf string) ([]proof.AuditEvent, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT body FROM proof_audit WHERE workflow_id=? ORDER BY sequence", wf)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []proof.AuditEvent{}
	for rows.Next() {
		var b string
		var e proof.AuditEvent
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(b), &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *Store) SaveVerification(ctx context.Context, r verification.Run) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if r.State == verification.StatePass {
		for _, c := range r.Checks {
			if c.Required && c.Outcome == verification.Passed && len(c.EvidenceIDs) == 0 {
				return errors.New("required passing check is missing evidence")
			}
		}
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO verification_runs(id,workflow_id,candidate_sha,body) VALUES(?,?,?,?)", r.ID, r.WorkflowID, r.CandidateSHA, string(b))
	return err
}
func (s *Store) Verification(ctx context.Context, id string) (verification.Run, error) {
	var b string
	var r verification.Run
	err := s.db.QueryRowContext(ctx, "SELECT body FROM verification_runs WHERE id=?", id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return r, err
	}
	if err != nil {
		return r, err
	}
	err = json.Unmarshal([]byte(b), &r)
	return r, err
}

// TaskVerifications returns the persisted proof history for a workflow task.
func (s *Store) TaskVerifications(ctx context.Context, wf, task string) ([]verification.Run, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT body FROM verification_runs WHERE workflow_id=? ORDER BY rowid`, wf)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []verification.Run
	for rows.Next() {
		var body string
		var run verification.Run
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(body), &run); err != nil {
			return nil, err
		}
		if run.TaskID == task {
			runs = append(runs, run)
		}
	}
	return runs, rows.Err()
}

// LatestPassingIndexVerification returns the most recently persisted passing
// pre-commit result for exactly this repository and Git tree.
func (s *Store) LatestPassingIndexVerification(ctx context.Context, repository, tree string) (verification.Run, bool, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT body FROM verification_runs WHERE candidate_sha=? ORDER BY rowid DESC", tree)
	if err != nil {
		return verification.Run{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var body string
		var run verification.Run
		if err := rows.Scan(&body); err != nil {
			return verification.Run{}, false, err
		}
		if err := json.Unmarshal([]byte(body), &run); err != nil {
			return verification.Run{}, false, err
		}
		if run.CandidateKind == verification.CandidateGitIndex && run.CandidateRepository == repository && run.Profile == verification.PreCommit && run.State == verification.StatePass {
			return run, true, nil
		}
	}
	return verification.Run{}, false, rows.Err()
}
