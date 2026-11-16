# GitHub integration (Phase 7)

Verity exposes an HTTP handler for `pull_request`, `push`, and `workflow_run`
webhooks. Configure a strong webhook secret and grant the GitHub App only the
repository permissions required for its enabled features: Checks read/write,
Pull requests read/write for the marked summary comment, Actions read for run
and artifact metadata/download, and Metadata read. The handler verifies
`X-Hub-Signature-256` before parsing, caps payloads at 1 MiB, accepts only
supported actions, and persists `X-GitHub-Delivery` IDs in SQLite. Failed event
processing releases the delivery claim so GitHub retries; successful duplicate
deliveries return accepted without dispatching again.

`integrations/github.Client` publishes a named Check Run to an exact full
commit SHA and updates an existing check with the same external ID. Completed
checks require an explicit GitHub conclusion; callers must derive it from a
validated Verity verification/proof result. It can upsert one marked PR summary
comment and list workflow runs/artifacts for a SHA. Artifact downloads are
limited to 50 MiB and checked against GitHub size/digest metadata when present.
Permission failures and exhausted rate limits have distinct errors. Requests
respect cancellation and retry boundedly for rate limiting and server errors.

The integration accepts no token from webhook bodies and never logs request
authorization headers. Store the API token outside project configuration and
inject it when constructing the client. Enterprise GitHub API URLs can be
provided with `Client.BaseURL`.

## Preview provider

`integrations/preview.Netlify` starts a Netlify build for the branch resolved
from the requested full commit SHA, and rejects a returned deployment whose
`commit_ref` differs from that SHA. The URL resolver requires HTTPS and can
restrict preview hostnames. Credentials are injected at runtime. Provider
requests use timeouts and permission failures are surfaced. The Netlify client
currently starts and associates preview builds; cleanup and provider-specific
smoke verification remain caller responsibilities.

Webhook events are normalized inputs for an application-level event handler.
That handler must associate pull requests, commits, workflow runs and
verification records, and invalidate prior results when a new head SHA arrives.
The integration package does not create lifecycle workflows on its own because
the repository has no workflow-creation application service yet.
