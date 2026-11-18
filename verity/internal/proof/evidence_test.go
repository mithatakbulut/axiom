package proof

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"verity/internal/candidate"
	vigilbridge "verity/internal/integrations/vigil"
	"verity/internal/verification"
)

func TestNewEvidenceBindsContentAndCandidate(t *testing.T) {
	content := []byte("verification output")
	candidate := hex.EncodeToString(make([]byte, 20))
	e, err := NewEvidence("ev-1", "wf-1", candidate, "check-log", "runner:test", time.Now(), content)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(content)
	if e.ContentSHA256 != hex.EncodeToString(want[:]) {
		t.Fatalf("content digest = %s, want %x", e.ContentSHA256, want)
	}
	if e.CandidateSHA != candidate {
		t.Fatalf("candidate = %q, want %q", e.CandidateSHA, candidate)
	}
}

func TestVersionTwoBundleRequiresAndBindsTypedCandidate(t *testing.T) {
	digest := hex.EncodeToString(make([]byte, 32))
	identity := candidate.Identity{SchemaVersion: 1, Kind: candidate.ContentSource, ID: digest, Repository: "file:///workspace", CapturedAt: time.Now().UTC(), Provenance: "verification:test"}
	run := verification.Run{ID: "run", SchemaVersion: verification.CurrentRunSchemaVersion, WorkflowID: "wf", Candidate: identity, CandidateKind: candidate.ContentSource, CandidateSHA: digest, CandidateRepository: identity.Repository, IsolationPolicy: verification.IsolationLocalProcess}
	bundle := Bundle{SchemaVersion: CurrentBundleSchemaVersion, WorkflowID: "wf", Candidate: identity, CandidateKind: candidate.ContentSource, CandidateSHA: digest, CandidateRepository: identity.Repository, IsolationPolicy: verification.IsolationLocalProcess, Verification: run}
	if err := bundle.Validate(); err != nil {
		t.Fatalf("valid typed proof bundle rejected: %v", err)
	}
	bundle.Candidate.ID = strings.Repeat("1", 64)
	if err := bundle.Validate(); err == nil {
		t.Fatal("accepted proof bundle with a candidate identity differing from its verification")
	}
	bundle.Candidate = candidate.Identity{}
	if err := bundle.Validate(); err == nil {
		t.Fatal("accepted version 2 proof bundle without a typed candidate")
	}
}

func TestLocalArtifactStoreConcurrentPutPublishesCompleteObject(t *testing.T) {
	s := LocalArtifactStore{Root: t.TempDir()}
	data := []byte("concurrent proof payload")
	const writers = 24
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	digests := make(chan string, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := s.Put(context.Background(), data)
			if err != nil {
				errs <- err
				return
			}
			digests <- d
		}()
	}
	wg.Wait()
	close(errs)
	close(digests)
	for err := range errs {
		t.Errorf("concurrent put: %v", err)
	}
	var want string
	for d := range digests {
		if want == "" {
			want = d
		}
		if d != want {
			t.Errorf("digest changed: %s != %s", d, want)
		}
	}
	got, err := s.Get(context.Background(), want)
	if err != nil || string(got) != string(data) {
		t.Fatalf("read after concurrent publish: %q %v", got, err)
	}
}

func BenchmarkArtifactStore(b *testing.B) {
	store := LocalArtifactStore{Root: b.TempDir()}
	ctx := context.Background()
	data := make([]byte, 64<<10)
	digest, err := store.Put(ctx, data)
	if err != nil {
		b.Fatal(err)
	}
	b.Run("PutDeduplicated64KiB", func(b *testing.B) {
		b.SetBytes(int64(len(data)))
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := store.Put(ctx, data); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("Get64KiB", func(b *testing.B) {
		b.SetBytes(int64(len(data)))
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := store.Get(ctx, digest); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func TestLocalArtifactStoreVerifiesContentAndDeduplicates(t *testing.T) {
	s := LocalArtifactStore{Root: t.TempDir()}
	ctx := context.Background()
	data := []byte("proof")
	d1, err := s.Put(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := s.Put(ctx, data)
	if err != nil || d1 != d2 {
		t.Fatalf("dedup: %q %q %v", d1, d2, err)
	}
	got, err := s.Get(ctx, d1)
	if err != nil || string(got) != string(data) {
		t.Fatalf("get: %q %v", got, err)
	}
	path, _ := s.path(d1)
	if err = os.WriteFile(path, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(ctx, d1); err != ErrDigestMismatch {
		t.Fatalf("tamper error = %v", err)
	}
}

func TestBundleRequiresEvidenceForPassingRequiredChecks(t *testing.T) {
	sha := hex.EncodeToString(make([]byte, 20))
	run := verification.Run{ID: "vr1", SchemaVersion: 1, WorkflowID: "wf1", CandidateSHA: sha, Checks: []verification.CheckResult{{ID: "test", Name: "test", Required: true, Outcome: verification.Passed, EvidenceIDs: []string{"ev1"}}}}
	b := Bundle{SchemaVersion: 1, WorkflowID: "wf1", CandidateSHA: sha, Verification: run}
	if err := b.Validate(); err == nil {
		t.Fatal("expected missing evidence rejection")
	}
	e, _ := NewEvidence("ev1", "wf1", sha, "check-log", "runner", time.Now(), []byte("ok"))
	b.Evidence = []Evidence{e}
	b.Artifacts = []ArtifactReference{{EvidenceID: "ev1", Digest: e.ContentSHA256, Size: e.Size}}
	first, err := b.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	second, err := b.Marshal()
	if err != nil || string(first) != string(second) {
		t.Fatalf("export not reproducible: %v", err)
	}
}

func TestBundleValidatesCandidateBoundVigilReferences(t *testing.T) {
	sha := "0123456789abcdef0123456789abcdef01234567"
	run := verification.Run{ID: "vr1", SchemaVersion: 1, WorkflowID: "wf1", CandidateSHA: sha, State: verification.StateBlocked}
	b := Bundle{SchemaVersion: 1, WorkflowID: "wf1", CandidateSHA: sha, Verification: run}
	b.VigilLinks = []vigilbridge.Link{{SchemaVersion: 1, WorkflowID: "wf1", TaskID: "task1", AttemptID: "att1", CorrelationID: "corr1", BaseSHA: sha, CandidateSHA: sha, SessionID: "sess1", Status: vigilbridge.CandidateReady, VigilVersion: "0.1.0", CapturedAt: time.Now()}}
	if err := b.Validate(); err != nil {
		t.Fatal(err)
	}
	b.VigilLinks[0].CandidateSHA = "1123456789abcdef0123456789abcdef01234567"
	if err := b.Validate(); err == nil {
		t.Fatal("proof accepted Vigil reference for another candidate")
	}
}

func TestBundleRequiresExactlyOneArtifactPerEvidence(t *testing.T) {
	sha := hex.EncodeToString(make([]byte, 20))
	e, err := NewEvidence("e", "wf", sha, "check", "runner", time.Now(), []byte("output"))
	if err != nil {
		t.Fatal(err)
	}
	b := Bundle{SchemaVersion: 1, WorkflowID: "wf", CandidateSHA: sha, Verification: verification.Run{ID: "run", SchemaVersion: 1, WorkflowID: "wf", CandidateSHA: sha}, Evidence: []Evidence{e}}
	if err := b.VerifyArtifacts(context.Background(), LocalArtifactStore{Root: t.TempDir()}); err == nil {
		t.Fatal("missing artifact accepted")
	}
	ref := ArtifactReference{EvidenceID: e.ID, Digest: e.ContentSHA256, Size: e.Size}
	b.Artifacts = []ArtifactReference{ref, ref}
	if err := b.Validate(); err == nil {
		t.Fatal("duplicate artifact accepted")
	}
}

func TestInvalidCapturePreservesSharedArtifact(t *testing.T) {
	ctx := context.Background()
	store := LocalArtifactStore{Root: t.TempDir()}
	data := []byte("shared")
	digest, err := store.Put(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Capture(ctx, store, "", "wf", hex.EncodeToString(make([]byte, 20)), "check", "runner", time.Now(), data); err == nil {
		t.Fatal("invalid evidence accepted")
	}
	if _, err := store.Get(ctx, digest); err != nil {
		t.Fatalf("shared artifact lost: %v", err)
	}
}
