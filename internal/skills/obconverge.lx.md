# obconverge reference

**Invocation**: `obconverge [global] <subcommand> [flags]`
**stdout**: artifact path. **stderr**: slog. Prefer `--skills-json` for parsing.

## Pipeline

`scan` → `index.jsonl` → `classify` → `classification.jsonl` → `plan` → `plan.md` → `apply` → `journal.jsonl` + `trash/`. `undo` reverses journal. `purge` empties trash.

Universal flags: `--vault <path>`, `--output/-o <path>`.

| Subcommand | Unique flags |
|---|---|
| `classify` | `--index` |
| `plan` | `--classification`, `--policy` |
| `apply` | `--execute`, `--secrets block\|warn\|silent` |
| `undo` | `--execute` |
| `purge` | `--execute` |

Mutating subcommands (`apply`, `undo`, `purge`) default to dry-run. `--execute` opts in.

## Global flags

`-c/--config <path>` (replaces user layer) · `--log-level debug|info|warn|error` · `--log-format text|json` · `--version` · `--skills` · `--skills-json`

## Buckets

SECRETS wins over all others. Classifier order:

| Bucket | Condition | Default action |
|---|---|---|
| `EXACT` | byte-identical | `drop` |
| `CRLF-ONLY` | equal after CRLF→LF | `drop` |
| `WHITESPACE-ONLY` | CRLF + trailing-ws trimmed + blank-line collapse | `drop` |
| `TAG-DELTA` | body equal; FM differs only in top-level `tags` | `merge-frontmatter` |
| `FRONTMATTER-ONLY` | body equal; FM differs beyond tags | `merge-frontmatter` |
| `FRONTMATTER-EQUAL` | FM equal; body differs | `review` |
| `APPEND-ONLY` | one side is CRLF-normalized prefix of other | `review` |
| `DIVERGED` | otherwise | `review` |
| `SECRETS` | credential pattern matches | `quarantine` |
| `UNIQUE` | lone basename | `keep` |

## Actions at apply time

`drop`: move `paths[0]` → trash · `merge-frontmatter`: union-merge into `paths[1]`, loser trashed, winner rewritten atomically, scalar/type conflicts refuse · `review`/`keep`/`quarantine`: no-op.

## Exit codes

`0` ok · `1` usage · `2` validation · `3` plan-required · `4` hash-drift · `5` refused.

## Artifacts (all JSONL files lead with a `{"type":"header","schema":"<n>/<v>"}` record)

| File | Schema | Key fields |
|---|---|---|
| `index.jsonl` | `index/4` | path, basename, size, byte_hash, content_hash, whitespace_hash, frontmatter_hash, frontmatter_no_tags_hash, body_hash, tags, aliases, has_secrets, secret_pattern |
| `classification.jsonl` | `classification/6` | type, bucket, basename, paths/path, referrer_count, secret_pattern |
| `plan.md` | `plan/1` | `- [ ] #<action> `<id>` — <paths>` checklist |
| `journal.jsonl` | `journal/1` | action_id, op, result (`applied`/`skipped`/`refused`/`dry-run`), path, secondary_path, trash_path, secondary_trash, content_hash, expected_hash, actual_hash, reason, secret_pattern |

## User config (`~/.config/obconverge/config`, YAML)

Precedence: defaults → this file → `-c` override (replaces) → CLI flags.

```yaml
vault_path: "~/Documents/Obsidian/Vault"
work_dir: ".obconverge"
apply_mode: "dry-run"       # dry-run | apply
rewrite_links: "ask"        # never | ask | always
delete_mode: "soft"         # soft | hard
log_level: "info"           # debug | info | warn | error
log_format: "text"          # text | json
tags_handling: "merge"      # replace | add | merge
aliases_handling: "merge"
properties_handling: "merge"
```

## Per-vault policy (`<vault>/.obconverge/policy.yaml`)

```yaml
rules:
  EXACT: drop
  # any bucket → any action
secret_response: block      # block | warn | silent
```

## Secret detector

Built-ins (names; matched bytes never leak): `anthropic` · `openai` · `aws-access-key` · `google-api` · `github-pat` · `github-fine` · `jwt` · `slack` · `pem`.

User extensions: `~/.config/obconverge/secret_patterns.json` (additive; names must not collide).

```json
{"patterns":[{"name":"corp-token","regex":"CORP-[A-Z0-9]{16}"}]}
```

## Safety invariants

- `scan`/`classify`/`plan` never mutate (enforced by `internal/invariants` import-graph test).
- SECRETS bytes never in any artifact, log, or plan.
- `apply` re-hashes before every mutation; drift → skip.
- `apply` refuses drops of linked uniques; pair drops are link-safe (basename preserved).
- All deletions soft; `undo` reverses any journal entry until `purge`.
