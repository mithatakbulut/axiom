package orchestration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"verity/internal/verification"
)

func TestOwnedWorkspaceMaterializesCandidateAndCleanupChecksOwner(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	snapshots := filepath.Join(root, "snapshots")
	if err := os.WriteFile(filepath.Join(root, "source.txt"), []byte("base"), 0600); err != nil {
		t.Fatal(err)
	}
	candidate, err := verification.CreateContentSnapshot(ctx, root, snapshots, []string{"source.txt"})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := createOwnedWorkspace(ctx, snapshots, candidate, "attempt-1")
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(workspace, "source.txt"))
	if err != nil || string(content) != "base" {
		t.Fatalf("materialized content %q: %v", content, err)
	}
	if err = os.WriteFile(filepath.Join(workspace, "source.txt"), []byte("candidate"), 0600); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(filepath.Join(root, "source.txt"))
	if err != nil || string(original) != "base" {
		t.Fatalf("project source changed: %q %v", original, err)
	}
	if err = removeOwnedWorkspace(workspace, "wrong-attempt"); err == nil {
		t.Fatal("wrong owner removed workspace")
	}
	if _, err = os.Stat(workspace); err != nil {
		t.Fatalf("workspace removed after owner mismatch: %v", err)
	}
	if err = removeOwnedWorkspace(workspace, "attempt-1"); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Lstat(workspace); !os.IsNotExist(err) {
		t.Fatalf("workspace remains: %v", err)
	}
}
