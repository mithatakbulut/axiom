package verification

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// This opt-in test must run only on a disposable Linux host configured with a
// rootless Docker daemon and a locally available, digest-pinned test image.
func TestRootlessContainerIsolationMaliciousFixture(t *testing.T) {
	if os.Getenv("VERITY_ROOTLESS_DOCKER_TESTS") != "1" {
		t.Skip("set VERITY_ROOTLESS_DOCKER_TESTS=1 to run against a real rootless Docker daemon")
	}
	if runtime.GOOS != "linux" {
		t.Skip("rootless Docker integration requires Linux")
	}
	image := os.Getenv("VERITY_TEST_CONTAINER_IMAGE")
	if !validContainerImage(image) {
		t.Fatal("VERITY_TEST_CONTAINER_IMAGE must be pinned by sha256 digest")
	}
	hostHome := t.TempDir()
	secretPath := filepath.Join(hostHome, ".verity-host-home-secret")
	if err := os.WriteFile(secretPath, []byte("host-home-sentinel"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", hostHome)
	t.Setenv("VERITY_ISOLATION_SENTINEL_SECRET", "must-not-enter-container")
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("../../testdata/execution-integrity/create_fixture.py")
	if err != nil {
		t.Fatal(err)
	}
	repository := filepath.Join(t.TempDir(), "fixture")
	if output, err := exec.Command(python, script, repository).CombinedOutput(); err != nil {
		t.Fatalf("generate malicious fixture: %v\n%s", err, output)
	}
	resolve := exec.Command(git, "-C", repository, "rev-parse", "HEAD")
	data, err := resolve.Output()
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.TrimSpace(string(data))
	plan := Plan{
		CandidateSHA:    sha,
		TrustLevel:      TrustUntrusted,
		IsolationPolicy: IsolationRootlessContainer,
		ContainerImage:  image,
		Checks: []PlannedCheck{{
			ID: "malicious-isolation-probe", Name: "malicious isolation probe",
			Argv: []string{"python3", "malicious/probe.py", secretPath}, Required: true, Timeout: 30 * time.Second,
		}},
	}
	runner := ContainerRunner{RuntimeBinary: os.Getenv("VERITY_DOCKER_BINARY")}
	results, err := runner.Execute(context.Background(), repository, plan, true)
	if err != nil || len(results) != 1 || results[0].Outcome != Passed {
		t.Fatalf("rootless isolation probe = %#v, %v", results, err)
	}
	var observed map[string]any
	if err = json.Unmarshal([]byte(results[0].Output), &observed); err != nil {
		t.Fatalf("decode isolation probe output %q: %v", results[0].Output, err)
	}
	for key, want := range map[string]any{
		"host_path_write": "blocked", "symlink_escape": "blocked", "loopback": "reachable",
		"external_egress": "blocked", "interfaces": []any{"lo"},
		"credential_env": "absent", "host_home_secret": "absent", "docker_socket": "absent",
	} {
		got, ok := observed[key]
		if !ok || !equalProbeValue(got, want) {
			t.Errorf("isolation probe %s = %#v, want %#v", key, got, want)
		}
	}
	if spawned, ok := observed["pid_spawned"].(float64); ok && spawned >= 200 {
		t.Errorf("container spawned %.0f blocked children; PID cap was not observed", spawned)
	}
}

func equalProbeValue(got, want any) bool {
	switch expected := want.(type) {
	case []any:
		actual, ok := got.([]any)
		if !ok || len(actual) != len(expected) {
			return false
		}
		for i := range expected {
			if actual[i] != expected[i] {
				return false
			}
		}
		return true
	default:
		return got == want
	}
}
