package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"verity/internal/proof"
	"verity/internal/store/sqlite"
)

func proofCommand(args []string, opts options) error {
	if len(args) < 3 {
		return errors.New("usage: verity proof show|export <verification-run-id> [output.json] [config-path]")
	}
	path := opts.configPath()
	if opts.config == "" && args[1] == "show" && len(args) > 3 {
		path = args[3]
	} else if opts.config == "" && len(args) > 4 {
		path = args[4]
	}
	c, err := loadConfig(path)
	if err != nil {
		return err
	}
	dbPath := c.Database
	if !filepath.IsAbs(dbPath) {
		dbPath = filepath.Join(filepath.Dir(filepath.Dir(path)), dbPath)
	}
	db, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx := context.Background()
	run, err := db.Verification(ctx, args[2])
	if err != nil {
		return err
	}
	if args[1] == "show" {
		if opts.json {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(run)
		}
		fmt.Printf("Verification: %s\nWorkflow: %s\nCandidate: %s\nState: %s\nChecks: %d\n", run.ID, run.WorkflowID, run.CandidateSHA, run.State, len(run.Checks))
		return nil
	}
	if args[1] != "export" {
		return errors.New("proof command must be show or export")
	}
	evidence, err := db.Evidence(ctx, run.WorkflowID, run.CandidateSHA)
	if err != nil {
		return err
	}
	audit, err := db.ProofAudit(ctx, run.WorkflowID)
	if err != nil {
		return err
	}
	refs := make([]proof.ArtifactReference, 0, len(evidence))
	for _, e := range evidence {
		refs = append(refs, proof.ArtifactReference{EvidenceID: e.ID, Digest: e.ContentSHA256, Size: e.Size})
	}
	vigilLinks, err := db.VigilLinks(ctx, run.WorkflowID, run.CandidateSHA)
	if err != nil {
		return err
	}
	bundle := proof.Bundle{SchemaVersion: 1, WorkflowID: run.WorkflowID, CandidateSHA: run.CandidateSHA, Verification: run, Evidence: evidence, Artifacts: refs, Audit: audit, VigilLinks: vigilLinks}
	data, err := bundle.Marshal()
	if err != nil {
		return err
	}
	output := "proof-" + run.ID + ".json"
	if len(args) > 3 {
		output = args[3]
	}
	if err := os.WriteFile(output, data, 0600); err != nil {
		return err
	}
	fmt.Printf("Proof exported to %s\n", output)
	return nil
}
