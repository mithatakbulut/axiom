package proof

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var ErrDigestMismatch = errors.New("artifact digest mismatch")

type ArtifactStore interface {
	Put(context.Context, []byte) (string, error)
	Get(context.Context, string) ([]byte, error)
	Delete(context.Context, string) error
}

// LocalArtifactStore is a content-addressed store. The root directory must be application-owned.
type LocalArtifactStore struct{ Root string }

func (s LocalArtifactStore) path(d string) (string, error) {
	if !validSHA(d, 64) {
		return "", ErrDigestMismatch
	}
	return filepath.Join(s.Root, d[:2], d[2:]), nil
}
func (s LocalArtifactStore) Put(ctx context.Context, b []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	d := hex.EncodeToString(sum[:])
	p, _ := s.path(d)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return "", err
	}
	if existing, err := os.ReadFile(p); err == nil {
		if sha256Hex(existing) != d {
			return "", ErrDigestMismatch
		}
		return d, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".verity-artifact-*")
	if err != nil {
		return "", err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return "", err
	}
	if _, err = f.Write(b); err != nil {
		f.Close()
		return "", err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	// Publish a complete object atomically. Link fails if another writer
	// already published this digest, so readers never observe a partial file.
	if err = os.Link(temp, p); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
		existing, readErr := os.ReadFile(p)
		if readErr != nil {
			return "", readErr
		}
		if sha256Hex(existing) != d {
			return "", ErrDigestMismatch
		}
	}
	return d, nil
}
func (s LocalArtifactStore) Get(ctx context.Context, d string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p, err := s.path(d)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	if sha256Hex(b) != d {
		return nil, ErrDigestMismatch
	}
	return b, nil
}
func (s LocalArtifactStore) Delete(ctx context.Context, d string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := s.path(d)
	if err != nil {
		return err
	}
	return os.Remove(p)
}

type RetentionPolicy struct {
	MaxAgeDays int      `json:"max_age_days"`
	Redact     []string `json:"redact,omitempty"`
}

func (p RetentionPolicy) Validate() error {
	if p.MaxAgeDays < 0 {
		return errors.New("max_age_days cannot be negative")
	}
	for _, v := range p.Redact {
		if strings.TrimSpace(v) == "" {
			return errors.New("redaction values cannot be empty")
		}
	}
	return nil
}
func (p RetentionPolicy) Apply(b []byte) []byte {
	out := string(b)
	for _, v := range p.Redact {
		out = strings.ReplaceAll(out, v, "[REDACTED]")
	}
	return []byte(out)
}
