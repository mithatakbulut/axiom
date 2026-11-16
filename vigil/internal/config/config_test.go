package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("version: 1\nmode: observe\nstorage:\n  retention_days: 7\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil || c.Storage.RetentionDays != 7 || c.Storage.Path == "" {
		t.Fatalf("config: %+v %v", c, err)
	}
	if err = os.WriteFile(p, []byte("mode: invalid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Load(p); err == nil {
		t.Fatal("accepted unsupported mode")
	}
}

func TestRelativeStoragePathUsesConfigDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("storage:\n  path: events.db\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil || c.Storage.Path != filepath.Join(dir, "events.db") {
		t.Fatalf("storage path: %q, %v", c.Storage.Path, err)
	}
}
