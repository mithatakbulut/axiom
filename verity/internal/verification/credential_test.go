package verification

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func credentialManifest() string {
	return "version: 1\ntrust_level: untrusted\nisolation: rootless_container_no_network\ncontainer_image: checks@sha256:" + strings.Repeat("a", 64) + "\nnetwork_allowlist:\n  - api.example.com:443\n  - assets.example.com:443\nbrowser:\n  adapter: playwright\n  base_url: https://api.example.com/app\n  scenarios:\n    - id: authenticated\n      description: authenticated smoke\n      required: true\n      spec: playwright/auth.spec.js\n      expose_to_test_code: true\n      test_credentials:\n        - name: VERITY_TEST_TOKEN\n          source_env: VERITY_TEST_API_TOKEN\n          expires_at_env: VERITY_TEST_API_TOKEN_EXPIRES\n          ttl_seconds: 300\n"
}

type countingSnapshotter struct{ calls int }

func (s *countingSnapshotter) Snapshot(context.Context, string, string) (string, func(), error) {
	s.calls++
	return "", func() {}, nil
}

func TestMissingCredentialBlocksBrowserBeforeCandidateMaterialization(t *testing.T) {
	root := t.TempDir()
	runtimePath := filepath.Join(root, "docker")
	logPath := filepath.Join(root, "docker.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + logPath + "'\nif [ \"$1\" = info ]; then echo '[\"name=rootless\"]'; exit 0; fi\n"
	if err := os.WriteFile(runtimePath, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VERITY_TEST_API_TOKEN", "")
	t.Setenv("VERITY_TEST_API_TOKEN_EXPIRES", "")
	plan, err := ParseManifest([]byte(credentialManifest()))
	if err != nil {
		t.Fatal(err)
	}
	verificationPlan, err := plan.Plan(testSHA(), Standard)
	if err != nil {
		t.Fatal(err)
	}
	snapshotter := &countingSnapshotter{}
	checks, err := (ContainerRunner{RuntimeBinary: runtimePath, RelayBinary: os.Args[0], Snapshotter: snapshotter, hostOS: "linux", hostUID: 1000}).Execute(context.Background(), "repo", verificationPlan, true)
	if err != nil || len(checks) != 1 || checks[0].Outcome != Blocked || !strings.Contains(checks[0].Diagnostic, "VERITY_TEST_API_TOKEN") {
		t.Fatalf("missing credential result=%#v err=%v", checks, err)
	}
	if snapshotter.calls != 0 {
		t.Fatalf("candidate materialized without credential: %d", snapshotter.calls)
	}
	log, err := os.ReadFile(logPath)
	if err != nil || strings.Contains(string(log), "run") {
		t.Fatalf("container launched for missing credential: %s %v", log, err)
	}
}

func TestManifestBindsShortLivedCredentialsOnlyToBrowserDestination(t *testing.T) {
	manifest, err := ParseManifest([]byte(credentialManifest()))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := manifest.Plan(testSHA(), Standard)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Checks) != 1 || len(plan.Checks[0].Credentials) != 1 || plan.Checks[0].NetworkAllowlist[0] != "api.example.com:443" {
		t.Fatalf("credential policy was not check-scoped: %#v", plan.Checks)
	}
	data, err := json.Marshal(plan)
	if err != nil || strings.Contains(string(data), "secret-value") {
		t.Fatalf("plan leaked credential value: %s %v", data, err)
	}
	for _, mutate := range []struct{ name, old, replacement string }{
		{"not explicit", "expose_to_test_code: true", "expose_to_test_code: false"},
		{"not HTTPS", "https://api.example.com/app", "http://api.example.com/app"},
		{"not allowlisted", "  - api.example.com:443\n", "  - other.example.com:443\n"},
		{"unscoped source", "VERITY_TEST_API_TOKEN", "GITHUB_TOKEN"},
		{"too long", "ttl_seconds: 300", "ttl_seconds: 601"},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			if _, err := ParseManifest([]byte(strings.Replace(credentialManifest(), mutate.old, mutate.replacement, 1))); err == nil {
				t.Fatal("unsafe test credential policy accepted")
			}
		})
	}
}

func TestResolveCredentialsRequiresExpiringHostValueAndRedactsIt(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	expires := now.Add(2 * time.Minute).Format(time.RFC3339)
	t.Setenv("VERITY_TEST_API_TOKEN", "secret-value-123")
	t.Setenv("VERITY_TEST_API_TOKEN_EXPIRES", expires)
	check := PlannedCheck{Adapter: "playwright", BaseURL: "https://api.example.com", Timeout: 5 * time.Minute, Credentials: []PlannedCredential{{Name: "VERITY_TEST_TOKEN", SourceEnv: "VERITY_TEST_API_TOKEN", ExpiresAtEnv: "VERITY_TEST_API_TOKEN_EXPIRES", TTL: 5 * time.Minute}}}
	resolved, timeout, err := resolveCredentials(check, now)
	if err != nil || timeout != 2*time.Minute || len(resolved) != 1 {
		t.Fatalf("resolve=(%#v,%s,%v)", resolved, timeout, err)
	}
	childEnv := credentialsEnvironment([]string{"PATH=/bin"}, resolved)
	if len(childEnv) != 2 || childEnv[1] != "VERITY_TEST_TOKEN=secret-value-123" {
		t.Fatalf("credential environment is not scoped: %#v", childEnv)
	}
	if got := redactCredentialValues("output secret-value-123", resolved); got != "output [REDACTED TEST CREDENTIAL]" {
		t.Fatalf("credential output not redacted: %q", got)
	}
	t.Setenv("VERITY_TEST_API_TOKEN_EXPIRES", now.Add(6*time.Minute).Format(time.RFC3339))
	if _, _, err := resolveCredentials(check, now); err == nil {
		t.Fatal("credential beyond max lifetime accepted")
	}
	t.Setenv("VERITY_TEST_API_TOKEN_EXPIRES", now.Add(-time.Second).Format(time.RFC3339))
	if _, _, err := resolveCredentials(check, now); err == nil {
		t.Fatal("expired credential accepted")
	}
}

func TestVerificationRunRetainsCredentialNamesAndCheckScopedEgress(t *testing.T) {
	t.Setenv("VERITY_TEST_API_TOKEN", "")
	t.Setenv("VERITY_TEST_API_TOKEN_EXPIRES", "")
	manifest, err := ParseManifest([]byte(credentialManifest()))
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("1", 64)
	run, err := (Engine{Runner: CommandRunner{}, CandidateKind: CandidateContentSource, CandidateRepository: "credential-fixture"}).Verify(context.Background(), t.TempDir(), "run", "workflow", "", manifest, Standard, sha, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Checks) != 1 || len(run.Checks[0].CredentialBindings) != 1 || run.Checks[0].CredentialBindings[0].Name != "VERITY_TEST_TOKEN" || len(run.Checks[0].NetworkAllowlist) != 1 || run.Checks[0].NetworkAllowlist[0] != "api.example.com:443" {
		t.Fatalf("verification proof lost credential policy: %#v", run.Checks)
	}
	encoded, err := json.Marshal(run)
	if err != nil || strings.Contains(string(encoded), "secret-value") {
		t.Fatalf("run serialized credential material: %s %v", encoded, err)
	}
}
