# Atomic local pull-request trigger

Status: accepted for the local CLI path, 2026-11-16.

A separately committed delivery claim can survive a crash before workflow creation. The pull-request trigger therefore commits the delivery ID, exact repository/PR/head-SHA association, definition and initial workflow together. The run ID is derived from a SHA-256 hash of the association. Separate deliveries for the same association reuse the run; another SHA gets a new run. Existing claims without a matching association fail closed for operator reconciliation. An explicit repository selection limits what the configured signed webhook may create.

The CLI processes one forwarded signed body; it is not an HTTP server or autonomous scheduler. This preserves the local-first boundary while providing an integration point to an existing GitHub webhook receiver. No completion or verification is inferred from a delivery.
