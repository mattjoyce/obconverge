# obconverge

**Status**: `v0.1.0-audit` — full pipeline shipped: `scan` → `classify` → `plan` → `apply` → `undo` → `purge`. 10 of 10 classifier buckets implemented.

A reconciliation CLI for Obsidian.

**→ [TUTORIAL.md](TUTORIAL.md)** — seven scenario-driven walk-throughs covering every feature.

## What it does today (audit mode)

Scans an Obsidian vault and produces three artifacts under `<vault>/.obconverge/`:

1. `index.jsonl` — every regular file with size, mod-time, SHA-256, CRLF-normalized content hash, body/frontmatter hashes, parsed `tags`/`aliases`, and a credential-shape flag.
2. `classification.jsonl` — for each basename that appears in multiple locations, a verdict from one of seven buckets:

   | Bucket | Meaning |
   |---|---|
   | `EXACT` | byte-identical |
   | `CRLF-ONLY` | differs only in line endings |
   | `FRONTMATTER-ONLY` | body identical, frontmatter differs |
   | `FRONTMATTER-EQUAL` | frontmatter identical, body differs |
   | `DIVERGED` | both differ non-trivially |
   | `SECRETS` | contains credential-shaped strings (wins over any other verdict) |
   | `UNIQUE` | lone occurrence |
   | `WHITESPACE-ONLY` | equal after trailing-space trimming + blank-line collapse |

3. `plan.md` — an Obsidian-friendly markdown checklist the operator reviews *in the editor*, with a stable action-ID per item. Re-running `plan` preserves the check state for items whose IDs still apply.

### Safety guarantee

- **No writes outside `.obconverge/`.** `TestRun_DoesNotMutateVault` snapshots the vault before and after each scan and fails if any byte changes.
- **Secrets are never printed** to stdout, stderr, log, or any artifact. The `secrets.Detect` API returns only a bool and a pattern name — the matching bytes never leave the function.
- **Protected paths** (`.obsidian/`, `.trash/`, `.git/`, `.obconverge/`, `.stfolder/`, `.sync/`) are skipped at walk time.

## What it does *not* do yet

- **`apply`** — the reconciliation half of the pipeline. No mutation. No soft-delete to trash. No frontmatter merges. No link rewrites.
- **`undo`** — requires a journal, which requires apply.
- **Wikilink / embed graph** — needed before apply can safely touch linked notes.
- **`TAG-DELTA` / `APPEND-ONLY` buckets** — policy accepts them for forward compatibility; classify doesn't emit them yet.
- **Tested at vault-scale** — largest test vault is 8 files. Behavior on 50k files or 10 MB attachments is unknown.

See [SPEC.md](SPEC.md) for the full design; what's built today is a subset.

## Install

```bash
git clone https://github.com/mattjoyce/obconverge.git
cd obconverge
make install   # -> $HOME/go/bin/obconverge
```

Or directly:

```bash
go install github.com/mattjoyce/obconverge/cmd/obconverge@latest
```

## Quick start

```bash
# Pure read: three commands, three artifacts, zero vault modifications.
obconverge scan --vault ~/Documents/Obsidian/MyVault
obconverge classify --vault ~/Documents/Obsidian/MyVault
obconverge plan --vault ~/Documents/Obsidian/MyVault

# Open the plan in Obsidian:
open "~/Documents/Obsidian/MyVault/.obconverge/plan.md"
```

Or with a config file at `~/.config/obconverge/config`:

```yaml
vault_path: "~/Documents/Obsidian/MyVault"
log_level: "info"
```

…then the `--vault` flag is optional.

## Extending the secret detector

Built-in credential patterns ship with the binary in `internal/secrets/patterns.json`. To add your own (for example, an internal company token format), drop a file at `~/.config/obconverge/secret_patterns.json`:

```json
{
  "patterns": [
    {"name": "corp-token", "regex": "CORP-[A-Z0-9]{16}", "description": "Internal corp tokens"}
  ]
}
```

Rules:

- User patterns are **additive**. They cannot remove or shadow built-ins.
- Names must be unique across built-ins and user patterns. Collisions are a hard error.
- Regex syntax is Go's `regexp` package (RE2). Invalid regex fails at CLI startup, not silently.

## Policy

`<vault>/.obconverge/policy.yaml` (optional) overrides the defaults:

```yaml
rules:
  EXACT: drop
  CRLF-ONLY: drop
  FRONTMATTER-ONLY: merge-frontmatter
  FRONTMATTER-EQUAL: review
  DIVERGED: review
  SECRETS: quarantine
  UNIQUE: keep

# How apply treats SECRETS-bucket files with a mutating action.
# Only relevant if you override the SECRETS rule to something other
# than quarantine. Default: block.
#   block   — refuse the action; journal records reason secrets_bucket
#   warn    — proceed; log a warning; journal stamps secret_pattern
#   silent  — proceed quietly; journal still stamps secret_pattern
secret_response: block
```

Unknown bucket names or action names are a hard error — better to fail loud than do the wrong thing to a vault. Per-run override: `obconverge apply --secrets warn`.

## Stance

Borrowed from Hickey, enforced in code:

- **Simple, not easy.** Four phases, four artifacts. Each is a value, not a state.
- **Effects at the edges.** `scan`, `classify`, `plan` are read-only. Only `apply` will mutate — and doesn't exist yet.
- **Decisions are data.** The plan is a markdown note. Approval is a ticked checkbox. No hidden state.
- **Policy, not mechanism.** The classifier computes; `policy.yaml` decides.

## Sibling

- [obsave](https://github.com/mattjoyce/obsave) — writes notes *into* a vault. obconverge copies its config pattern (YAML at `~/.config/<tool>/config`, four-layer precedence, handling modes).

## Development

```bash
make check      # gofmt + vet + golangci-lint + gosec + go test -race
make build      # ./obconverge
make install    # $HOME/go/bin/obconverge
```

All tests run against real filesystem fixtures in `t.TempDir()`. No mocks.

## For LLM agents

The binary self-describes via embedded descriptors so agents don't need to parse the README:

```bash
obconverge --skills       # markdown form (human-readable)
obconverge --skills-json  # JSON form (machine-readable)
```

The JSON descriptor enumerates subcommands, flags, classifier buckets, policy actions, exit codes, artifact schemas, and secret-pattern names. A drift test (`cmd/obconverge:TestCLI_DescriptorMatchesCobraTree`) fails CI if the descriptor disagrees with the actual cobra tree, so agents can trust it.

Agent-friendly behaviors:

- **stdout** is the artifact path on success; everything else (progress, warnings, errors) goes to **stderr** via `slog`.
- `--log-format json` switches stderr to structured JSON for parseable logs.
- **Exit codes** are typed (0 success, 1 usage, 2 validation, 3 plan-required, 4 hash-drift, 5 refused) and documented in the descriptor.
- **Artifacts are JSONL** with a schema-versioned header record; readers refuse unknown schemas (`ErrUnsupportedSchema`) so agents can version-pin.
- **Idempotent**: re-running any subcommand is safe. `plan` preserves checkbox state across runs via stable action IDs.

## Roadmap

- [x] `scan` — walk vault, emit index
- [x] `classify` — seven buckets, SECRETS-quarantine, real-filesystem tests
- [x] `plan` — policy-driven, checkbox-reviewable, re-entrant
- [x] `--skills` / `--skills-json` agent descriptor with drift test
- [x] Link referrer index — wikilink / embed / heading-ref / block-ref detection with alias resolution; surfaced in `classification.jsonl` and `plan.md`
- [x] `apply` (drop) — dry-run by default, `--execute` to mutate; hash-before-mutate with `hash_drift` skip; soft-delete to `.obconverge/trash/<timestamp>/`; refuses SECRETS (block/warn/silent modes) and linked notes; append-only journal
- [x] `apply` (merge-frontmatter) — union-merge of frontmatter keys; loser trashed, winner rewritten atomically; scalar/type conflicts refuse with `frontmatter_conflict`
- [x] `undo` — journal reversal: restore drops from trash and revert merge-frontmatter rewrites using the winner's pre-merge backup. Refuses to overwrite files the operator has edited post-apply.
- [x] `purge` — remove `.obconverge/trash/` entirely; marks the boundary beyond which undo cannot recover ("reversible until --purge").
- [x] Import-graph purity invariant test — `internal/invariants/purity_test.go` asserts scan/classify/plan/etc. never transitively import apply or undo.
- [x] Tightened linked-note refusal — pair drops now proceed (basename preserved by survivor); only unique-drops of linked files refuse
- [x] TAG-DELTA bucket — pairs whose frontmatter differs only in `tags`; same `merge-frontmatter` action as FRONTMATTER-ONLY but named precisely
- [x] APPEND-ONLY bucket — detects when one file is a CRLF-normalized byte-prefix of the other; default action `review` (operator decides which copy to keep)
- [ ] `--rewrite-links` — edit referrers when dropping a linked unique
- [ ] `TAG-DELTA` / `APPEND-ONLY` buckets
- [ ] Import-graph purity invariant test
