# GitHub pull-request delivery to a local workflow

Verity can process one forwarded, signed `pull_request` delivery without hosting a web server. The forwarder must preserve the exact raw body, delivery ID and `X-Hub-Signature-256` header and retry failures. Configure a GitHub webhook secret outside the project, pass it through `VERITY_GITHUB_WEBHOOK_SECRET`, and invoke:

```sh
verity --config /path/to/project/.verity/config.yaml github receive owner/repo DELIVERY_ID sha256=SIGNATURE < raw-payload.json
verity --config /path/to/project/.verity/config.yaml github workflows owner/repo
```

The receiver verifies the signature before interpreting the payload and accepts only the selected repository and `opened`, `reopened`, `synchronize`, or `ready_for_review` pull-request actions. Unsupported events/actions are rejected; filter them in the forwarder. The JSON listing maps PR numbers and exact head SHAs to workflow IDs. A deterministic ID and a single SQLite transaction couple the delivery, association, definition and READY workflow. A duplicate delivery or another delivery for the same PR and SHA reuses that workflow; a new SHA creates a new one. An old delivery claim without a corresponding workflow fails and requires operator reconciliation rather than silently acknowledging lost work.

A READY record does not fetch a repository, execute an agent, invalidate historical records, publish a GitHub Check, or verify a candidate. The exact SHA association keeps historical results identifiable; a consumer must explicitly select and verify the current SHA. This command does not provide a GitHub-facing HTTP endpoint, retry queue, authentication for a forwarder, or a complete scheduler. Existing separately configured GitHub client and lifecycle interfaces remain application responsibilities. Secret access and operating-system execution permissions remain the operator's responsibility.
