# GitHub pull-request delivery to a local workflow

Verity can process one forwarded, signed `pull_request` delivery without hosting a web server. The forwarder must preserve the exact raw body, delivery ID and `X-Hub-Signature-256` header and retry failures. Configure a GitHub webhook secret outside the project, pass it through `VERITY_GITHUB_WEBHOOK_SECRET`, and invoke:

```sh
verity --config /path/to/project/.verity/config.yaml github receive owner/repo DELIVERY_ID sha256=SIGNATURE < raw-payload.json
verity --config /path/to/project/.verity/config.yaml github workflows owner/repo
```

The receiver verifies the signature before interpreting the payload and accepts only the selected repository and `opened`, `reopened`, `synchronize`, `ready_for_review`, or `closed` pull-request actions. Unsupported events/actions are rejected; filter them in the forwarder. The JSON listing maps PR numbers and exact head SHAs to workflow IDs. A deterministic ID and a single SQLite transaction couple the delivery, association, definition and READY workflow/worker job. A duplicate delivery or another delivery for the same PR and SHA reuses that workflow; a new SHA creates a new one and supersedes older queued/running jobs. A close event cancels queued/running work. An old delivery claim without a corresponding workflow fails and requires operator reconciliation rather than silently acknowledging lost work.

A READY record alone does not prove anything. For the durable worker flow, run `verity github serve owner/repo 0.0.0.0:8080` and `verity github worker owner/repo loop`; see `github.md` for the trusted manifest, token, isolation, retry and proof limitations. `github receive` remains a local one-shot intake path. Secret access and operating-system execution permissions remain the operator's responsibility.
