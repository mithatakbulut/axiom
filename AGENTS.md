# Axiom development

Verity owns workflow state, approval, verification and proof; Vigil owns hook
observation, Stop and advisor policy. The optional bridge is Verity-owned and
uses Vigil's public MCP interface. See `verity/AGENTS.md` and
`vigil/AGENTS.md` for entry points and focused tests. Run `make check` at the
root for both full Go suites and the Python tracker. Environment requirements
and cache guidance are in `README.md`.

For substantive changes, use the local Verity CLI in this conversation (do not
start another Codex process). From this directory run
`.verity/bin/verity chat start feature`, retain the ID, and begin and finish
`spec` and `implement` as work progresses using `chat begin <id> <task>` and
`chat finish <id> <task>`. Begin `verify`, then run:

```sh
.verity/bin/verity chat verify <id> README.md Makefile AGENTS.md \
  verity/AGENTS.md verity/README.md verity/Makefile verity/go.mod verity/go.sum \
  verity/cmd verity/internal verity/testdata verity/docs \
  verity/codex-plugin/README.md verity/codex-plugin/hooks/codex_hook.py verity/codex-plugin/test_hook.py verity/codex-plugin/.codex-plugin \
  vigil/AGENTS.md vigil/README.md vigil/go.mod vigil/go.sum vigil/cmd vigil/internal vigil/configs vigil/scripts vigil/docs
```

The manifest `.verity/verification.yaml` runs both full Go suites and the
tracker in the captured source. The manifest is bound to the plan, while
`.verity` runtime data, build outputs and Python caches are excluded. Add any
new relevant source paths explicitly. The runner uses temporary HOME and may
need network access even when ordinary local tests pass. Report its actual
result and limitations; do not approve the final human gate. If the CLI or
manifest is missing, report that. Simple questions and read-only investigations
skip this workflow. Vigil hooks are independent, best-effort observations; they
are neither approval nor proof of a Verity task.
