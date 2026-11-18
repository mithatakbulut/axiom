# macOS-local acceptance — 2026-11-18

The core trusted-repository lifecycle has local test coverage. Overall local
release acceptance remains open. HTTP APIs, distributed execution, production
deployment, live GitHub hosting and Linux isolation are outside this target.
This review preserved the existing `proof verify` implementation and its tests.
It validates exported bundle/artifact consistency without opening a project
database; it does not authenticate the producer or prove actual execution.

## Acceptance evidence

| Criterion | Observed evidence | Remaining work |
| --- | --- | --- |
| Real-task stuck detection | Rechecked the five archived Observe/Assist turns from the November 16 batch. Three previously reported call-join gaps were wrapper-classification errors, as detailed below. | Independently label eligible complete turns and collect genuine stuck cases. Precision and recall remain unmeasured. |
| Advisor intervention and recovery | Approved Assist advice and a separate actual-CLI automatic Stop → live advisor → continuation → passing tests → exit-zero chain have now run. The automatic run called the advisor once and suppressed the recursive Stop. | Both examples are controlled workflows. The automatic advisory was generic and the primary rejected its unsupported theory; improved advice usefulness, natural stuck detection and independent efficacy measurements remain open. Normal-profile Autonomous remains gated. |
| Desktop hook delivery | The user reports that hooks are active in Settings. Public MCP history confirms one completed desktop turn with its prompt event, all four tool pre/post pairs and natural Stop. | The GUI report is user attestation, not agent inspection. This covers that completed turn, not every tool type or session. |
| Desktop GUI and Stop | The completed turn's saved snapshot matches all ten retained events and records `continue` / `observe`, with no advisor calls. `cua.getApp("com.openai.codex")` was denied before inspection. | Passive Stop delivery/analysis is verified for this turn. Real Stop continuation, recursion behavior and advisor recovery remain open. Computer Use cannot automate its own host app; user-observed GUI checks are the supported path here. |
| Managed Codex–Vigil correlation | The source implements structured provider thread-ID capture and a versioned MCP query bounded by attempt time. The automatic CLI trial's structured provider thread ID equals its real hook session ID. Eight client rejection cases cover invalid observations. | Identifier equality is now observed on CLI 0.147.0; candidate-bound persistence through a real managed Verity execution remains unverified. This chat's 11 connected Vigil tools do not expose `vigil_correlate_session`. |

The initial audit started no additional Codex process or live benchmark session
and invoked no advisor. The later controlled exception is recorded below.
The normal profile's hook trust and Observe mode were unchanged. Later tests
temporarily reviewed hooks and enabled Autonomous only in a separate
disposable setup, then restored that profile. No human gate was approved.
The root instructions require the local Verity workflow in this chat and
prohibit starting another Codex process. A future managed live trial therefore
needs an explicitly authorized workflow that permits that execution, as well
as normal hook trust and compatible installed Vigil tooling. It must use the
provider's structured result and public MCP contract, not transcript scraping
or direct Vigil database access by Verity.

### Follow-up after user hook confirmation

The user reported that hooks are currently active. Read-only MCP checks then
verified the completed desktop turn `01a1f012-466c-7f02-87a0-34dc7afd4152`:
one `UserPromptSubmit`, four uniquely matched `PreToolUse`/`PostToolUse` pairs
(one Bash, three `webrun`) and one non-recursive `Stop`, delivered at
`2026-11-18T19:13:11.493802Z`. The latest saved progress snapshot has the same
session/turn and ten events, decision `continue`, action `observe`, no stuck
signals and no advisor record. Eight explicit metadata assertions passed.
The connected server is unpaused in Observe; interventions are unavailable.

The MCP replay evaluation passed 10/10. Focused source tests
`TestModesAndStop`, `TestCorrelateToolIsVersionedAndReturnsOnlySessionAssociation`
and `TestSessionObservationsRespectAttemptTimeWindow` passed. These fixtures
exercise approval rejection, shadow/pause suppression, fake-advisor
continuation, recursion and duplicate-Stop guards, and the MCP time-window
contract; they are not a live advisor invocation. No installed binary or
configuration was changed. No additional Codex process was launched.

The local metadata receipt is
`vigil/docs/evidence/2026-11-18/desktop-hook-followup.json`. The assertion
compares independently returned MCP history and decision data, without
reading Vigil's SQLite database. It does not establish managed provider
correlation or the Stop of the still-running follow-up turn. The verification
workflow for this evidence update is `chat_8306d1c674f0d6d781f64f32bea60a06`;
its human gate remains unapproved.

The GUI access limitation is a documented product boundary, not a missing
macOS permission that the user can necessarily grant. The official
[Computer Use safety guidance](https://learn.chatgpt.com/docs/computer-use#safety-guidance)
excludes automating ChatGPT itself. The earlier request for an approved
self-automation surface should not be interpreted as an available setting.

### Explicitly approved live advisor and controlled recovery

The user asked to test intervention and approved one bounded live advisor call
for the prepared `controlled-url-failure` candidate, including the necessary
Codex model-discovery/advisor subprocesses. This is a narrow exception to the
root instruction against starting another Codex process. No new primary
Codex task was started. The test used a fresh temporary source directory and
Vigil database, Assist with approval required, shadow enabled, and one-call
limits per turn and task. The installed application remained Observe.

Two intentionally incorrect URI resolvers each actually failed
`TestResolve/escaped-slash`: one double-escaped `%2F`, the other decoded it
into a structural slash. Their command results were explicitly replayed
through the built Vigil hook command. This was not natural Codex delivery.
Stop analysis found two distinct attempts and the repeated `tool-exit-1`
fingerprint, producing `escalate_candidate` / `approval_required` and no
advisor before approval. The full unchanged suite then showed one passing
and three failing subtests.

The selected `gpt-5.6-sol/high` passed live model discovery on CLI 0.147.0.
The approved `vigil advisor run` returned a structured recommendation and
persisted `succeeded`. It identified decoded/normalized path reconstruction
as the likely problem and directed preservation of escaped separators and
query semantics. It also stated that the coarse repeated exit signal did
not establish the exact assertion diff. No silent model fallback occurred.
The runner's 35-second limit, read-only policy and disabled child hooks were
retained. All source file hashes were unchanged across the advisor call.

The primary agent evaluated the advice, replaced manual `path.Join` /
`PathUnescape` with `url.Parse` and `ResolveReference`, and reran the exact
unchanged tests: escaped slash, ordinary relative reference, absolute path
and query-only reference all passed (4/4, exit zero). The test file hash is
unchanged. A CLI request without `--approved` was rejected; final usage
reports exactly one advisor call and no running advisor. Existing fake-CLI
runner/control tests also passed, covering timeout/output limits, approval,
shadow/pause suppression, continuation, recursion and duplicate-Stop guards.

This verifies one live **approved Assist advice → primary-agent correction →
passing unchanged checks** path in a deliberately prepared example. The
primary agent already knew the task; this is not an independent efficacy
comparison. Assist returns the advice for review; it does not automatically
resume the main Codex task. No live autonomous continuation, independently
labeled natural stuck event, causal recovery uplift, precision or recall is
claimed. The remaining acceptance gates are not closed by this example.

Local evidence under `vigil/docs/evidence/2026-11-18/advisor-controlled/`
contains the two failing implementations, preparation code, original tests,
before/after output, actual recommendation, model check, unchanged-source
hashes, advisor state/usage and a SHA-256 manifest. These are intentionally
Git-ignored local records. The original disposable database remains under
`/private/tmp/vigil-advisor-acceptance-pjmtk3rz/`; no credentials, binary or
authentication profile were copied. Repository snapshot verification is
separate from the directly executed controlled-example checks. Workflow
`chat_8782e9274d2306137af82ae88772d123` tracks this update and retains its
human approval gate.

### Actual CLI automatic Stop continuation and recorder regression

At the user's request to test automatic intervention, a separate, signed-in
temporary profile ran CLI 0.147.0 with one primary `gpt-5.6-sol/none` task and
at most one `gpt-5.6-sol/high` advisor. Normal directory and changed-hook trust
review completed, and `/hooks` confirmed all 11 registrations active before
submission. No hook trust bypass was used. Only this trial's Vigil config was
Autonomous with shadow and per-call approval disabled; the user's request
authorized this scoped automatic test and its necessary subprocesses. The
ordinary installed profile stayed Observe.

The first task actually failed both prescribed Go commands, but the raw Bash
hooks omitted exit status. The recorder hashed failure lines only when an
error status was also known, so the initial Stop incorrectly had no failure
signals and invoked no advisor. A diagnostic rerun captured the real response
format. `TestGoTestFailureWithoutExitMetadata` and
`TestProgressGoFailuresWithoutExitMetadata` failed before the source fix:
empty failure fingerprint and `continue` with zero distinct attempts.
The fix recognizes a known failure line together with Go's terminal package
FAIL summary, without fabricating status or counts. A further regression
prevents conflicting passing `Tests:` summaries from supplying counts.

After the fix, a new primary CLI session ran the original task prompt once,
with no operator steering after submission and no replayed/synthesized hook
events in that session. The prompt deliberately required two reproductions
and an initial stop for review before editing. This is a controlled handoff
test, not an independently labeled naturally stuck trajectory.

- Provider thread and hook session both equal
  `01a1f02a-231b-7412-ad74-e962d50b039c`; all six pre/post tool pairs match.
- The real first Stop found repeated failure and two distinct attempts,
  invoked the live advisor automatically, and returned `decision: block`
  with the actual recommendation after 12.723 seconds.
- The primary received the advisory, continued without another user prompt,
  inspected and corrected the resolver, and passed all four unchanged tests.
  A separate post-run execution also passed all four. The original test file
  SHA-256 is unchanged.
- The second real Stop had `stop_hook_active=true` and an empty response.
  The disposable Vigil database contains exactly one escalation, marked
  `automatic=1` and `succeeded`; no advisor remains running. CLI recorded
  SessionEnd and exited zero.
- The test-profile configuration and hooks were restored byte-for-byte.
  Bootstrap also added a model-availability notice; restoration explicitly
  checked and removed only that known test-created difference. Initial
  bootstrap/cleanup failures and the diagnostic run are preserved separately.

The live advisor lacked a task contract and returned generic uncertainty and
an environment/precondition investigation. The primary explicitly rejected
that unsupported theory and diagnosed the code independently. This verifies
automatic invocation, actual advisory transport, continuation, recursion
suppression and termination, **not** a causal benefit from that advice.
Task-context quality and natural-case precision/recall remain open. Desktop
GUI continuation and managed Verity candidate-bound correlation are separate
unverified criteria.

The source fix and new tests are in `vigil/internal/recorder/`. The successful
live binary precedes the additional conflicting-summary count guard, whose
path was absent from the live outputs and is covered by the final regression
and full suites. The installed binary and release archives were not replaced
by this test. Rebuild those before deploying this source change.
Local evidence under `vigil/docs/evidence/2026-11-18/automatic-stop/` retains
the actual CLI output, minimal Stop receipts, real raw test-response fixtures,
before/after regression output, independent final checks, profile-restoration
receipt and a SHA-256 manifest. Profile backups/authentication files are not
copied there. Reading the disposable Vigil escalation table was an evaluation
audit, not a Verity bridge dependency. Workflow
`chat_228acef2726890efe891f8fd9293aef1` tracks verification and leaves human
approval pending.

Validation of the final recorder change: root `make check` passed both full Go
suites and the Python tracker; race-enabled recorder, control and CLI suites
passed. The original missing-metadata regression and the conflicting-summary
case both failed before their fixes. Full results and evidence hashes are
retained with the automatic-stop receipts. Source-snapshot verification is a
separate durable result in the workflow, not proof of natural-case efficacy.

## Retrospective coverage reconciliation

The original batch auditor treated every recorded `exec` wrapper as Bash.
Some wrappers contain a single explicit `tools.apply_patch(...)` call; their
hooks correctly identify `apply_patch`. The follow-up classifies only that
lexical form, without evaluating code, and retains the original unique
chronological join bounds (call −10 ms through result +100 ms). It does not
relax timing or infer missing hooks. This is a retrospective audit correction,
reported separately from the preregistered scoring.

| Archived turn | Previous joined calls | Corrected joined calls | Stop/snapshot | Interpretation |
| --- | ---: | ---: | --- | --- |
| 21612 Observe | 12/14 | 14/14 | 1/1 | Two patch wrappers reconcile the reported gap. |
| 23413 Observe | 13/19 | 19/19 | 1/1 | Six patch wrappers reconcile the reported gap. |
| 23413 Assist | 7/9 | 9/9 | 1/1 | Two patch wrappers reconcile the reported gap. |
| 21612 Assist | 5/5 | 5/5 | 0/0 | Still excluded: missing Stop and snapshot. |
| 23808 Assist | 4/14 | 6/14 | 1/1 | Still excluded: eight unmatched primary calls (four `exec`, four `wait`) and one unmatched hook pair. |

All five databases have zero unmatched pre/post hook IDs, zero unrelated
session records and zero tool/Stop records from unrelated turns. Equal hook
pair counts alone did not establish CLI coverage. Reconciled joins do not
establish independent blocker labels, semantic error detection, original-test
success, or advisor effectiveness; none of these turns is scored here.

The reproducible read-only auditor, trace SHA-256 values, counts and current
MCP evidence are preserved locally under
`vigil/docs/evidence/2026-11-18/`: `recheck-coverage.py`, `coverage.json`,
`desktop-hook-history.json` and `mcp-eval.json`. Run the auditor with the
retained `/private/tmp/vigil-live-acceptance.cTLLFb` directory as its sole
argument. The temporary inputs are not durable release artifacts. Evidence
is intentionally Git-ignored pending privacy/publication review; no ignore
rule was changed. These historical SQLite reads belong to Vigil evaluation,
not the Verity runtime bridge. No raw commands, prompts or outputs were
copied into this report.

## Verification

The focused session-correlation client tests passed, including all eight new
rejection cases. `GOCACHE=/private/tmp/axiom-go-cache make check` passed both
full Go suites and the Python tracker. Snapshot verification and its human
gate are recorded separately in the current Verity chat workflow; they do
not validate the open live criteria above.

The first snapshot run (`vr_4b6bdc0d976049baa5c954ac4bcbe848`) returned
FAIL: tracker PASS, both Go suites unable to download dependencies because
`proxy.golang.org` DNS resolution failed in the temporary-HOME environment.
This does not replace the passing local suites. Any network-enabled retry is
a separate verification record; retain the initial failure.
