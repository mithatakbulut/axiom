// Package handoff creates portable, non-executable instructions for manual work.
package handoff

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"verity/internal/execution"
)

type Package struct {
	SchemaVersion int               `json:"schema_version"`
	Request       execution.Request `json:"request"`
	CreatedAt     time.Time         `json:"created_at"`
	Instructions  string            `json:"instructions"`
}

func New(req execution.Request, now time.Time) (Package, error) {
	if err := req.Validate(); err != nil {
		return Package{}, err
	}
	return Package{SchemaVersion: 1, Request: req, CreatedAt: now.UTC(), Instructions: "Work only in the supplied workspace. Do not claim verification passed. Return the resulting full Git commit SHA and a concise summary."}, nil
}

func Write(path string, p Package) error {
	if p.SchemaVersion != 1 || p.CreatedAt.IsZero() {
		return execution.ErrInvalid
	}
	if err := p.Request.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(b, '\n'), 0600); err != nil {
		return fmt.Errorf("write handoff: %w", err)
	}
	return nil
}
