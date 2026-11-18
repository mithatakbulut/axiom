# Persistent plans use workflow runs and durable attempts

Plans extend the existing workflow definition and task states rather than introducing a second state machine. The captured verification manifest and selected source paths travel with the definition. A final dependent verification task prevents task completion alone from reporting plan success. Each task result is checked against a content snapshot; required check artifacts are verified again before final completion.

A local filesystem lock serializes schedulers for one plan without an expiring lease. Invocation intent is persisted before calling the CLI. When a scheduler finds an in-flight intent after obtaining that lock, it marks the attempt UNKNOWN and requires an actor-recorded reconciliation or abandonment. A finished attempt and persisted passing verification can be reused after interruption. The lock assumes cooperating schedulers on the same filesystem; it does not establish distributed coordination or prove that a child process stopped after its parent died.

A human approval remains an explicit workflow task and records its actor and reason. The CLI does not manage Codex desktop chats. Instead, each managed CLI turn receives a continuation packet assembled from the saved workflow and verification evidence.
