package main

import (
	"context"
	"os"

	"verity/internal/execution"
	"verity/internal/orchestration"
	"verity/internal/store/sqlite"
	"verity/internal/verification"
)

func planDrive(ctx context.Context, root, dataDir, model string, db *sqlite.Store, id string, provider execution.AgentRuntime) error {
	return orchestration.Drive(ctx, root, dataDir, model, db, id, provider, os.Stdout)
}

func planEvidence(ctx context.Context, db *sqlite.Store, dataDir string, result verification.Run) error {
	return orchestration.EvidenceValid(ctx, db, dataDir, result)
}
