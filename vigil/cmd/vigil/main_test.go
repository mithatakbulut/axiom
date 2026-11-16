package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHookFailsOpenOnInvalidConfig(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(p, []byte("version: 99"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := run([]string{"hook", "--config", p}); got != 0 {
		t.Fatalf("hook exit %d", got)
	}
	if got := run([]string{"doctor", "--config", p}); got == 0 {
		t.Fatal("doctor should fail")
	}
}
