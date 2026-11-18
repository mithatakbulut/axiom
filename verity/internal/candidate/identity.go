// Package candidate defines typed immutable source identities shared across
// execution, verification, proof, and integration records.
package candidate

import (
	"encoding/hex"
	"errors"
	"time"
)

type Kind string

const (
	GitCommit     Kind = "git_commit"
	GitIndex      Kind = "git_index"
	ContentSource Kind = "content_snapshot"
)

var ErrInvalidIdentity = errors.New("invalid candidate identity")

// Identity identifies one captured source state. ID is a Git object ID for
// Git candidates and a SHA-256 digest for content snapshots.
type Identity struct {
	SchemaVersion int       `json:"schema_version"`
	Kind          Kind      `json:"kind"`
	ID            string    `json:"id"`
	Repository    string    `json:"repository,omitempty"`
	BaseRevision  string    `json:"base_revision,omitempty"`
	CapturedAt    time.Time `json:"captured_at"`
	Provenance    string    `json:"provenance"`
}

func (i Identity) IsZero() bool {
	return i.SchemaVersion == 0 && i.Kind == "" && i.ID == "" && i.Repository == "" && i.BaseRevision == "" && i.CapturedAt.IsZero() && i.Provenance == ""
}

func (i Identity) Validate() error {
	if i.SchemaVersion != 1 || i.ID == "" || i.CapturedAt.IsZero() || i.Provenance == "" {
		return ErrInvalidIdentity
	}
	switch i.Kind {
	case GitCommit:
		if !validObjectID(i.ID) || i.Repository == "" || (i.BaseRevision != "" && !validObjectID(i.BaseRevision)) {
			return ErrInvalidIdentity
		}
	case GitIndex:
		if !validObjectID(i.ID) || i.Repository == "" || !validObjectID(i.BaseRevision) {
			return ErrInvalidIdentity
		}
	case ContentSource:
		if len(i.ID) != 64 || !validHex(i.ID) || (i.BaseRevision != "" && !validObjectID(i.BaseRevision)) {
			return ErrInvalidIdentity
		}
	default:
		return ErrInvalidIdentity
	}
	return nil
}

func validObjectID(s string) bool {
	return (len(s) == 40 || len(s) == 64) && validHex(s)
}

func validHex(s string) bool {
	_, err := hex.DecodeString(s)
	return err == nil
}
