package verification

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestContainerRunnerBlocksWhenRuntimeIsUnavailable(t *testing.T) {
	dir := t.TempDir()
	candidateDir := filepath.Join(dir, "candidate")
	if err := os.Mkdir(candidateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	plan := Plan{
		CandidateSHA:    testSHA(),
		IsolationPolicy: IsolationRootlessContainer,
		ContainerImage:  "checks@sha256:" + strings.Repeat("a", 64),
		Checks:          []PlannedCheck{{ID: "check", Name: "check", Argv: []string{"true"}, Required: true, Timeout: time.Second}},
	}
	runner := ContainerRunner{
		RuntimeBinary: filepath.Join(dir, "missing-docker"),
		Snapshotter:   staticSnapshotter(candidateDir),
		hostOS:        "linux",
		hostUID:       1000,
	}
	results, err := runner.Execute(context.Background(), dir, plan, true)
	if err != nil || len(results) != 1 || results[0].Outcome != Blocked || !strings.Contains(results[0].Diagnostic, "container runtime unavailable") {
		t.Fatalf("unavailable runtime result = %#v, %v; want explicit BLOCKED", results, err)
	}
}

func TestContainerRunnerRemovesContainerAfterTimeout(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "runtime.log")
	quotedLog := "'" + strings.ReplaceAll(logPath, "'", "'\\''") + "'"
	runtimePath := filepath.Join(dir, "docker")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = info ]; then echo '[\"name=rootless\"]'; exit 0; fi\n" +
		"printf '%s\\n' \"$1\" >> " + quotedLog + "\n" +
		"if [ \"$1\" = run ]; then exec sleep 2; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(runtimePath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	candidateDir := filepath.Join(dir, "candidate")
	if err := os.Mkdir(candidateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	plan := Plan{
		CandidateSHA:    testSHA(),
		IsolationPolicy: IsolationRootlessContainer,
		ContainerImage:  "checks@sha256:" + strings.Repeat("a", 64),
		Checks:          []PlannedCheck{{ID: "timeout", Name: "timeout", Argv: []string{"true"}, Required: true, Timeout: 100 * time.Millisecond}},
	}
	runner := ContainerRunner{RuntimeBinary: runtimePath, Snapshotter: staticSnapshotter(candidateDir), hostOS: "linux", hostUID: 1000}
	results, err := runner.Execute(context.Background(), dir, plan, true)
	if err != nil || len(results) != 1 || results[0].Outcome != TimedOut {
		t.Fatalf("timed out container result = %#v, %v", results, err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "run") || !strings.Contains(string(data), "rm") {
		t.Fatalf("timeout did not request container cleanup: %q", data)
	}
}
