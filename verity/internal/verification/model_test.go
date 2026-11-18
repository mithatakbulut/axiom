package verification

import (
	"encoding/hex"
	"testing"
	"time"

	"verity/internal/candidate"
)

func TestRunRequiresImmutableCandidateAndKnownOutcomes(t *testing.T) {
	sha := hex.EncodeToString(make([]byte, 20))
	run := Run{
		ID: "run-1", SchemaVersion: 1, WorkflowID: "wf-1", CandidateSHA: sha,
		Checks: []CheckResult{{ID: "check-1", Name: "unit", Outcome: Passed}},
	}
	if err := run.Validate(); err != nil {
		t.Fatalf("valid run rejected: %v", err)
	}
	run.CandidateSHA = "main"
	if err := run.Validate(); err != ErrInvalidCandidate {
		t.Fatalf("mutable candidate error = %v, want %v", err, ErrInvalidCandidate)
	}
}

func TestVersionTwoRunRequiresTypedCandidateWhileLegacyRemainsReadable(t *testing.T) {
	sha := hex.EncodeToString(make([]byte, 20))
	legacy := Run{ID: "old", SchemaVersion: 1, WorkflowID: "wf", CandidateSHA: sha, Checks: []CheckResult{{ID: "c", Name: "check", Outcome: Passed}}}
	if err := legacy.Validate(); err != nil {
		t.Fatalf("legacy verification record no longer reads: %v", err)
	}
	legacy.SchemaVersion = CurrentRunSchemaVersion
	if err := legacy.Validate(); err == nil {
		t.Fatal("version 2 run accepted without typed candidate")
	}
}

func TestRunTypedCandidateMustMatchLegacyProjection(t *testing.T) {
	digest := hex.EncodeToString(make([]byte, 32))
	run := Run{
		ID: "run", SchemaVersion: CurrentRunSchemaVersion, WorkflowID: "wf", CandidateKind: CandidateContentSource,
		CandidateSHA: digest, CandidateRepository: "file:///workspace",
		IsolationPolicy: IsolationLocalProcess,
		Candidate:       candidate.Identity{SchemaVersion: 1, Kind: candidate.ContentSource, ID: digest, Repository: "file:///workspace", CapturedAt: time.Now().UTC(), Provenance: "verification:test"},
		Checks:          []CheckResult{{ID: "check", Name: "unit", Outcome: Passed}},
	}
	if err := run.Validate(); err != nil {
		t.Fatalf("valid typed candidate rejected: %v", err)
	}
	run.Candidate.ID = hex.EncodeToString(make([]byte, 32))[:62] + "01"
	if err := run.Validate(); err == nil {
		t.Fatal("accepted typed candidate inconsistent with candidate SHA")
	}
}

func TestManifestSupportsHookAndFullRunProfiles(t *testing.T) {
	m, err := ParseManifest([]byte(`version: 1
commands:
  fast:
    command: ['true']
    required: true
    timeout_seconds: 5
  broad:
    command: ['true']
    required: true
    timeout_seconds: 30
profiles:
  pre-commit: [fast]
  pre-push: [fast, broad]
  full: [fast, broad]
`))
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []Profile{PreCommit, PrePush, Full} {
		if _, err := m.Plan(hex.EncodeToString(make([]byte, 20)), profile); err != nil {
			t.Fatalf("profile %s rejected: %v", profile, err)
		}
	}
}

func TestBrowserBaseURLRejectsEmbeddedCredentials(t *testing.T) {
	for _, baseURL := range []string{
		"https://user:password@example.test/",
		"https://example.test/?access_token=secret",
		"https://example.test/?api_key=secret",
		"https://example.test/?client-secret=secret",
	} {
		manifest := "version: 1\nbrowser:\n  adapter: playwright\n  base_url: " + baseURL + "\n"
		if _, err := ParseManifest([]byte(manifest)); err == nil {
			t.Errorf("manifest accepted credential-bearing base URL %q", baseURL)
		}
	}
	if _, err := ParseManifest([]byte("version: 1\nbrowser:\n  adapter: playwright\n  base_url: https://example.test/app\n")); err != nil {
		t.Fatalf("credential-free base URL rejected: %v", err)
	}
}
