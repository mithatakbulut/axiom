# Persistent plans

A plan is an explicit JSON workflow definition. Create it with `verity plan create plan.json`; the command prints its ID. `verity plan show ID` includes the saved definition, task states, attempts and verification records; `verity plan history ID` shows recorded state transitions. Run `verity plan start ID` once. It launches one managed Codex CLI turn at a time and continues through ready tasks until completion, an approval, or an error. After a stop, use `verity plan resume ID`; `verity plan pause ID` requests a pause at the next transition. A running Codex invocation is not cancelled by pause.

Example (adjust the source paths and criteria to the project):

```json
{
  "schema_version": 1, "name": "Example", "version": 1,
  "goal": "Implement the feature", "acceptance_criteria": ["Feature checks pass"],
  "source_paths": ["src", "tests"],
  "max_tasks": 4, "max_attempts": 2, "max_transitions": 64,
  "tasks": [
    {"id":"one", "title":"First", "description":"Implement the first part",
     "acceptance_criteria":["First checks pass"], "source_paths":["src", "tests"],
     "timeout_seconds":1800, "max_attempts":2},
    {"id":"two", "title":"Second", "description":"Implement the second part",
     "depends_on":["one"], "acceptance_criteria":["Second checks pass"],
     "source_paths":["src", "tests"], "timeout_seconds":1800, "max_attempts":2},
    {"id":"review", "title":"Review", "description":"Review the result",
     "depends_on":["two"], "acceptance_criteria":["Human review"], "approval":true, "max_attempts":1},
    {"id":"three", "title":"Third", "description":"Finish the feature",
     "depends_on":["review"], "acceptance_criteria":["Feature checks pass"],
     "source_paths":["src", "tests"], "timeout_seconds":1800, "max_attempts":2}
  ]
}
```

The project must already have `.verity/verification.yaml`. Each non-approval task criterion and each overall criterion must exactly match a manifest criterion description. Required standard-profile checks execute against a snapshot for every task; their passing observations must cover the selected criteria. All task source paths must be within the overall selected paths. The manifest is captured when the plan is created. Verity adds a final overall verification task and checks that every earlier task retains its evidence before reporting success. The caller is responsible for selecting all relevant files. Each provider attempt receives a fresh disposable workspace materialized from the immutable candidate produced by the latest completed task; the developer's project working tree is not the provider workspace. Task outputs are captured over the plan's overall selected paths and passed as the next task's base. Successfully verified owned workspaces are removed only when their sidecar owner marker matches; UNKNOWN or failed workspaces remain available for inspection. Selected paths limit snapshot evidence, not operating-system permissions for Codex or checks.

When a human approves a waiting task, run `verity plan approve ID TASK ACTOR REASON` and then `verity plan resume ID`. Use `verity plan reject ID TASK ACTOR REASON` to reject the gate and fail the plan. When the captured manifest requires human review, inspect the NEEDS_HUMAN verification and use `verity plan review ID VERIFICATION_ID ACTOR REASON`, then resume. This records approval for that exact candidate and does not turn a failed check into a pass. Verity never supplies approval itself. An `UNKNOWN` attempt cannot be retried automatically. Inspect it with `plan show`, investigate its effects, then either run `verity plan abandon ID TASK ACTOR REASON` or `verity plan reconcile ID TASK OUTCOME CANDIDATE ACTOR REASON`; use an empty candidate argument for a non-success outcome. Reconciliation of a successful attempt requires an existing source snapshot digest. Resume after the recorded decision. To explicitly retry a finished task after a failed check or blocked result, use `verity plan retry ID TASK` and resume; the task's attempt limit still applies. Exhausted attempts fail the plan. A verification failure leaves the task incomplete and its verification record visible; resuming reruns verification against the same candidate. Overall verification can be retried by resuming.

Verity stores invocation intent before launching Codex. On restart an in-flight intent becomes `UNKNOWN`; it is never silently launched again. A persisted result and passing verification are reused after a restart. Each new CLI turn receives the plan goal, task, completed task results and verification records; this does not require the previous CLI conversation. The local lock serializes schedulers on the same filesystem. The Codex desktop app has no repository-facing, verified API for creating or taking over a chat from this CLI, so this implementation does not manage desktop chats; it uses managed CLI turns instead.
