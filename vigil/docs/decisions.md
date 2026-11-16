# Architecture decisions

## 2026-11-13: Discovery precedes controller

Status: accepted for milestone 1, subject to version-specific compatibility tests. Use supported hook contracts as best-effort event sources and app-server `model/list` as the candidate for capability discovery. Keep a versioned internal event envelope independent of transcripts. Advisor execution must enforce read-only permissions, bounded runtime and output, minimal environment and loop prevention. Model availability and execution require runtime checks. Do not bypass hook trust. Live tests verified supported-model inference and one read-only write denial, and trusted CLI hook delivery and Stop continuation. The proposed gpt-6.1-sol is unavailable for this account, and CLI 0.147.0 did not surface model-request hooks in the spike. Preserve these as explicit compatibility limitations; never assume complete event coverage.

## 2026-11-13: Initial recorder boundaries

The first recorder uses a SQLite WAL database and a versioned, allowlisted event envelope; it never stores hook prompts, tool inputs, responses or transcript paths. Exact input bytes are hashed for duplicate detection because tested hooks do not expose a universal event ID. This can collapse independent identical deliveries; a stable upstream event identifier would be preferable when available. The CLI hook handler fails open and emits generic local JSON errors. Hook registration remains Milestone 2 and requires the normal trust review. SQLite is accessed through the cgo-based mattn driver; a C compiler is required.

## 2026-11-13: Recorder enrichment and privacy

Session summaries are derived from retained event records, correlating tool endpoints by session, turn and tool-use ID. Numeric usage and structured failure fields are recorded only when present; the observed Bash envelope can supply an exit code. Fingerprints are deliberately coarse and do not store arbitrary error text. Working-directory paths are omitted because path segments can contain secrets. Exact payload hashing remains an imperfect deduplication compromise in the absence of a universal hook event ID. Advisor execution tracking remains with Milestone 4. See `hook-contract.md` for tradeoffs.

## Control state during Milestone 2

Pause and selected operating mode are stored in SQLite so CLI and MCP instances share state. Configuration supplies the initial mode. Selecting assist or autonomous records intent only; the status reports intervention unavailable until later milestones implement and validate that capability. History exposes only already-filtered recorder metadata. No escalation decisions exist yet, so explain returns that fact.

## 2026-11-13: Codex registration and recovery

The installer edits only user-level `config.toml` and `hooks.json` in the selected `CODEX_HOME`. It validates TOML before appending a separate MCP table and appends hook groups after existing groups so existing hook indices keep their order. A private backup records before and after states for both files. Individual writes use same-directory atomic replacement, and a retry can finish an interrupted two-file operation if the inputs still match a recorded state. Uninstall removes owned entries, including an equivalent MCP table reformatted by Codex. Rollback restores original files only when that cannot overwrite later edits. Codex retains ownership of trust review and its stored review state.

The installer registers the eleven hook events recognized by CLI 0.147.0. The parser continues accepting model-request and Interrupt events when an upstream sender delivers them. Relative storage paths resolve against the Vigil YAML location so CLI and Codex-launched processes see the same database.

## 2026-11-13: Passive progress observations

Progress snapshots are computed on demand from retained events for one session; they do not invoke an advisor, write an escalation decision, or alter hook output. A candidate is an observation rather than an instruction. Repeated coarse recorder error fingerprints and repeated failed commands are signals, suppressed while a permission request is pending or the latest comparable test counts improve. This deliberately favors avoiding interventions; session-wide evidence can become stale and is not a task-boundary model.

For new PreToolUse events, the recorder stores a SHA-256 equality fingerprint of the trimmed command, never the command itself. Hashes disclose equality and can be guessed for common commands; tool-use IDs are required to associate failures. Existing events cannot recover this information. Only explicit `Tests: N passed, M failed` Bash output summaries are counted; other formats and absent hooks remain unknown. Existing error fingerprints classify exit codes and generic failures, not semantic root causes. Explicit `--repo` Git observation reads tracked changes against HEAD; it does not include untracked files, identify edits over time, or prove progress. Missing Git, an unborn HEAD, and inaccessible repositories are unknown. No repository path is persisted.

## Turn-scoped detection and advisor quotas

Stop persists a snapshot per observed turn; a user prompt resets session-wide diagnostic history. Uncorrelated or missing hooks reduce confidence and coverage. Error signatures and command equality are derived without retaining command or output text; Git counts remain supporting context. No advice is sent from a recursive Stop or while paused. A single SQLite write reserves each advisor slot, including the once-per-turn automatic guard. Tasks are keyed by an explicit contract when available, and otherwise share the session-level allowance. An older turn remains associated with the task active when its first retained event was recorded.

## Model and evidence boundaries

Keep the proposed model explicit and discover the model/effort pair before execution; a catalog result is not proof of inference entitlement. Assist requests originate in CLI or MCP, but running one with approval and waiving a requirement require an explicit terminal action. Keep authorization out of agent-controlled MCP arguments. Store a bounded outcome, not advisor text. Task evidence is append-only and verification uses the latest submission per requirement. A captured successful test can be linked to a requirement; hook coverage and explicit source attestations remain distinct.

## Release rollout

Ship local arm64 and amd64 candidate packages with Observe and shadow mode enabled by default. A live comparison and desktop GUI review are separate acceptance gates. The current Codex hook contract cannot enforce all primary-agent model requests or subagent actions; report observed consumption and enforce budgets on Vigil-owned advisor calls. Resource samples report threshold breaches and uncertain ancestry without process cleanup. Learning Loop and menu bar remain post-MVP.
