# Candidate identity and trusted verification policy

Status: Accepted for local and self-hosted candidate/policy contracts

## Candidate identity

The shared versioned `candidate.Identity` distinguishes `git_commit`,
`git_index`, and `content_snapshot`. A commit candidate ID is its full commit
object ID; an index candidate ID is its complete Git tree object ID, with
`HEAD` retained as separate base provenance; a content candidate ID is the
source snapshot SHA-256. Git candidates record repository identity; content
snapshots record the configured project root where available. New execution,
verification, proof, and Verity-owned Vigil records carry this typed identity
and validate it against their legacy SHA projection.

## Trusted policy

The local Verity configuration and `.verity/verification.yaml` are operator
control files. A Git candidate containing either path is rejected. Plan
approval binds the exact local config bytes, parsed manifest, candidate,
profile, check argv, criteria mapping, and isolation policy. Repository code
cannot replace its own policy while being verified.

An operator can intentionally change local policy and create a new plan. A
hosted service must supply these control files from its trusted installation
configuration, not from the pull request tree.

## Profiles and execution boundary

Profiles are names selected by the project manifest. Verity recognizes
`minimal`, `pre-commit`, `pre-push`, `standard`, `strict`, `full`, `release`,
and `custom`; it does not prescribe framework-specific checks. The current
runner explicitly binds `local_process_reduced_env`. This is a reduced
environment, not OS isolation. A requested isolation policy unsupported by a
runner fails closed.

## Compatibility

Schema-version-1 verification records and proof bundles retain their existing
`candidate_sha` representation and remain readable. New verification records
and proof bundles use schema version 2 and require a typed candidate identity.
New durable execution attempts use schema version 2; pre-versioned attempt
records are read as legacy version 1. New Vigil bridge links use schema
version 2 while version-1 caller-supplied links remain readable. No SQLite
data rewrite is needed because these records are stored as JSON bodies.
