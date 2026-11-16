package recorder

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// Backup creates a consistent snapshot even while another hook is writing.
func (s *Store) Backup(ctx context.Context, path string) error {
	if path == "" {
		return errors.New("backup requires --output")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err = f.Close(); err == nil {
		_, err = s.db.ExecContext(ctx, "VACUUM INTO ?", path)
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}
