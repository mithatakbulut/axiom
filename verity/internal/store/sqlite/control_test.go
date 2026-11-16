package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"verity/internal/process/control"
)

func TestProcessRunSnapshotRecoveryAndLease(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db.sqlite")
	s, e := Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	d := control.Template("bugfix", "bug-v1")
	if e = s.SaveDefinition(ctx, d); e != nil {
		t.Fatal(e)
	}
	r, e := control.NewRun("run-1", d, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if e = s.CreateRun(ctx, r); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AcquireLease(ctx, r.ID, "worker-a", time.Now(), time.Minute); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AcquireLease(ctx, r.ID, "worker-b", time.Now(), time.Minute); e == nil {
		t.Fatal("concurrent live lease acquired")
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	got, e := s.GetRun(ctx, r.ID)
	if e != nil {
		t.Fatal(e)
	}
	if got.DefinitionVersion != 1 || len(got.Tasks) != 3 {
		t.Fatalf("snapshot recovery: %+v", got)
	}
	if e = got.Start(); e != nil {
		t.Fatal(e)
	}
	if e = s.UpdateRun(ctx, got, 0); e != nil {
		t.Fatal(e)
	}
	if _, e = s.GetDefinition(ctx, d.ID, 1); e != nil {
		t.Fatal(e)
	}
}

func TestLeaseExpirationAtSecondBoundary(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r, err := control.NewRun("lease-run", control.Template("bugfix", "lease-def"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CreateRun(ctx, r); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err = s.AcquireLease(ctx, r.ID, "a", base.Add(-time.Second), 1100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcquireLease(ctx, r.ID, "b", base, time.Second); err == nil {
		t.Fatal("live fractional-second lease was taken early")
	}
	if _, err = s.AcquireLease(ctx, r.ID, "b", base.Add(100*time.Millisecond), 900*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcquireLease(ctx, r.ID, "c", base.Add(time.Second+time.Nanosecond), time.Second); err != nil {
		t.Fatalf("expired whole-second lease was not released: %v", err)
	}
}

func TestLeaseConcurrentAcquisition(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db.sqlite")
	a, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	r, err := control.NewRun("lease-run", control.Template("bugfix", "lease-def"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = a.CreateRun(ctx, r); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, store := range []*Store{a, b} {
		owner := []string{"a", "b"}[i]
		go func() { <-start; _, err := store.AcquireLease(ctx, r.ID, owner, now, time.Second); results <- err }()
	}
	close(start)
	successes := 0
	for range 2 {
		if <-results == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful acquisitions = %d, want 1", successes)
	}
}
