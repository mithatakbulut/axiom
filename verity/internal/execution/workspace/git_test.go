package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	b, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, b)
	}
	return string(b)
}
func repo(t *testing.T) string {
	t.Helper()
	p := t.TempDir()
	git(t, p, "init")
	git(t, p, "config", "user.email", "test@example.invalid")
	git(t, p, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(p, "file"), []byte("base"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, p, "add", "file")
	git(t, p, "commit", "-m", "initial")
	return p
}

func TestWorktreeCreateCleanupAndPreserveDirtyChanges(t *testing.T) {
	ctx := context.Background()
	root := repo(t)
	g := Git{}
	w, err := g.Create(ctx, root, filepath.Join(t.TempDir(), "work"))
	if err != nil {
		t.Fatal(err)
	}
	if sha, err := g.Head(ctx, w.Path); err != nil || sha != w.BaseSHA {
		t.Fatalf("base mismatch: %s %v", sha, err)
	}
	if err := g.Cleanup(ctx, root, w.Path); err != nil {
		t.Fatal(err)
	}
	w, err = g.Create(ctx, root, filepath.Join(t.TempDir(), "dirty"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.Path, "file"), []byte("user change"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := g.Cleanup(ctx, root, w.Path); err == nil {
		t.Fatal("dirty worktree cleanup should fail")
	}
	if b, err := os.ReadFile(filepath.Join(w.Path, "file")); err != nil || string(b) != "user change" {
		t.Fatal("user change was not preserved")
	}
}

func TestCreateRefusesExistingDestination(t *testing.T) {
	root := repo(t)
	dest := filepath.Join(t.TempDir(), "exists")
	if err := os.Mkdir(dest, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := (Git{}).Create(context.Background(), root, dest); err == nil {
		t.Fatal("expected refusal")
	}
}
