# Execution integrity implementation log

## 2026-11-18

- Roadmap: `docs/execution-integrity-plan.md` (untracked before this work; preserve it).
- Started Verity conversation workflow `chat_56ab16e1896c1bbe4ad74e613b92adc9`; `spec` is running.
- Root `AGENTS.md` requires the in-conversation `verity chat` workflow and expressly forbids starting another Codex process. `verity plan start` launches managed Codex processes, so the persistent-plan runner cannot be started under that instruction. Continue with the mandated chat workflow; do not approve the final human gate.
- Initial investigation: verification currently represents candidate identity as an untyped `CandidateSHA`; `GitSnapshotter` archives commits and `ContentSnapshotter` captures selected paths. Neither captures the complete staged index. The existing snapshot unpacker rejects symlinks; the index implementation needs its own safe handling and explicit unsupported-entry checks.
- No roadmap implementation or validation has been completed yet. Next: finish specification, implement and test candidate contracts and exact-index capture, then proceed through all remaining phases. Run the root-required fresh Verity verification after substantive changes.

## 2026-11-18 — initial staged-index adapter

- `spec` finished and `implement` begun in Verity chat `chat_56ab16e1896c1bbe4ad74e613b92adc9`; implementation remains in progress.
- Added initial staged-tree capture and materialization in `verity/internal/verification/index_snapshot.go`. It copies the index before `git write-tree`, reads blobs without checkout filters, preserves executable bits and symlinks, rejects submodules and LFS pointers, and can recheck the staged tree and base.
- Added an initial integration test for staged versus unstaged bytes, index preservation, and changed index detection. Fresh checks: `go test ./internal/verification -run TestIndexSnapshotUsesStagedTree -count=1` and `go test ./internal/verification` passed.
- This is an incomplete foundation, not a usable pre-commit gate: it is not wired to the CLI, verification plan, proof, or hook. Edge cases, contracts, trusted policy, lifecycle, isolation, integrations, E2E, full verification, and documentation remain. Split-index and unborn-HEAD handling require investigation. No milestone is complete.

- Subsequent work: added split-index handling and a test for split-index preservation, executable files, and symlinks. Fresh focused index tests and root `make check` passed (both Go suites and Python tracker). The Verity `implement` step remains running; final Verity snapshot verification and human gate have not been reached.

## 2026-11-18 — staged-index verification CLI

- Confirmed `chat_56ab16e1896c1bbe4ad74e613b92adc9` is the active feature run: `spec` COMPLETED and `implement` RUNNING. `chat begin ... implement` correctly refused because that task was already active.
- Added `verity verify index plan|run`. The approval digest now binds workflow, candidate kind (`git_index`), complete tree identity, base HEAD, manifest, profile, and exact plan. The engine uses an index-aware current-candidate resolver before and after checks; verification executes from staged Git blobs and results record candidate kind/base alongside the tree in `candidate_sha`.
- Added the parsed manifest's SHA-256 digest to every verification result, closing the corresponding provenance field in the roadmap.
- Added an end-to-end staged-versus-unstaged CLI test, preserving the original Git index. Updated Verity quick-start/status and the roadmap progress record. Explicit unsupported outcomes remain for submodules, LFS pointers, and unmerged indexes. No automatic commit hook, OS sandbox, complete typed-candidate migration, or later roadmap phases are implemented.
- Fresh validation after manifest-digest addition: focused Verity CLI/verification tests passed; root `GOCACHE=/tmp/axiom-gocache make check` passed; the registered snapshot verification passed (`vr_b9059b9b81e85452e3d79972ab8cc207`); and `git diff --check` passed. The fresh workflow `chat_39454bf061574e140e7f3798f2b745d4` is `WAITING_APPROVAL` at the explicit human gate; it was not approved.

## 2026-11-18 — Phase 1 hardening continuation

- The Git index adapter now resolves canonical repository root and common directory, supports linked worktrees, detects unmerged entries, and recursively materializes the complete staged tree. Verification gives each check a fresh candidate copy and rejects unsupported isolation policies rather than falling back to host execution.
- Candidate kind is an enum in verification results; results include repository identity, base for index candidates, manifest digest, and isolation policy. Proof exports validate and preserve those fields. Added manifest profile names `pre-commit`, `pre-push`, and `full` without installing hooks.
- Added coverage for staged deletion/rename/addition, excluded untracked files, partial staging, nested and Unicode paths, split/linked worktrees, symlinks/modes, conflicts, submodules/LFS rejection, index mutation during verification, failed-capture cleanup, fresh-copy defense, proof provenance, and unsupported isolation.
- Latest focused package tests passed. Root and registered full snapshot verification are pending for this continuation; no human gate is approved.
- Trusted-policy follow-up: Git candidate verification now checks the active local config path and `.verity/verification.yaml` in the actual candidate tree. The exact config file digest is included in the plan approval. Canonical Git prefix handling covers macOS `/var`/`/private` temp-path aliases. Root `make check` and registered full snapshot verification passed (`vr_5fb5263703e91b2809f11c40ae949e96`).

## 2026-11-18 — Phase 2 local Git gates

- Started phase workflow `chat_ff1cc73f7886ba9d66ea5a4a4c472ba4`; spec and implementation are complete, verification passed, and the workflow is waiting at its human approval gate. No human approval was recorded.
- Added reversible `pre-commit` and `post-commit` hook management with `core.hooksPath` resolution, explicit chaining, original bytes/mode restoration, drift refusal, per-hook state, and no Git config mutation. Pre-commit runs the configured staged-tree profile and stores check evidence. Post-commit appends an audit binding only if commit tree, common repository directory, profile, candidate kind, and PASS result match.
- Tests cover staged bytes versus unstaged contents, failing checks, exact chained-hook restore, custom hook paths, drift refusal, post-commit exact-tree binding, and mismatch non-binding. Focused tests passed. Root `make check` passed. The fresh registered snapshot first failed because its isolated HOME could not resolve the Go module proxy; rerunning with network access passed (`vr_dc6e489e47a84bcd2be96fbac10711cd`).

## 2026-11-18 — Phase 3 initial fail-closed adapter

- Started workflow `chat_c9a442ebb0ca714a6124aae65b39a321`; spec is complete and implementation is active.
- Added explicit `trust_level: untrusted`, which requires `rootless_container_no_network`; the local runner refuses to execute such a plan. Container image must be digest-pinned, and trust/isolation/image provenance is recorded in verification runs and proof bundles.
- Added an initial Linux rootless Docker runner: no network, read-only root, no added capabilities, `no-new-privileges`, non-root user, CPU/memory/PID limits, bounded tmpfs, bounded stdin candidate archive, no host path mounts or inherited check environment, timeout cleanup, and explicit BLOCKED when runtime support is unavailable. A fake-runtime integration test verifies invocation flags; archive tests confirm symlinks are not followed.
- Focused verification/proof/lifecycle/CLI tests pass. A real Linux container runtime, malicious-container scenarios, and host-side temporary snapshot quota remain unverified; Phase 3 is not complete. Explicit network allowlists are also not implemented.
- Root `make check` passed. The registered snapshot run initially failed on isolated-home module DNS, then passed with network access (`vr_6d9a8b742499e632d7c3248e8713162a`). Its workflow remains at the unapproved human gate.

## 2026-11-18 — Phase 4 orchestration extraction

- Started workflow `chat_d3ebb049d932032d09ef6b17dc4a4365`; implementation is active.
- Extracted the durable plan driver, recovery path, evidence validation, and shared plan lock into `verity/internal/orchestration`. The CLI retains only a thin adapter and passes its output writer to the service. Existing workflow tests still exercise retries, UNKNOWN recovery, verification/evidence gating, and attributed human-review decisions.
- At this point in the work, agent attempts still received the project root; this limitation was resolved by the later Phase 4 owned-workspace change recorded below. Phase 3 container isolation remains incomplete.

## 2026-11-18 — Phase 5 durable GitHub PR worker (initial slice)

- Extended webhook persistence to atomically enqueue normalized exact-SHA jobs, supersede older queued/running heads, and cancel active queue entries on `closed` events. Added repository-filtered lease claims and bounded retries.
- Added `github serve` with request timeouts and a repository-scoped `github worker <owner/repo> once|loop`. The worker fetches only `https://github.com/owner/repo.git` PR refs into a temporary bare repository, verifies the full SHA, uses operator-local manifest policy and the rootless container runner, persists check-output evidence, validates proof artifacts, checks current PR head, and maps outcomes to explicit Check Run conclusions. API tokens are not added to the verification environment; fetch auth is confined to the trusted Git subprocess environment.
- Queue, exact PR-head API and CLI package tests pass. Real GitHub and Linux container tests remain unavailable; hosted proof URLs, GitHub App lifecycle and Enterprise remotes remain open. These component tests do not complete Phase 5.

## 2026-11-18 — Phase 4 owned plan workspaces

- Plan attempts now materialize a fresh workspace from the preceding completed immutable candidate, persist that owned path in durable intent, capture the full plan source selection, and feed verified output forward to later tasks. The project working tree is no longer handed to the agent.
- Successful verified workspaces are removed only after the workflow transition and only when an attempt-specific sidecar owner marker matches. Failed/UNKNOWN attempts remain for inspection; restart after candidate proof persistence can revalidate the immutable archive if safe cleanup already removed the workspace.
- Added workspace materialization/owner-cleanup coverage and updated persistent-plan documentation. Orchestration and CLI tests pass. Full end-to-end failure matrix remains outstanding.

## 2026-11-18 — Phase 6 session observation bridge

- Rejected passing a correlation token through the Codex agent environment because agent code can read that environment. The trusted runtime instead records the structured Codex thread ID on the durable result.
- Added Vigil's versioned read-only `vigil_correlate_session` MCP query, limited to the exact session ID and the attempt time window. Verity validates returned session/turn/time fields and saves a candidate-bound observational link; missing Vigil observations remain non-blocking.
- Focused Verity and Vigil package tests pass. Real Codex-to-hook delivery and identifier compatibility are not demonstrated in this environment, so Phase 6 remains incomplete. See `verity/docs/decisions/0014-vigil-session-observation-correlation.md`.

## 2026-11-18 — Phase 7 fixture repository initial slice

- Added `verity/testdata/execution-integrity`, a safe generator for a disposable two-commit repository containing staged, unstaged, and untracked changes plus opt-in pass/fail/slow/artifact, Playwright, and isolation-probe scenarios.
- Added a verification package test that generates the repository, checks its Git state, runs the passing baseline, and confirms the deterministic failure mode. The isolation probe is not executed on the host.
- The fixture generator test passes. Real Linux container, GitHub delivery, browser, and Codex hook evidence remain separate release gates.
- `GOCACHE=/tmp/axiom-final-gocache make check`, `go vet ./...`, `go build ./...`, both modules' `go test -race ./...`, `git diff --check`, and gofmt checks passed. Registered snapshot verification passed as `vr_8287a0788e6bea73e6c904c541b78e99`; the Phase 7 chat remains at its unapproved human gate.

## 2026-11-18 — Phase 3 exact network allowlist

- Added trusted-manifest `network_allowlist` entries for exact lowercase DNS `host:port` pairs, bound the sorted list into the approval plan and verification run, and preserve it in the embedded proof run.
- Kept container networking at `--network=none`. Allowlisted traffic uses a mounted read-only Verity relay plus private Unix socket to a host CONNECT broker. The broker checks exact destinations, resolves DNS on the host, rejects private/reserved/translation ranges, and dials the validated public IP directly.
- Added tests for allowlist validation, Docker argument isolation, broker denial, public/reserved address classification, and relay argument rejection. Focused verification and CLI packages passed. Rootless Docker runtime and malicious-container execution remain unverified because this host has no Docker daemon; Phase 3 stays open pending that evidence.
- Extended the GitHub worker fixture to submit an HMAC-signed webhook request and drive the durable queue through verification, proof reference, and exact-SHA Check Run publication using fake GitHub transport. The stale-head case remains covered. This is deterministic local end-to-end coverage, not a live GitHub or container run.
- Tightened allowlist enforcement so an approved CONNECT authority must also match the tunneled TLS ClientHello SNI; added parser coverage for valid and invalid ClientHello data. The broker still dials only a validated public IP. Rootless Docker validation remains unavailable on this host.
- Added a `net.Pipe` end-to-end broker test proving matching SNI bytes reach the pinned upstream and mismatched SNI bytes do not. Clarified that encrypted HTTP authority headers are outside this TCP/TLS destination policy.
- Added explicit per-browser-scenario test credential bindings from trusted `VERITY_TEST_*` variables. Values require HTTPS and an allowlisted base host, explicit `expose_to_test_code`, an RFC3339 expiry within the declared (maximum 10-minute) lifetime, and are available only to the container's Playwright invocation. They are not serialized into plan/proof and are redacted from outputs; missing or stale values block before snapshotting. Normal command checks and agent provider credentials remain isolated. Focused tests cover validation, destination scope, expiry, redaction, selected env forwarding, and no-start fail-closed behavior.

## 2026-11-18 — Phase 0 typed candidate migration

- Added shared versioned candidate identities for commit, index-tree, and content-snapshot candidates. New durable execution attempts persist typed base identity; successful durable results and operator reconciliation require typed output identity. Verification schema 2 and proof bundle schema 2 require matching typed identities. Verity-owned Vigil links now use schema 2 and include the typed candidate.
- Pre-versioned attempts remain readable as schema 1; verification and proof schema 1 remain readable. Focused tests cover old attempt reads, durable typed identity round-trips, schema mismatch rejection, and the existing verification/proof flow.
- Focused affected-package tests pass. The registered snapshot verifier passed as `vr_78ce4c406ad29f4432a1f9a6e118488d`; root checks, race, vet, and build passed for this continuation.

## 2026-11-18 — Phase 3 host snapshot extraction limits

- Candidate unpacking now enforces archive and extracted-content byte limits plus a 50,000-entry limit. Truncated regular-file entries fail closed. Added focused quota-boundary tests.
- Each host snapshot is bounded; aggregate disk use across concurrent processes, explicit network destination allowlists, real Linux runtime tests, and malicious-container integration tests remain open.
- Root `make check`, both full race suites, both `go vet ./...` and `go build ./...`, and `git diff --check` passed. Registered snapshot verification is being run for this continuation.

## 2026-11-18 — Phase 3 container archive entry limit

- Applied the same 50,000-entry ceiling to host-side tar creation before files are copied into the bounded container archive. Added a focused test proving archive creation fails closed when the entry limit is exceeded.
- Focused `go test ./internal/verification`, root `make check`, and `git diff --check` passed. Registered snapshot verification `vr_b0374057ebd4c215407bd37815e3db2d` failed because its temporary HOME could not resolve `proxy.golang.org` to fetch uncached Go modules; the tracker test passed. The registered verifier's network-dependent checks remain unverified for this snapshot.

## 2026-11-18 — Phase 3 verifier-output redaction

- Verification output from both local and container runners is now redacted before it is returned for evidence capture. Patterns cover common GitHub/cloud keys, bearer authorization, credential assignments, PEM private keys, and URL user info. This supplements reduced environments and bounded output; it does not make passing credentials to checks safe or provide exhaustive secret detection.
- Focused redaction tests, root `make check`, and `git diff --check` pass. Registered snapshot verification passed as `vr_1fed046a4f8523be897c58d50b949c20`; its workflow remains at the unapproved human gate.

## 2026-11-18 — Phase 3 browser URL credential checks

- Trusted manifest validation now rejects Playwright base URLs containing URL user information or common credential query parameter names. The runner has no scoped credential broker, so such credentials cannot be smuggled through its base-URL setting.
- Focused manifest tests, root `make check`, and `git diff --check` pass. Registered snapshot verification passed as `vr_8bcf3ee2aa84bbd53acc4de773ca0fb5`; its workflow remains at the unapproved human gate.

## 2026-11-18 — Phase 3 timeout cleanup control flow

- Added a fake-runtime integration test that forces a rootless container check timeout and verifies the result is `TIMED_OUT` and the runner requests `rm --force` cleanup. This tests host-side behavior but is not evidence from a Docker daemon or malicious container.
- Focused test, root `make check`, and `git diff --check` pass. Registered snapshot verification passed as `vr_a5dc759fc945e5847e20a875e8f0eaef`; its workflow remains at the unapproved human gate.

## 2026-11-18 — Phase 3 opt-in malicious-container probe

- Expanded the disposable malicious probe to check protected-path writes, loopback and external TCP access, credential-named environment variables, and runtime socket presence. Added an opt-in test that invokes it only through the rootless container runner on Linux with a digest-pinned image; normal test runs skip it.
- The real runtime test cannot run on this macOS host. Python probe syntax, focused fixture tests, root `make check`, and `git diff --check` passed. Registered verification `vr_8b69ddd534f8b3e03b67f75204d0b7d4` passed; its workflow is at the unapproved human gate.

## 2026-11-18 — Phase 3 isolation probe coverage correction

- Corrected the network probe to account for Docker's `none` network retaining container-local loopback. It now verifies only the loopback interface is present, local loopback works, and external egress is denied. Added symlink escape, bounded PID-spawn, host-home sentinel, and host-environment secret checks, with structured-output assertions in the opt-in Linux runtime test.
- Python AST parsing, focused verification-package tests, root `GOCACHE=/tmp/axiom-phase3-root-cache make check`, and `git diff --check` passed. Registered full snapshot verification `vr_2a46d6ea04207073d2d9fb0b186d44cc` passed. The opt-in runtime integration is skipped on this macOS host; real Linux/rootless-Docker execution is still required.

## 2026-11-18 — Phase 3 cross-process snapshot quota

- Added a per-user Unix `flock` lease held from before Git archive creation through verification cleanup. It serializes Git commit, staged-index, and content archive extraction/staging under one shared temp directory. Crashed processes release the lock automatically; the next holder removes only private, marker-owned snapshots and private Verity candidate archives. Old/unmarked paths remain untouched.
- Index-tree listing and materialization now also obey the 512 MiB/50,000-entry limits; content selection uses the same entry ceiling. A first cleanup test exposed an unsafe name collision with temporary HOME directories, which was fixed by changing the snapshot prefix and requiring the owner marker. Cross-process lock/cancellation/orphan tests now pass.
- Focused package tests pass. The first full package pass found that a content-snapshot test held its lease while starting another capture; the test now releases it between operations. Root `make check` and `git diff --check` pass with the final crash-release assertion. Registered snapshot verification `vr_48dfd09ffd5d6ea23490a3e3f0cdb68e` passed; its workflow remains at the unapproved human gate.

## 2026-11-18 — Phase 4 stale-candidate completion guard

- Added a CLI orchestration regression test that persists a passing verification for an older candidate during a successful execution attempt, then makes the actual candidate check fail. It proves the stale PASS is not reused and the task/workflow do not complete.
- Focused test, root `make check`, and `git diff --check` pass. Registered snapshot verification passed as `vr_f8fe247bbe906878abb929e53cb168e4`; workflow `chat_9506932f38afba4dbbe1c49ac2e67d75` is at the unapproved human gate.

## 2026-11-18 — Phase 5 authorized manual PR retrigger

- Added `verity github retrigger owner/repo <pull-number>`. It resolves the authenticated GitHub actor, requires write-capable repository permission, fetches the live PR head, then enqueues a fresh actor-attributed workflow/job. Delivery replay is idempotent, mismatched delivery reuse fails, and parallel same-head work is rejected until the previous job is terminal.
- Added SQLite migration 9 to preserve old queue rows while allowing multiple historical runs for one PR head, and migration 10 for an append-only actor audit exposed through workflow listing. Dashboard schema validation and compatibility documentation were updated. Focused GitHub, store migration, and CLI tests pass. Root `GOCACHE=/tmp/axiom-phase5-final-cache make check`, targeted race tests, Verity `go vet ./...`, and `git diff --check` pass. Registered full snapshot verification `vr_13e87fe1cb1d0047dbccf88e20d6f43b` passed; workflow `chat_815c967261800a98b57e7e477ec4ecdf` remains at the unapproved human gate.

## 2026-11-18 — Phase 5 GitHub Enterprise Git remote

- Added operator-configured `VERITY_GITHUB_GIT_URL` alongside `VERITY_GITHUB_API_URL`. The worker validates the HTTPS Git base, rejects credentials/query/fragment/traversal and invalid repository path segments, scopes its temporary Basic authorization header to that base path, and still defaults to public GitHub.
- Focused URL and fake-Git tests verify Enterprise path construction and that credentials are configured only for the Git subprocess. Root `GOCACHE=/tmp/axiom-enterprise-root-cache make check`, Verity `go vet ./...`, and `git diff --check` pass. Registered snapshot verification `vr_7e8352e151eda5019180b2faf3a2c004` passed; workflow `chat_72d10a4c9983a9db162598323ca635ef` remains at the unapproved human gate. Live Enterprise and GitHub App authentication remain unverified.

## 2026-11-18 — Phase 5 GitHub App installation token lifecycle

- Added GitHub App RS256 JWT creation, repository installation discovery, repository-scoped installation-token creation, and refresh before expiry. The worker accepts either this App identity or a PAT; Git fetch receives the resolved installation token only through its trusted Git subprocess environment. Manual retrigger remains PAT-based because it requires an authenticated human actor.
- Fake-transport tests verify JWT signature/claims, installation lookup, repository scoping, cache reuse, and refresh. Root `GOCACHE=/tmp/axiom-appauth-root-cache make check` and `git diff --check` pass. Registered verification `vr_f7d5b74f2310eb5acf1a87fee7f45953` passes after its first network-restricted attempt could not download dependencies. Live App credentials, GitHub Enterprise, and Linux rootless-container runs remain unverified.

## 2026-11-18 — Phase 7 slow fixture execution

- The generated fixture already contained an opt-in slow test, but the generator test never ran it. Fixture validation now runs the slow scenario and asserts a bounded duration. Focused Verity verification-package tests and root `GOCACHE=/tmp/axiom-phase7slow-cache make check` pass, along with `git diff --check`; `go vet ./...` passes in both modules. Registered verification `vr_e77f13b489be537eacad6ae47b53fb1a` passed. Real Playwright and rootless-container paths remain environment-dependent; current Docker Engine inspection found no running daemon/socket.

## 2026-11-18 — Phase 4 restart-window evidence

- The recovery test now explicitly represents a process crash after durable invocation intent and before provider invocation. It asserts the attempt remains `UNKNOWN` without a fabricated result and that resume never calls the provider; the provider's unknown outcome is separately labeled as timeout/uncertain completion. Focused CLI regression and root `GOCACHE=/tmp/axiom-phase4-restart-cache make check` pass, as does `git diff --check`. Registered snapshot verification `vr_64f6212966a304202e67845f0cb0cba8` passes.

## 2026-11-18 — Phase 5 GitHub API retry evidence

- Added fake-transport tests showing a transient 503 is retried and then succeeds, while exhausted 403/429 rate limits remain an explicit `ErrRateLimit`. Focused GitHub integration tests and root `GOCACHE=/tmp/axiom-phase5-retry-cache make check` pass, along with `git diff --check`. Registered snapshot verification `vr_8b6fc337698ce7b8f54891ea47110615` passes.

## 2026-11-18 — Phase 7 approved-manifest mutation scenario

- Extended the multi-step plan test to mutate the project manifest after an actor approves a gate; the plan still uses the captured policy snapshot and completes verification. Focused CLI test and root `GOCACHE=/tmp/axiom-phase7-manifest-cache make check` pass, as does `git diff --check`. Registered snapshot verification `vr_16b766c811d88a4a9172ad1395d2cc31` passed.

## 2026-11-18 — Phase 7 staged-index mutation scenario

- Extended the pre-commit integration test so a trusted check changes the repository's real index while checks execute against the original immutable staged tree. Verity detects the changed candidate and blocks with a stale result. Focused hook CLI regression and root `GOCACHE=/tmp/axiom-phase7-indexmutation-cache make check` pass, as does `git diff --check`. Registered snapshot verification `vr_7988b4fcc986806a72de0394619c8a63` passes.

## 2026-11-18 — Phase 5 worker orchestration regression

- Added a Linux non-root fake-adapter test for queue claim, exact ref fetch, proof validation and Check Run publication. It covers both a passing current head and a changed head that must publish `cancelled` without a PR summary. This checks orchestration wiring, not real container isolation. It was skipped on this macOS host. Root `GOCACHE=/tmp/axiom-phase5-worker-cache make check` and `git diff --check` pass; registered verification `vr_30e3552ca545dce625362f7d9bd1b6ea` passes.

## 2026-11-18 — Phase 5 durable rate-limit retry

- Added a platform-independent worker test that injects a GitHub `429` during progress publication and verifies the job returns to PENDING with its error and retry time recorded while the workflow remains READY. Focused worker test and root `GOCACHE=/tmp/axiom-phase5-retryqueue-cache make check` pass, as does `git diff --check`. Registered snapshot verification `vr_0d699a5eece63e43b019b4c51b27d368` passes.

## 2026-11-18 — Phase 5 installation-bound webhook processing

- Pull-request normalization now preserves GitHub's installation ID. App-authenticated workers require that ID on webhook PR jobs and compare it with the installation discovered for the configured repository before Check Run progress or candidate fetch; persisted event repository, PR number and head SHA must match the durable queue row. The normalized base SHA is also now retained for candidate provenance. Focused GitHub integration tests and root `GOCACHE=/tmp/axiom-install-scope-root make check` pass, as does `git diff --check`. Registered verification `vr_2376b44649b7264063d0a94c8b5894c2` passes.

## 2026-11-18 — Phase 5 proof export reference

- Completed GitHub Check Run summaries now include the precise `verity proof export` command for their persisted proof ID. The worker test asserts the command uses the verification ID attached to the candidate. A public hosted proof URL remains unsupported. Focused GitHub integration tests and root `GOCACHE=/tmp/axiom-proof-reference-root make check` pass, as does `git diff --check`. Registered verification `vr_11dd4d41f7f96f0a0f5d1c129d9ea97f` passes.

## 2026-11-18 — Phase 5 ingress installation allowlist

- `PullWorkflowHandler` now rejects mismatched app installations before creating a workflow or claiming a delivery, and `github serve` / `github receive` accept `VERITY_GITHUB_INSTALLATION_ID` for this ingress check. The worker retains its independent discovered-installation check as defense in depth. Focused GitHub and CLI package tests and root `GOCACHE=/tmp/axiom-ingress-allowlist make check` pass, as does `git diff --check`. Registered verification `vr_679cae6e63d7ea11085e415d1e178607` passes.

## 2026-11-18 — Phase 7 Git hook bypass evidence

- Added a CLI integration test that stages a candidate with a failing pre-commit profile, installs the managed pre-commit and post-commit hooks, commits with `--no-verify`, and confirms the pre-commit wrapper was skipped while the post-commit observer ran. The store has no passing verification for that exact index tree. Focused `go test ./cmd/verity -run TestGitNoVerifyBypassDoesNotCreatePassingVerification -count=1` and root `GOCACHE=/tmp/axiom-bypass-make-cache make check` pass, as does `git diff --check`. The first registered full snapshot attempt failed because its temporary HOME could not resolve Go modules; a network-enabled run passed as `vr_c081d862d0dec9330ecc6a2465014c72`. A fresh snapshot after this log update passed as `vr_34edb9c1e4013dd74d37cdada2cebea1`. Local hooks remain bypassable and are not a remote enforcement boundary; the workflow remains at the unapproved human gate.

## 2026-11-18 — Phase 4 proof export integrity

- `proof export` now verifies every referenced content-addressed artifact against the local store before writing a bundle. The staged-index CLI test exports twice and checks byte-for-byte reproducibility, then corrupts and removes a referenced artifact and confirms export fails without leaving an output file. Focused `go test ./cmd/verity -run TestVerifyIndexUsesApprovedStagedCandidate -count=1`, root `make check`, and `git diff --check` pass. The subsequent snapshot-verifier attempt for this documentation update is pending; previous candidate snapshot `vr_e95cf5ef8525ad361ed0c260dc0f95e9` passed, while the latest snapshot attempts could not resolve `proxy.golang.org` from the runner's temporary HOME.

## 2026-11-18 — Phase 2 symlink hook refusal coverage

- Added a regression proving `hooks install --chain` refuses a pre-existing symlink hook and leaves both the symlink and target contents unchanged. The manager already rejected symlinks; the missing evidence was test coverage. Added a Phase 3 runner regression proving a missing container runtime returns `BLOCKED` without running checks. Both focused tests pass with a temporary Go cache.
- Final root `GOCACHE=/tmp/axiom-symlink-hook-final make check` and `git diff --check` pass. Registered snapshot `vr_01a66a3e9c80714f89ce8f78c2a19e23` and the final-source rerun `vr_1e2b604f1761f22028e36e5b88376a41` failed because their temporary HOME could not resolve `proxy.golang.org` for uncached modules; tracker tests passed in both. Live Linux rootless-runtime, GitHub, and Codex hook delivery evidence remains environment-dependent.

## 2026-11-18 — macOS-local roadmap acceptance

- Narrowed roadmap acceptance to trusted local repositories on macOS. Local checks use immutable snapshots, bounded execution, and a reduced environment, and are explicitly documented as not OS-sandboxed. Untrusted PR execution, Linux isolation evidence, and live GitHub hosting are optional deployment work outside this target.
- `GOCACHE=/tmp/axiom-plan-final-cache make check` passed. Registered snapshot verification `vr_5a00f9d36ace1ae4a25ac487e1ec88c6` passed all three required checks: Verity Go tests, Vigil Go tests, and tracker tests. Module downloads used the existing local Go module cache through a temporary PATH wrapper; no repository manifest changes were needed. The final human approval gate remains pending.
