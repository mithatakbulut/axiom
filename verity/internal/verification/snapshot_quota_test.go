//go:build darwin || linux

package verification

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestSnapshotQuotaSerializesAndReapsOwnedOrphans(t *testing.T) {
	unrelatedDir, err := os.MkdirTemp(os.TempDir(), "verity-verify-unrelated-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(unrelatedDir)
	if err := os.WriteFile(filepath.Join(unrelatedDir, "keep"), []byte("not ours"), 0o600); err != nil {
		t.Fatal(err)
	}
	orphanDir, err := os.MkdirTemp(os.TempDir(), "verity-candidate-snapshot-orphan-test-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphanDir, snapshotOwnerMarker), []byte(snapshotOwnerMarkerValue), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(orphanDir, "source"), 0o700); err != nil {
		t.Fatal(err)
	}
	archive, err := os.CreateTemp(os.TempDir(), "verity-candidate-orphan-test-*.tar")
	if err != nil {
		t.Fatal(err)
	}
	archivePath := archive.Name()
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	first, err := acquireSnapshotSlot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer first()
	if _, err := os.Stat(orphanDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale snapshot remains after quota acquisition: %v", err)
	}
	if _, err := os.Stat(archivePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale candidate archive remains after quota acquisition: %v", err)
	}
	if _, err := os.Stat(filepath.Join(unrelatedDir, "keep")); err != nil {
		t.Fatalf("unmarked temp directory was modified: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	if release, err := acquireSnapshotSlot(ctx); release != nil || !errors.Is(err, context.DeadlineExceeded) {
		if release != nil {
			release()
		}
		t.Fatalf("second snapshot lease returned release=%t, err=%v; want context deadline", release != nil, err)
	}
	first()
	second, err := acquireSnapshotSlot(context.Background())
	if err != nil {
		t.Fatalf("quota lock was not released: %v", err)
	}
	second()
}

func TestSnapshotQuotaChildProcessLease(t *testing.T) {
	if os.Getenv("VERITY_SNAPSHOT_QUOTA_CHILD") == "1" {
		release, err := acquireSnapshotSlot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		if err := os.WriteFile(os.Getenv("VERITY_SNAPSHOT_QUOTA_READY"), []byte("ready"), 0o600); err != nil {
			t.Fatal(err)
		}
		time.Sleep(300 * time.Millisecond)
		return
	}
	ready := filepath.Join(t.TempDir(), "ready")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(executable, "-test.run=^TestSnapshotQuotaChildProcessLease$")
	child.Env = append(os.Environ(), "VERITY_SNAPSHOT_QUOTA_CHILD=1", "VERITY_SNAPSHOT_QUOTA_READY="+ready)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	waitUntil := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(waitUntil) {
			_ = child.Process.Kill()
			_ = child.Wait()
			t.Fatal("child process did not acquire snapshot quota lease")
		}
		time.Sleep(10 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	if release, err := acquireSnapshotSlot(ctx); release != nil || !errors.Is(err, context.DeadlineExceeded) {
		if release != nil {
			release()
		}
		_ = child.Process.Kill()
		_ = child.Wait()
		t.Fatalf("cross-process snapshot lease returned release=%t, err=%v", release != nil, err)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatalf("simulate crashed lease owner: %v", err)
	}
	_ = child.Wait()
	release, err := acquireSnapshotSlot(context.Background())
	if err != nil {
		t.Fatalf("snapshot lock remained held after child exit: %v", err)
	}
	release()
}
