# obconverge

**Status**: `v0.1.0-audit` — read-only. Does not modify your vault yet.

A reconciliation CLI for Obsidian. Today it audits. Later it reconciles.

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
```

Unknown bucket names or action names are a hard error — better to fail loud than do the wrong thing to a vault.

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

## Roadmap

- [x] `scan` — walk vault, emit index
- [x] `classify` — seven buckets, SECRETS-quarantine, real-filesystem tests
- [x] `plan` — policy-driven, checkbox-reviewable, re-entrant
- [ ] `apply` — hash-before-mutate, journal every op, soft-delete to `.obconverge/trash/`
- [ ] Wikilink + embed graph (enables safe linked-note moves)
- [ ] `undo` — journal reversal
- [ ] `TAG-DELTA` / `APPEND-ONLY` buckets
- [ ] `--skills` / `--skills-json` agent descriptor
- [ ] Import-graph purity invariant test
