package verification

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestContentSnapshotPreservesSourceAndRejectsTampering(t *testing.T) {
	root, store := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "source"), []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	digest, err := CreateContentSnapshot(context.Background(), root, store, []string{"source"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source"), []byte("second"), 0600); err != nil {
		t.Fatal(err)
	}
	path, cleanup, err := (ContentSnapshotter{Root: store}).Snapshot(context.Background(), root, digest)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	content, err := os.ReadFile(filepath.Join(path, "source"))
	if err != nil || string(content) != "first" {
		t.Fatalf("snapshot content: %q, %v", content, err)
	}
	if err := os.WriteFile(filepath.Join(store, digest+".tar"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := (ContentSnapshotter{Root: store}).Snapshot(context.Background(), root, digest); err == nil {
		t.Fatal("tampering was accepted")
	}
}

func TestContentSnapshotRejectsEscapesAndSymlinks(t *testing.T) {
	root, store, outside := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	for _, selected := range []string{"../secret", "link/secret", "."} {
		if _, err := CreateContentSnapshot(context.Background(), root, store, []string{selected}); err == nil {
			t.Fatalf("accepted %q", selected)
		}
	}
}
