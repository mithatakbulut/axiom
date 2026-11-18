//go:build darwin || linux

package verification

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// acquireSnapshotSlot serializes host snapshots for this Unix user across
// Verity processes. The lease remains held until the returned cleanup runs.
func acquireSnapshotSlot(ctx context.Context) (func(), error) {
	temp := os.TempDir()
	dir := filepath.Join(temp, fmt.Sprintf("verity-snapshot-quota-%d", os.Getuid()))
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 || !ownedByCurrentUID(info) {
		return nil, errors.New("snapshot quota directory is not private and user-owned")
	}
	lockPath := filepath.Join(dir, "active.lock")
	if existing, err := os.Lstat(lockPath); err == nil && !existing.Mode().IsRegular() {
		return nil, errors.New("snapshot quota lock is not a regular file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	fileInfo, err := file.Stat()
	if err != nil || !fileInfo.Mode().IsRegular() || fileInfo.Mode().Perm()&0o077 != 0 || !ownedByCurrentUID(fileInfo) {
		_ = file.Close()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("snapshot quota lock is not a private user-owned file")
	}
	for {
		if err := ctx.Err(); err != nil {
			_ = file.Close()
			return nil, err
		}
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			if err := removeOrphanSnapshots(temp); err != nil {
				_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				_ = file.Close()
				return nil, err
			}
			var once sync.Once
			return func() {
				once.Do(func() {
					_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
					_ = file.Close()
				})
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = file.Close()
			return nil, err
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = file.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func ownedByCurrentUID(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid())
}

// A crashed process releases flock automatically. Once exclusive ownership is
// acquired, no current Verity process can still be using these owned temp
// objects, so stale snapshots and candidate tar files can be safely removed.
func removeOrphanSnapshots(temp string) error {
	entries, err := os.ReadDir(temp)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		isSnapshot := strings.HasPrefix(name, "verity-candidate-snapshot-")
		isArchive := strings.HasPrefix(name, "verity-candidate-") && strings.HasSuffix(name, ".tar")
		if !isSnapshot && !isArchive {
			continue
		}
		path := filepath.Join(temp, name)
		info, err := os.Lstat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		if !ownedByCurrentUID(info) {
			continue
		}
		if isSnapshot && info.IsDir() && info.Mode().Perm() == 0o700 {
			marker := filepath.Join(path, snapshotOwnerMarker)
			markerInfo, markerErr := os.Lstat(marker)
			if markerErr != nil {
				if errors.Is(markerErr, os.ErrNotExist) {
					continue
				}
				return markerErr
			}
			if !markerInfo.Mode().IsRegular() || markerInfo.Mode().Perm() != 0o600 || !ownedByCurrentUID(markerInfo) {
				continue
			}
			markerData, err := os.ReadFile(marker)
			if err != nil {
				return err
			}
			if string(markerData) == snapshotOwnerMarkerValue {
				if err := os.RemoveAll(path); err != nil {
					return err
				}
			}
		}
		if isArchive && info.Mode().IsRegular() && info.Mode().Perm() == 0o600 {
			if err := os.Remove(path); err != nil {
				return err
			}
		}
	}
	return nil
}
