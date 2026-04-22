---
tags:
  - project
  - cli
  - obsidian
  - tooling
  - golang
  - spec
status: spec
created: 2026-04-21
revised: 2026-04-23
language: go
aliases:
  - obconverge
  - obconverge spec
---

# obconverge — a vault reconciliation CLI

## Problem

Vaults accrete duplicates. Mirror folders (`Prod/`, `Default/`), half-finished sync runs, template misuse, and AI-generated variants all leave the same note sitting in two or three places, slightly different each time. After a while a vault carries:

- **Exact duplicates** — byte-identical.
- **Near-duplicates** — CRLF drift, trailing whitespace, frontmatter reshuffle.
- **Diverged edits** — both sides carry unique content.
- **Orphaned uniques** — present in one subtree only.

The traditional shell loop — `find | md5sum | diff | rm` — re-discovers the *structure* of the vault on every run, drowns the operator in line-ending noise, and routinely reads secrets (see [[Fabric]]) into terminal scrollback. We can do simpler.

## Stance (borrowed from Hickey)

- **Simple, not easy.** `find + rm` is at hand and familiar — it is also complected. The dedup tool must separate *scanning*, *classifying*, *deciding*, and *mutating* into distinct phases.
- **Values, not places.** Each phase produces a value — a file on disk — not an in-memory mutation. You can pipe it, commit it, walk away overnight.
- **Decisions are data.** The plan is a markdown document. The operator reads it *in Obsidian* before approving it.
- **Effects at the edges.** Scan and apply touch the filesystem. Classify and plan do not. That is the only place we give up purity, and we give it up deliberately.
- **Policy, not mechanism.** The tool does not decide that "newer wins". It computes signals; policy is declared.

## Architecture

Four phases. Each takes a value and produces a value.

```
scan      : FS                                → index.jsonl
classify  : index.jsonl                       → classification.jsonl
plan      : classification.jsonl + policy     → plan.md              (human-reviewable)
apply     : plan.md (approved)                → FS + journal.jsonl   (append-only)
```

Every artifact lives under `.obconverge/` inside the vault. Nothing outside `.obconverge/` is touched until `apply`. `apply` is the only phase that writes to user notes, and every mutation is journaled.

**Purity invariant.** `scan`, `classify`, and `plan` are pure functions of their inputs — they may read the vault; they do not mutate it. Only `apply` mutates. This is not a style choice; it is the property that makes the plan reviewable, cacheable, and diffable. It is enforced by package boundaries: read-phase packages accept an `FSReader` interface that exposes no write methods, and an invariant test walks the import graph to fail any commit that smuggles a writer into a pure phase.

## Obsidian semantics the tool must respect

A vault is not a tree of bytes. It is a graph. This is where a generic dedup tool gets it wrong.

### The link graph is identity

A note is addressed by *its basename and its aliases*. `[[Foo]]` resolves to any note whose basename is `Foo.md` **or** whose frontmatter `aliases:` list contains `Foo`. Before deleting or renaming, the tool must:

1. Enumerate incoming wikilinks (`[[X]]`, `[[X|display]]`, `[[X#Heading]]`, `[[X#^block]]`).
2. Enumerate incoming embeds (`![[X]]`, `![[X#^block]]`).
3. Report a link-impact summary: *N incoming links, M embeds, K block-refs.*
4. If `--rewrite-links` is set, update referrers atomically with the move/delete.
5. If not, refuse the mutation and surface the referrer list for review.

Block refs (`^blockid`) and heading refs (`#Heading`) are durable identifiers. Merges must preserve them — they are load-bearing for notes elsewhere in the vault.

### Frontmatter is structured, not textual

YAML frontmatter must be parsed, not string-diffed. A *union merge* of two notes:

- keys unique to one side: keep.
- keys present on both with equal value: keep once.
- list-valued keys (`tags`, `aliases`): set-union.
- scalar conflicts: record both; escalate to human.

This bucket alone would have resolved the `Ryokunics Synaptic Bridge Technology.md` and `Terebiem-Ryokn Glossary.md` cases in the 2026-04-18 session without a single byte of human review. `diff` saw them as 100% different because a line-terminator flipped every byte; structurally the bodies were identical.

### Attachments travel with their referents

`Attachments/` files (images, PDFs, audio) are referenced by zero or more notes via `![[image.png]]`. Deleting a referenced attachment silently breaks a note without the editor noticing. The tool must:

- Treat attachments as first-class nodes in the graph.
- Flag *orphaned* attachments (zero incoming refs) as cleanup candidates.
- Refuse to drop a *referenced* attachment unless every referencer is also being dropped.

### Some folders are sacred

Default exclusions — mutable only with an explicit flag:

- `.obsidian/` — config, themes, plugins, workspace. Never touch.
- `.trash/` — Obsidian's own soft-delete.
- `Templates/` — template files must never be deduped *against content*. Content routinely starts life as a copy of a template; killing one kills the other.
- `.git/`, `.stfolder/`, `.sync/` — sync tooling state.

Encoded as built-in `protected-paths`. User can extend via `.obconverge/ignore`.

### Daily notes share scaffolding, not identity

`2025-04-18-Fri.md` and `2025-04-19-Sat.md` share the template scaffold. They are not duplicates. The classifier must fingerprint *content outside template boilerplate*, or exempt `Daily/` from similarity detection entirely. Policy-configurable.

### Canvas files are JSON

`.canvas` files describe a spatial board in JSON. They cannot be merged line-wise. Treat them as opaque: exact-match dedup only.

### Clippings carry provenance

Web clippings typically have `source:` in frontmatter. Two clippings with the same `source` but different fetch dates are *versions of the same external document* — their own bucket, distinct from normal duplicates.

## Classifier buckets

Given two files sharing a logical name (basename minus `.md`, optionally minus trailing ` N` / ` vN`):

| Bucket | Condition | Default action |
|---|---|---|
| `EXACT` | byte-identical | auto-drop one |
| `CRLF-ONLY` | equal after line-ending normalization | auto-drop one |
| `WHITESPACE-ONLY` | equal after whitespace normalization | auto-drop one |
| `FRONTMATTER-ONLY` | body identical, frontmatter differs | auto-union-merge |
| `FRONTMATTER-EQUAL` | frontmatter identical, body differs | human review |
| `TAG-DELTA` | only `tags` list differs | auto-union tags |
| `APPEND-ONLY` | one side is a prefix of the other | keep superset |
| `DIVERGED` | bodies differ non-trivially | human review |
| `SECRETS` | contains credential-shaped strings | **quarantine, never display** |
| `UNIQUE` | exists only in one subtree | relocate or keep |

The classifier is a *pure function* of the index — `Classify(pair) Bucket`. Policy is a separate function — `Decide(bucket, policy) Action`. They are different files because they change at different rates and for different reasons. The classifier's output is a JSONL file the operator can diff, commit, and re-run.

## Configuration

Copied from obsave because the operator already has the muscle memory — same family, same mental model, same `~/.config/` location. One less thing to learn.

**Location**: `~/.config/obconverge/config` — YAML, no extension. Paths inside the config use `~/` expansion.

### Four-layer precedence

Configuration is assembled top-down; each layer overrides the prior:

1. Hard-coded defaults in the binary — conservative and non-destructive (`apply_mode: dry-run`, `delete_mode: soft`, `rewrite_links: ask`).
2. Default config file at `~/.config/obconverge/config`, if present.
3. `-c/--config <name>` loads a named file from the same directory, *replacing* the default layer (not merged — matches obsave).
4. CLI flags override anything above.

Validation runs after assembly. Unknown enum values fall back to the safe default with a log line, not a panic. Same shape as obsave's `setDefaultsAndOverrides`.

### Config shape

```yaml
vault_path: "~/Documents/Obsidian/Personal1"
work_dir: ".obconverge"           # relative to vault_path
policy_file: "policy.yaml"         # relative to work_dir
ignore_file: "ignore"              # relative to work_dir

apply_mode: "dry-run"              # dry-run | apply
rewrite_links: "ask"               # never | ask | always
delete_mode: "soft"                # soft (-> .obconverge/trash/) | hard

log_level: "info"                  # debug | info | warn | error
log_format: "text"                 # text | json

tags_handling: "merge"             # replace | add | merge
aliases_handling: "merge"
properties_handling: "merge"
```

### Scope separation

Three distinct config surfaces, by scope. They don't overlap:

| Scope | Location | Contents |
|---|---|---|
| User CLI defaults | `~/.config/obconverge/config` | vault path, logging, handling modes |
| Per-vault policy | `<vault>/.obconverge/policy.yaml` | bucket → action, similarity thresholds, extra protected paths |
| Per-vault exclusions | `<vault>/.obconverge/ignore` | gitignore-syntax paths |

A second operator on the same vault gets their own `~/.config/obconverge/config` but inherits the same `.obconverge/policy.yaml`. Policy is a property of the vault, not the operator.

`policy.yaml` declares *what*. The classifier is the *how*. They are different files because they change at different rates.

### Handling modes

`replace | add | merge` — copied verbatim from obsave because the semantics fit Obsidian's list/map frontmatter exactly:

- **`replace`** — CLI value wins, config discarded.
- **`add`** — union, CLI values added only if not already present.
- **`merge`** — union, duplicates allowed.

One mechanism, two uses: driving CLI-vs-config resolution *and* the FRONTMATTER-ONLY / TAG-DELTA bucket's auto-merge. The vault policy can set `tags_handling: add` so classifier-driven tag merges dedupe automatically.

### Path expansion

Path-valued fields (`vault_path`, `work_dir`, `policy_file`, `ignore_file`) pass through the `expandAndCleanPath` helper from obsave: handles bare `~`, `~/foo`, `filepath.Clean`, `filepath.Abs`. The only place we silently mutate operator input.

## The plan is a markdown document

`obconverge plan` writes `.obconverge/plans/YYYY-MM-DD-HHMM.md` as an **Obsidian note**. The operator opens it in Obsidian, reads it, edits it, saves it. Each action is a checklist item with a stable action-id:

```markdown
- [ ] #drop `a1b2c3` — `Prod/Ryokunics Synaptic Bridge Technology.md`
      CRLF-only diff against `Ryokunics Synaptic Bridge Technology.md`
- [x] #merge-frontmatter `d4e5f6` — `Terebiem Group - Corporate Archaeology.md`
      Adds tag `neural-interface` from Prod copy
- [ ] #review `g7h8i9` — `Fabric.md`
      SECRETS bucket: 6 credential strings unique per side.
      Do NOT open this file's contents in the terminal.
```

Ticking the checkbox is the approval signal. `obconverge apply` reads only checked items. Unchecked items stay pending across runs.

The plan is a value. It has an author (the tool), a reviewer (the operator), a reader (`apply`). None of them share memory — the plan is the full interface between the decision phase and the effect phase. Running `scan → classify → plan` on one machine and `apply` on another must work; if it doesn't, the interface is dishonest.

This is a deliberate choice: the tool's UI *is the user's editor*. No custom TUI to learn. The plan is a note; it can be wikilinked, tagged, archived into `.obconverge/plans/`. It becomes part of the vault's own memory of its curation.

## Apply and the undo log

`obconverge apply` is the only destructive phase. Invariants:

1. No mutation without a *corresponding checked entry* in an approved plan.
2. Every mutation is journaled to `.obconverge/journal.jsonl` as an append-only log: timestamp, operation, source, target, content hash.
3. Deletions are soft by default: files move to `.obconverge/trash/<timestamp>/<original-path>`. Hard delete requires `--purge`.
4. Moves rewrite incoming links iff `--rewrite-links`; otherwise refuse to move notes with incoming links.
5. `obconverge undo <journal-id>` reverses one op. `obconverge undo --since <timestamp>` reverses a range.
6. Before each action, re-hash the source file. If it has changed on disk since the plan was written, skip and report. No TOCTOU.

**Identity vs place.** `apply` never trusts place. Before each action it re-reads the source file, recomputes its content hash, and proceeds only if the hash matches the one in the plan. The filesystem is a place; the hash is the value. `ErrHashDrift` fires the instant the two diverge.

**Schema version on artifacts.** The first record in every JSONL artifact is a header: `{"type":"header","schema":"index/1","created":"..."}`. Readers reject unknown schemas with a clear message. Cheap now; painful to retrofit later.

## Secret protection

Vaults routinely carry API keys and tokens (cf. [[Fabric]]). The tool must:

- Detect common credential shapes: OpenAI `sk-`, Anthropic `sk-ant-`, AWS `AKIA…`, Google `AIza…`, GitHub `ghp_`, JWTs, generic high-entropy strings over a length threshold.
- Route any file containing such a string into the `SECRETS` bucket, regardless of other similarity signals.
- In plan output, **emit only the bucket, a redacted fingerprint, and the file path.** Never the secret itself.
- Refuse to print secret content to stdout, log, or plan under any flag. For `SECRETS` merges, the tool opens both files in Obsidian side-by-side and steps out.

This is precisely the failure mode the traditional `cat`/`diff` loop produces. It is a safety rail that generic dedup tools lack.

## Protection invariants (one-line summary)

Stated flatly so a reviewer can audit the implementation:

1. No write outside `.obconverge/` before `apply`.
2. No read into stdout/log/plan from files classified `SECRETS`.
3. No mutation of a file whose hash has changed since plan.
4. No deletion of a referenced attachment.
5. No move of a linked note without `--rewrite-links`.
6. No recursion into protected paths without explicit opt-in.
7. Every apply produces a journal entry; every journal entry is reversible until `--purge`.
8. `scan`, `classify`, `plan` do not transitively import any FS writer.

## Invariant tests

The spec's stance lives in `test/invariants/`. Each protection invariant and each stance claim has a corresponding test. Removing a stance requires removing or updating its test. **The tests are the spec's teeth — without them the stance is voice, not contract.**

Examples:

- `TestPurityImportGraph`: walks the import graph from each read-phase package; fails if any transitively depends on `os.Create`, `os.Rename`, `os.Remove`, `os.WriteFile`.
- `TestHashDriftRefused`: writes a plan, mutates the source file, runs apply, asserts `ErrHashDrift` and zero writes to the vault.
- `TestPlanIsInterface`: runs `scan → classify → plan` in one process, copies `plan.md` + vault to a `t.TempDir()`, runs `apply` against the copy, asserts success with no shared in-memory state.
- `TestSchemaMismatchRefused`: reads an artifact with `schema: index/99`, asserts reader returns `ErrUnsupportedSchema` and consumes zero further records.
- `TestSecretsNeverPrinted`: runs plan against a fixture containing known credential patterns; asserts no substring of the secrets appears in stdout, stderr, or plan file.
- `TestLinkedNoteMoveRefused`: classifies a note with incoming wikilinks, plans a move without `--rewrite-links`, asserts refusal at apply time.

## Skills descriptor

obconverge ships its CLI-LX descriptor embedded in the binary, same as obsave.

```go
//go:embed skills/obconverge.lx.json
var skillsJSONPayload []byte

//go:embed skills/obconverge.lx.md
var skillsMarkdownPayload []byte
```

Exposed via top-level `--skills` (markdown) and `--skills-json` (JSON) flags that dump the descriptor to stdout and exit 0. No vault access, no config load — runs before any side-effecting code path.

**Descriptor contents**:

- Subcommands: `scan`, `classify`, `plan`, `apply`, `undo`, `similar` — each with description and flag surface.
- Classifier buckets: the full table from the Classifier buckets section, each with default action.
- Exit codes: 0 ok, 1 usage, 2 validation, 3 plan-required, 4 hash-drift, 5 refused (linked note, referenced attachment, protected path).
- Artifact schemas: `index.jsonl`, `classification.jsonl`, `plan.md`, `journal.jsonl`.

**Why embed**:

1. Agents (PAI, Claude Code) auto-discover capability without the README.
2. Wrappers parse the JSON to build typed interfaces.
3. `obconverge --skills | less` is the fastest re-learn after time away.

The descriptor travels with the binary. Version skew between "what the agent thinks the tool does" and "what this binary actually does" is impossible by construction.

**Ground rules**:

- Descriptor is authored by hand under `skills/` at repo root, version-controlled.
- CI lints both files for validity (JSON schema + CLI-LX markdown).
- Descriptor drift from the actual flag surface is a bug — a test parses `skills/obconverge.lx.json` and asserts every declared subcommand/flag exists in the cobra command tree, and vice versa.

## Go implementation notes

The data shapes and boundaries *are* the spec.

- **CLI framework**: `cobra`. Subcommands (`scan`, `classify`, `plan`, `apply`, `undo`, `similar`) and top-level `--skills`/`--skills-json` justify it.
- **Config**: `gopkg.in/yaml.v3` (matches obsave). Single `Config` struct with `yaml:"..."` tags. Four-layer precedence per the Configuration section.
- **YAML frontmatter**: same `yaml.v3`. Preserves key order and comments. Do *not* round-trip through an unordered `map` unless you can accept losing order.
- **Markdown parsing**: `yuin/goldmark` with a custom extension for wikilinks — `[[...]]`, `![[...]]`, `^block`, `#heading`. Wikilinks are not CommonMark; no existing extension handles the full surface cleanly.
- **Hashing**: SHA-256 for byte-identity, plus a second content-only SHA-256 (frontmatter stripped, line endings normalized) for semantic-identity. Cheap; stored in the index.
- **Walk**: `io/fs.WalkDir` with an ignore-file (`.obconverge/ignore`, gitignore syntax).
- **Artifacts**: JSON-lines. `encoding/json`, one object per line, `json.Encoder` with a non-indented encoder. First record is always a schema header. Append-only for `journal.jsonl`; `fsync` after each append. Never rewritten.
- **Logging**: `log/slog` with `log_level` and `log_format` (`text` | `json`) from config. Never log secrets.
- **Concurrency**: scan is embarrassingly parallel. Apply is strictly serial. Do not parallelize apply.
- **Skills**: `//go:embed skills/obconverge.lx.json` and `skills/obconverge.lx.md` exposed via top-level `--skills` / `--skills-json` flags. Dumps and exits 0 — no vault access, no config load. A test parses the JSON and asserts every subcommand/flag matches the cobra command tree.
- **Package layout**:
  - `cmd/obconverge/main.go` — cobra root, flag wiring, thin.
  - `internal/scan` — walks FS, emits `index.jsonl`. Accepts `FSReader`.
  - `internal/classify` — pure function from index to classification.
  - `internal/plan` — pure function from classification + policy to plan.md.
  - `internal/apply` — the only writer. Imports real `os` package directly.
  - `internal/policy` — YAML load + validation.
  - `internal/hashing` — content and semantic hashes.
  - `internal/wikilink` — goldmark extension + referrer index.
  - `internal/secrets` — credential detection.
  - `internal/artifact` — JSONL reader/writer with schema versioning.
  - `test/invariants/` — the tests that make the stance real.
- **No TUI framework.** Plan-as-markdown eliminates the question.
- **Dependencies to avoid**: anything that brings a TUI, anything that requires a daemon, anything that needs network. This is a file tool.

## Non-goals

- Not a sync tool. Does not push/pull.
- Not an Obsidian plugin. Runs as a separate CLI against the vault.
- Not a semantic-similarity tool. All classifiers are exact or normalized comparisons. A future opt-in `obconverge similar` could use embeddings; v1 does not.
- Not a backup tool. Assumes the operator has one. The journal is a partial safety net, not a substitute.

## Serving the Obsidian user

What `obconverge` offers a vault-keeper that a shell loop does not:

- **Confidence to run it.** Everything is reversible until `--purge`.
- **A record.** The plan and journal *are notes*. They accrete into the vault's own history of its own curation.
- **Respect for the graph.** Links don't silently break.
- **Respect for secrets.** Keys never hit terminal scrollback.
- **Leverage on recurring problems.** Mirror folders, CRLF drift, frontmatter churn — each becomes a classifier case, not a one-off script.

## Open questions

- Do we surface a "similar-name" heuristic (e.g. `Metaphor as API (previous).md` vs `Metaphor as API (latest).md`), or stay strictly name-equal? *Leaning*: separate subcommand `obconverge similar`, opt-in.
- Canvas merge — worth doing, or hard-pass? *Leaning*: hard-pass for v1.
- Cross-vault reconciliation (two *different* vaults, not two subtrees of one)? *Leaning*: out of scope for v1.

## Related

- [[Fabric]] — the note that motivated the `SECRETS` bucket.
- [[Prod]] — the mirror folder that triggered this project.
- [[MOC-Projects]]
- [[Agent Coding Standards - GoLang]] — the Go conventions this spec follows.
- [[24-10-110 obsave-cli]] — sibling tool; obconverge copies its config pattern.

---

*Next step: hand this spec and the data shapes to the Go engineer. The real risk surfaces are the wikilink extractor, the secret detector, and the import-graph invariant test; the rest is straightforward I/O.*
