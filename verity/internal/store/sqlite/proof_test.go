package sqlite

import (
	"context"
	"testing"
	"time"

	vigilbridge "verity/internal/integrations/vigil"
	"verity/internal/proof"
)

func TestProofRecordsPersistAndAuditIsAppendOnly(t *testing.T) {
	s, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	sha := "0123456789abcdef0123456789abcdef01234567"
	e, err := proof.NewEvidence("ev1", "wf1", sha, "check-output", "runner:test", time.Now(), []byte("output"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SaveEvidence(ctx, e); err != nil {
		t.Fatal(err)
	}
	got, err := s.Evidence(ctx, "wf1", sha)
	if err != nil || len(got) != 1 || got[0].ContentSHA256 != e.ContentSHA256 {
		t.Fatalf("evidence: %#v %v", got, err)
	}
	a := proof.AuditEvent{ID: "audit1", WorkflowID: "wf1", Sequence: 1, Type: "verification_saved", CandidateSHA: sha, Actor: "system", At: time.Now()}
	if err = s.AppendProofAudit(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err = s.AppendProofAudit(ctx, a); err == nil {
		t.Fatal("expected sequence conflict")
	}
	if _, err = s.db.ExecContext(ctx, "DELETE FROM proof_audit WHERE workflow_id=?", "wf1"); err == nil {
		t.Fatal("audit delete unexpectedly succeeded")
	}
}

func TestVigilLinksPersistAndAreAppendOnly(t *testing.T) {
	s, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	link := vigilbridge.Link{SchemaVersion: 1, WorkflowID: "wf", TaskID: "task", AttemptID: "attempt", CorrelationID: "corr", BaseSHA: "0123456789abcdef0123456789abcdef01234567", CandidateSHA: "1123456789abcdef0123456789abcdef01234567", SessionID: "sess", Status: vigilbridge.CandidateReady, VigilVersion: "0.1.0", CapturedAt: time.Now().UTC()}
	if err := s.SaveVigilLink(ctx, link); err != nil {
		t.Fatal(err)
	}
	got, err := s.VigilLinks(ctx, "wf", link.CandidateSHA)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SessionID != "sess" || got[0].AttemptID != "attempt" {
		t.Fatalf("unexpected links: %+v", got)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE vigil_links SET body='{}' WHERE attempt_id='attempt'`); err == nil {
		t.Fatal("Vigil link update was allowed")
	}
	if _, err = s.db.ExecContext(ctx, `DELETE FROM vigil_links WHERE attempt_id='attempt'`); err == nil {
		t.Fatal("Vigil link deletion was allowed")
	}
}
