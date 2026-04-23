# obconverge tutorial

A walk-through built around real scenarios. Each story covers a feature (or combination of features), shows the exact commands, and explains the reasoning. Copy-paste should work verbatim once you substitute your vault path.

> **Before you start**: obconverge can modify files in your vault. Every mutation is reversible until you run `purge`, but the safest first experience is still a **copy** of your real vault. The commands below use `~/vault-demo` — substitute your own path.

---

## Install

```bash
git clone https://github.com/mattjoyce/obconverge.git
cd obconverge
make install                       # -> $HOME/go/bin/obconverge
obconverge --version               # expect: v0.1.0-audit
```

Or directly:

```bash
go install github.com/mattjoyce/obconverge/cmd/obconverge@latest
```

Optional one-time config so you can omit `--vault` everywhere:

```bash
mkdir -p ~/.config/obconverge
cat > ~/.config/obconverge/config <<'YAML'
vault_path: "~/vault-demo"
log_level: "info"
YAML
```

---

## Story 1 — "My vault has accumulated duplicates"

**Who**: You've synced between two or three machines for years. `Prod/` and `Default/` subtrees drifted out of sync; there are byte-identical copies, CRLF-only differences, and a handful of notes with tag mismatches.

**What you want**: An inventory, a safe cleanup path, and an undo button.

### Step 1 — Audit (read-only)

```bash
obconverge scan
obconverge classify
```

Two artifacts now exist under `~/vault-demo/.obconverge/`:

- `index.jsonl` — one record per vault file with hashes and metadata.
- `classification.jsonl` — one record per pair-of-duplicates or unique file.

Nothing in the vault was touched. `git status` in your vault shows zero changes outside `.obconverge/`.

Quick sense-check:

```bash
jq -r '.bucket' ~/vault-demo/.obconverge/classification.jsonl | sort | uniq -c
# 12 CRLF-ONLY
#  3 DIVERGED
#  1 EXACT
#  2 FRONTMATTER-ONLY
#  1 SECRETS
#  4 TAG-DELTA
# 284 UNIQUE
```

### Step 2 — Produce a reviewable plan

```bash
obconverge plan
open ~/vault-demo/.obconverge/plan.md
```

The plan is an Obsidian note. It opens cleanly in the editor with a YAML frontmatter header and sections per bucket, SECRETS first:

```markdown
## SECRETS — `#quarantine`

- [ ] #quarantine `f71314f96e2b` — `Keys.md`
      SECRETS (aws-access-key): do NOT open this file's contents in the terminal.

## EXACT — `#drop`

- [ ] #drop `614d02d002bb` — `Notes/Alpha.md` ↔ `Prod/Alpha.md`
      Byte-identical duplicates. Referrers: 3 incoming links.
```

Review each item. Tick the checkboxes for actions you approve; leave the rest unchecked.

> **Tip**: re-running `obconverge plan` preserves your ticks. Action IDs are stable 12-hex SHA-256 prefixes of (bucket, type, sorted paths), so only items that genuinely changed get reset.

### Step 3 — Dry-run

```bash
obconverge apply
# dry-run: would_apply=15 skipped=0 refused=1 unchecked=27  (pass --execute to mutate)
```

Dry-run is the default. It reports outcomes without moving any files.

### Step 4 — Execute

```bash
obconverge apply --execute
# apply complete: applied=15 skipped=0 refused=1 unchecked=27
```

- **applied=15** — 15 files moved to `.obconverge/trash/<timestamp>/`.
- **refused=1** — one item refused (usually SECRETS or a linked unique). Check the journal:

```bash
jq 'select(.result=="refused")' ~/vault-demo/.obconverge/journal.jsonl
```

### Step 5 — Change your mind

```bash
obconverge undo --execute
# undo complete: restored=15 skipped=0
```

Every trashed file is back at its original path. Every merged frontmatter is rolled back to its pre-merge state. The vault is byte-identical to before `apply`.

### Step 6 — Commit permanently

When you're satisfied:

```bash
obconverge purge --execute
# purge complete: removed=15 files, 24576 bytes
```

After `purge`, trash is gone — `undo` can't help. This is the "reversible until `--purge`" boundary from the spec.

---

## Story 2 — "I left an API key in a note"

**Who**: Anyone who copies shell transcripts into notes. Anyone with a tutorial file showing `export OPENAI_API_KEY=...`.

**What you want**: Find every file with credential-shaped content. Don't let the tool display the secrets. Decide what to do.

### Finding them

obconverge's classifier routes files matching any credential pattern to the `SECRETS` bucket, regardless of any other similarity signal.

```bash
obconverge scan && obconverge classify
jq 'select(.bucket=="SECRETS")' ~/vault-demo/.obconverge/classification.jsonl
```

Output:

```json
{"type":"unique","bucket":"SECRETS","basename":"Setup.md","path":"Setup.md","secret_pattern":"anthropic"}
{"type":"unique","bucket":"SECRETS","basename":"AWS-notes.md","path":"AWS-notes.md","secret_pattern":"aws-access-key"}
```

The credential itself is **never** in any obconverge output — plan.md, journal.jsonl, stderr logs, terminal — all only see the pattern name. A test (`TestSecretsNeverLeakContent`) proves the bytes never appear in any artifact.

### What apply does with them

By default, SECRETS files map to `quarantine` in the policy — apply is a no-op. You see them flagged at the top of the plan, and you handle them manually:

1. Open the file in Obsidian.
2. Rotate the credential at its source (OpenAI dashboard, AWS console, etc.).
3. Replace the live credential in the note with a placeholder.

### If you want to deduplicate credential files anyway

Sometimes a SECRETS pair is two copies of the same tutorial note, and you want to consolidate. Override the per-vault policy and the response mode:

```yaml
# ~/vault-demo/.obconverge/policy.yaml
rules:
  SECRETS: drop
secret_response: warn    # proceed, but log a warning
```

```bash
obconverge apply --execute
# time=... level=WARN msg="apply: proceeding on SECRETS file" path=... pattern=anthropic
# apply complete: applied=1 ...
```

The three secret-response modes are `block` (default — refuse), `warn` (proceed with a stderr warning), and `silent` (proceed quietly). The journal always stamps `secret_pattern` for audit, regardless of mode — silent suppresses operator noise, not the audit trail.

---

## Story 3 — "Same note in two places, different frontmatters"

**Who**: You merged your work and personal vaults, or your `Prod/` and `Default/` subtrees drifted in their tagging.

**What you want**: Keep one canonical copy, don't lose the union of tags and aliases.

### What obconverge does

When two files have the same body but different frontmatter, obconverge classifies them as:

- **`TAG-DELTA`** — frontmatter differs *only* in the top-level `tags` key.
- **`FRONTMATTER-ONLY`** — frontmatter differs in other ways too.

Both default to `merge-frontmatter`: union-merge the frontmatter, drop the losing copy.

### Walkthrough

Start with:

```yaml
# Notes/Project.md (loser — lexicographically first)
---
title: Project X
tags:
  - active
  - personal
---
Body text shared between both files.

# Prod/Project.md (winner — lexicographically second)
---
title: Project X
tags:
  - active
  - archived
source: https://example.com
---
Body text shared between both files.
```

Pipeline:

```bash
obconverge scan && obconverge classify && obconverge plan
```

Plan.md shows:

```markdown
## FRONTMATTER-ONLY — `#merge-frontmatter`

- [ ] #merge-frontmatter `abc123def456` — `Notes/Project.md` ↔ `Prod/Project.md`
      Bodies identical; frontmatter differs. Referrers: 3 incoming links.
```

Tick the box. Apply:

```bash
obconverge apply --execute
```

Result at `Prod/Project.md`:

```yaml
---
title: Project X
tags:
  - active           # winner's order preserved
  - archived
  - personal         # loser's unique tag appended (set-union)
source: https://example.com  # loser-only key grafted on
---
Body text shared between both files.
```

`Notes/Project.md` is now in `.obconverge/trash/<ts>/Notes/Project.md`. The winner's pre-merge state is backed up to `.obconverge/trash/<ts>/Prod/Project.md` — `undo` restores both.

### If the merge conflicts

If the files have **different scalar values** for the same key (e.g., `title: X` vs `title: Y`), the merge refuses:

```
# dry-run output:
# dry-run: would_apply=0 skipped=0 refused=1 unchecked=0
# journal reason: frontmatter_conflict
```

Resolve it manually in Obsidian (decide which value wins), then re-run the pipeline.

---

## Story 4 — "One copy of a note is the other plus more"

**Who**: You archive notes periodically by copying them, then keep writing in the "live" one. The live version is the archive + more content.

**What obconverge does**: The `APPEND-ONLY` bucket fires when one file's CRLF-normalized content is a strict byte-prefix of the other's.

```bash
obconverge scan && obconverge classify
jq 'select(.bucket=="APPEND-ONLY")' ~/vault-demo/.obconverge/classification.jsonl
```

Default action is `review` — the plan flags it; you decide which copy to keep. This is deliberately conservative: auto-dropping the shorter file is almost always correct, but "almost always" isn't a safety bar worth defaulting to.

To override and auto-drop the shorter copy:

```yaml
# ~/vault-demo/.obconverge/policy.yaml
rules:
  APPEND-ONLY: drop   # drops paths[0] (lexicographically first)
```

Note: `drop`'s victim is lexicographically first, which may or may not be the shorter file in your layout. Inspect before ticking.

---

## Story 5 — "Our team has custom credential formats"

**Who**: You work at a company with internal token shapes that obconverge doesn't know about (`CORP-xxxxxxxx`, `STAGING.xxxxx`, etc.).

**What you want**: Add detectors without forking the tool.

### Add a user-pattern file

```bash
mkdir -p ~/.config/obconverge
cat > ~/.config/obconverge/secret_patterns.json <<'JSON'
{
  "patterns": [
    {
      "name": "corp-token",
      "regex": "CORP-[A-Z0-9]{16}",
      "description": "Internal corp API tokens"
    },
    {
      "name": "staging-jwt",
      "regex": "STAGING\\.[A-Za-z0-9._-]{50,}",
      "description": "Staging JWTs"
    }
  ]
}
JSON
```

From the next `scan` onward, files matching these patterns route to `SECRETS` alongside the built-in detections.

### Rules

- **Additive**: user patterns are appended to built-ins. A file matching a user pattern goes to SECRETS even if it wouldn't match any built-in.
- **Never shadow**: a user pattern name that collides with a built-in (or with another user pattern) is a hard error — obconverge refuses to start. Users extend the detector, never replace it.
- **Regex is Go's RE2**. Invalid regex fails at startup, not silently at match time.

### Verify

```bash
obconverge scan && obconverge classify
jq 'select(.secret_pattern=="corp-token")' ~/vault-demo/.obconverge/classification.jsonl
```

Agents can discover these via `obconverge --skills-json | jq .secret_patterns_source` — the descriptor documents the extension path and schema.

---

## Story 6 — "I want an LLM agent to drive obconverge"

**Who**: You're wiring obconverge into an agent workflow — a coding assistant that manages notes, a Slack bot that audits the team vault, a cron-driven curator.

**What you want**: Machine-readable capability discovery. Structured output. Typed exit codes. Guardrails that stop an agent from doing anything it can't undo.

### Capability discovery

```bash
obconverge --skills-json | jq .
```

The JSON descriptor enumerates:

- Every subcommand and flag (with type and required/optional).
- Every classifier bucket (condition, default action, implementation status).
- Every policy action (with effect description).
- Every exit code.
- Every artifact name, schema version, and field list.
- Every secret-pattern name.
- Where the operator can add user extensions.

A drift test in CI (`cmd/obconverge:TestCLI_DescriptorMatchesCobraTree`) asserts the descriptor matches the actual cobra command tree — if someone adds a flag and forgets the descriptor, the test goes red. Agents can trust it.

### Parsing output

Every JSONL artifact begins with a schema-versioned header. Agents can version-pin:

```python
import json

with open('.obconverge/classification.jsonl') as f:
    header = json.loads(next(f))
    assert header['type'] == 'header'
    assert header['schema'].startswith('classification/'), f"unknown schema {header['schema']}"
    for line in f:
        record = json.loads(line)
        if record['bucket'] == 'SECRETS':
            alert(f"Found {record['secret_pattern']} in {record.get('path') or record['paths']}")
```

### Exit codes

| Code | Meaning | Agent reaction |
|---|---|---|
| `0` | success | parse output, continue |
| `1` | usage error | fix flags, retry |
| `2` | validation error (bad config / policy / vault path) | inspect stderr, fix inputs |
| `3` | plan required | run `plan` first |
| `4` | hash drift since plan was written | re-run `scan` → `classify` → `plan` |
| `5` | refused by safety invariant | inspect journal; human decision needed |

### Structured logs

```bash
obconverge apply --execute --log-format json --log-level info 2> apply.log
```

stderr becomes newline-delimited JSON slog records. stdout stays clean — just the artifact path.

### Guardrails baked in

An agent can't accidentally:

- **Mutate the vault on the wrong command.** Dry-run is the default for `apply`, `undo`, `purge`. `--execute` is explicit.
- **Operate on stale data.** `apply` re-hashes every file before mutating; drift is a skip, not a silent overwrite.
- **Orphan references.** Dropping a linked unique is refused (exit 5). Pair drops are link-safe (basename preserved by the survivor).
- **Leak credentials.** SECRETS bytes never appear in any artifact, log, or agent-visible output.
- **Delete irreversibly.** `undo` works until `purge`. `purge` requires its own `--execute`.

---

## Story 7 — "I want a weekly read-only audit"

**Who**: You want visibility into vault drift over time. No mutations. Metrics to a dashboard.

```bash
#!/usr/bin/env bash
# ~/bin/obconverge-audit.sh
set -euo pipefail

VAULT="$HOME/vault-demo"
obconverge scan --vault "$VAULT" --log-level error >/dev/null
obconverge classify --vault "$VAULT" --log-level error >/dev/null

CLASS="$VAULT/.obconverge/classification.jsonl"

# Skip the header record (first line is {"type":"header",...}).
for bucket in EXACT CRLF-ONLY WHITESPACE-ONLY TAG-DELTA FRONTMATTER-ONLY \
              FRONTMATTER-EQUAL APPEND-ONLY DIVERGED SECRETS UNIQUE; do
    count=$(jq -r --arg b "$bucket" 'select(.type!="header" and .bucket==$b) | .bucket' "$CLASS" | wc -l)
    echo "obconverge_bucket_count{vault=\"demo\",bucket=\"$bucket\"} $count"
done
```

Run via cron; pipe to your metrics collector. Nothing mutates; there's nothing to undo.

If you also want to watch for secret leaks:

```bash
jq -r 'select(.type!="header" and .bucket=="SECRETS") | .secret_pattern' "$CLASS" \
    | sort | uniq -c
```

Alert if any count goes non-zero.

---

## Reference

### Artifact layout

```
<vault>/
  .obconverge/
    index.jsonl              ← scan output
    classification.jsonl     ← classify output
    plan.md                  ← plan output (Obsidian-reviewable)
    policy.yaml              ← optional per-vault policy
    ignore                   ← optional gitignore-syntax exclusions
    journal.jsonl            ← apply output (audit log)
    journal.jsonl.*.bak      ← previous apply runs
    trash/
      <YYYYMMDD-HHMMSS>/     ← one directory per apply run
        <original-path>      ← soft-deleted or backup copies
```

### Config locations by scope

| Purpose | Path | Scope |
|---|---|---|
| User CLI defaults | `~/.config/obconverge/config` | per-operator |
| User secret patterns | `~/.config/obconverge/secret_patterns.json` | per-operator |
| Per-vault policy | `<vault>/.obconverge/policy.yaml` | per-vault |
| Per-vault ignore | `<vault>/.obconverge/ignore` | per-vault |

### Safety invariants (tested)

1. `scan`/`classify`/`plan` never mutate the vault — enforced by `internal/invariants/purity_test.go`.
2. SECRETS content never appears in any artifact, log, or plan — asserted by `TestSecretsNeverLeakContent`.
3. `apply` re-hashes before every mutation; drift is a skip, not a silent overwrite.
4. `apply` refuses drops of linked uniques; pair drops are link-safe.
5. All deletions are soft (→ trash); `undo` reverses any journal entry until `purge`.
6. Policy is data (`policy.yaml`); mechanism is code. The classifier is policy-free.

### Further reading

- [SPEC.md](SPEC.md) — the design, stance, and invariant rationale.
- [README.md](README.md) — status and roadmap.
- `obconverge --skills` — condensed operator reference.
- `obconverge --skills-json` — machine-readable capability descriptor.
