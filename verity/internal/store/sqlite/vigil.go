package sqlite

import (
	"context"
	"encoding/json"
	"errors"

	vigilbridge "verity/internal/integrations/vigil"
)

func (s *Store) SaveVigilLink(ctx context.Context, link vigilbridge.Link) error {
	if err := link.Validate(); err != nil {
		return err
	}
	if link.CandidateSHA == "" || link.VigilVersion == "" {
		return errors.New("Vigil proof reference requires candidate SHA and detected Vigil version")
	}
	body, err := json.Marshal(link)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO vigil_links(attempt_id,workflow_id,candidate_sha,body) VALUES(?,?,?,?)`, link.AttemptID, link.WorkflowID, link.CandidateSHA, string(body))
	return err
}

func (s *Store) VigilLinks(ctx context.Context, workflowID, candidateSHA string) ([]vigilbridge.Link, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT body FROM vigil_links WHERE workflow_id=? AND candidate_sha=? ORDER BY attempt_id`, workflowID, candidateSHA)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []vigilbridge.Link{}
	for rows.Next() {
		var body string
		var link vigilbridge.Link
		if err = rows.Scan(&body); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(body), &link); err != nil {
			return nil, err
		}
		if err = link.Validate(); err != nil {
			return nil, err
		}
		out = append(out, link)
	}
	return out, rows.Err()
}
