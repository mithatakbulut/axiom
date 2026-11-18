# GitHub integration

This integration is optional for the macOS-local product target. Its worker
executes repository code and requires the Linux rootless-container policy
described below; do not enable it for unattended PRs until that isolation has
been validated on the deployment host.

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

## Self-hosted pull request worker

`verity github serve owner/repo 0.0.0.0:8080` runs a bounded HTTP receiver;
`verity github worker owner/repo loop` drains that repository's durable SQLite
queue. `worker once` processes at most one job. The webhook secret is supplied
as `VERITY_GITHUB_WEBHOOK_SECRET`; `VERITY_GITHUB_INSTALLATION_ID` optionally
restricts the receiver to one app installation before it queues PR events. The
GitHub API token is supplied as
`VERITY_GITHUB_TOKEN`, outside project configuration. A worker uses the local
`.verity/verification.yaml` as trusted policy and requires
`trust_level: untrusted`, `isolation: rootless_container_no_network`, and a
digest-pinned image. It fetches the PR ref into a temporary bare repository,
checks the exact SHA, verifies through the container runner, attaches evidence,
validates the proof bundle and artifacts, checks the current PR head, then
publishes the exact-SHA Check Run. Missing isolation produces BLOCKED and can
never become success. A `closed` event cancels pending and leased jobs;
newer head events supersede older work. Workers should be scoped to one
explicit `owner/repo` value.

Git fetch credentials are passed only to the trusted Git subprocess through
its environment and are not inherited by verification commands. The worker
accepts either a personal access token (`VERITY_GITHUB_TOKEN`) or a GitHub App
identity (`VERITY_GITHUB_APP_ID` and `VERITY_GITHUB_APP_PRIVATE_KEY_PATH`),
but not both. App private keys stay in an operator-managed file outside the
repository. The worker preserves the installation ID from signed pull-request
webhook payloads, discovers the installation for its configured `owner/repo`,
and rejects an event whose installation ID does not match before it publishes
progress or fetches code. It mints a repository-scoped installation token and
refreshes it before expiry. User-authorized manual retrigger still requires a personal
access token. By default, the worker fetches from `https://github.com`. For GitHub Enterprise, set
`VERITY_GITHUB_API_URL` to its API base and `VERITY_GITHUB_GIT_URL` to its HTTPS
Git base, for example `https://git.example.test/enterprise`; the Git base must
not include credentials, query parameters, or fragments. Credentials are
scoped to the configured Git base path. The completed Check Run includes the
proof ID and the exact `verity proof export <verification-id>
proof-<verification-id>.json` command. Run it on the self-hosted worker to
export the validated bundle; this service does not host a public proof URL.
Real GitHub deployment and Linux rootless-container runs remain required
before this service can be considered production ready.

## Preview provider

`integrations/preview.Netlify` starts a Netlify build for the branch resolved
from the requested full commit SHA, and rejects a returned deployment whose
`commit_ref` differs from that SHA. The URL resolver requires HTTPS and can
restrict preview hostnames. Credentials are injected at runtime. Provider
requests use timeouts and permission failures are surfaced. The Netlify client
currently starts and associates preview builds; cleanup and provider-specific
smoke verification remain caller responsibilities.

The current worker orchestrates PR head verification and exact-SHA checks. Push
and Actions events remain normalization/client capabilities without equivalent
application workflows; deployment and artifact ingestion remain outside the
worker.

An authorized maintainer can explicitly queue a new run for the current PR
head with:

```sh
verity github retrigger owner/repo 123
```

The command uses `VERITY_GITHUB_TOKEN` to identify the authenticated user,
requires repository `write`, `maintain`, or `admin` permission, and fetches the
live head SHA from GitHub before enqueueing. It records the actor in the durable
job payload, creates a unique run for that manual delivery, and refuses to
queue another run while the same PR head already has a pending or leased job.
The usual worker still performs verification and rechecks the PR head before
publication. Replaying the same delivery is idempotent.
