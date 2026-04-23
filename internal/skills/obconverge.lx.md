# obconverge — capability descriptor

This descriptor is embedded in the binary and emitted by `obconverge --skills` (this file) or `obconverge --skills-json` (machine-readable). Agents should prefer the JSON form; humans read this one.

**Tool**: obconverge
**Version**: v0.1.0-audit
**Status**: read-only. `scan`, `classify`, `plan` are implemented. `apply` and `undo` are not yet built.

## Invocation shape

```
obconverge [persistent-flags] <subcommand> [subcommand-flags]
```

stdout prints the artifact path on success. Logs go to stderr (slog, filterable by `--log-level`, formattable by `--log-format`).

## Subcommands

### `scan`

Walks the vault and emits `<vault>/<work_dir>/index.jsonl`. Pure read.

| Flag | Type | Notes |
|---|---|---|
| `--vault <path>` | string | Overrides `config.vault_path` |
| `--output`, `-o <path>` | string | Overrides default output location |

### `classify`

Groups index entries by basename and assigns each pair a bucket.

| Flag | Type | Notes |
|---|---|---|
| `--vault <path>` | string | |
| `--index <path>` | string | Defaults to `<vault>/<work_dir>/index.jsonl` |
| `--output`, `-o <path>` | string | |

### `plan`

Consumes classification + policy; writes `<vault>/<work_dir>/plan.md` for human review in Obsidian.

| Flag | Type | Notes |
|---|---|---|
| `--vault <path>` | string | |
| `--classification <path>` | string | |
| `--policy <path>` | string | Missing file is fine — defaults apply |
| `--output`, `-o <path>` | string | |

Re-entrant: running `plan` over an existing `plan.md` preserves checkbox state for actions whose IDs still appear.

Action IDs are a 12-hex SHA-256 prefix of (bucket, type, sorted paths). Stable across runs.

## Persistent flags (all subcommands)

| Flag | Notes |
|---|---|
| `--config`, `-c <path>` | Override config file; replaces user layer (does not merge) |
| `--log-level <level>` | `debug` / `info` / `warn` / `error` |
| `--log-format <fmt>` | `text` / `json` |
| `--version` | Print version and exit |
| `--skills` | Print this markdown descriptor and exit |
| `--skills-json` | Print the JSON descriptor and exit |

## Classifier buckets

| Bucket | Condition | Default action | Implemented |
|---|---|---|---|
| `EXACT` | byte-identical | `drop` | yes |
| `CRLF-ONLY` | equal after CRLF→LF | `drop` | yes |
| `WHITESPACE-ONLY` | equal after whitespace collapse | `drop` | no |
| `FRONTMATTER-ONLY` | body identical, frontmatter differs | `merge-frontmatter` | yes |
| `FRONTMATTER-EQUAL` | frontmatter identical, body differs | `review` | yes |
| `TAG-DELTA` | only `tags` list differs | `merge-frontmatter` | no |
| `APPEND-ONLY` | one side is a byte-prefix of the other | `drop` | no |
| `DIVERGED` | non-trivial differences | `review` | yes |
| `SECRETS` | contains credential-shaped string; **wins over any other bucket** | `quarantine` | yes |
| `UNIQUE` | single occurrence of the basename | `keep` | yes |

## Actions

| Action | Effect at apply time |
|---|---|
| `drop` | Move the losing copy to `.obconverge/trash/` |
| `merge-frontmatter` | Union-merge frontmatter keys; drop the losing copy |
| `review` | Human-only; apply never touches |
| `quarantine` | SECRETS only; apply opens both files in Obsidian and steps out |
| `keep` | No-op |

## Exit codes

| Code | Meaning |
|---|---|
| `0` | success |
| `1` | usage error |
| `2` | validation error (bad config, bad policy, bad vault path) |
| `3` | plan required (reserved for `apply`) |
| `4` | hash drift since plan was written (reserved for `apply`) |
| `5` | refused by safety invariant (reserved for `apply`) |

## Artifacts

All JSONL artifacts begin with a header record: `{"type":"header","schema":"<name>/<version>","created":"<RFC3339>"}`. Readers reject unknown schemas with `ErrUnsupportedSchema`.

| Artifact | Schema | Shape |
|---|---|---|
| `index.jsonl` | `index/2` | One `Entry` per regular file: path, basename, size, mod_time, byte_hash, content_hash, frontmatter_hash, body_hash, tags, aliases, has_secrets, secret_pattern |
| `classification.jsonl` | `classification/2` | One `Record` per pair or unique: type (`pair`/`unique`), bucket, basename, paths or path, secret_pattern |
| `plan.md` | `plan/1` | Obsidian frontmatter + markdown checklist; each item: `- [ ] #<action> `<id>` — paths` with a description line |

## Config file

**Location**: `~/.config/obconverge/config` (YAML).

**Precedence**:
1. Hard-coded safe defaults (every enum defaults conservatively)
2. User config at `~/.config/obconverge/config` (optional)
3. `--config/-c` override file (replaces user layer, does not merge)
4. CLI flag overrides

**Fields**:

```yaml
vault_path: "~/Documents/Obsidian/MyVault"
work_dir: ".obconverge"
policy_file: "policy.yaml"
ignore_file: "ignore"
apply_mode: "dry-run"          # dry-run | apply
rewrite_links: "ask"           # never | ask | always
delete_mode: "soft"            # soft | hard
log_level: "info"              # debug | info | warn | error
log_format: "text"             # text | json
tags_handling: "merge"         # replace | add | merge
aliases_handling: "merge"
properties_handling: "merge"
```

## Secret detector patterns

| Pattern name | Matches |
|---|---|
| `anthropic` | `sk-ant-...` |
| `openai` | `sk-...` |
| `aws-access-key` | `AKIA[0-9A-Z]{16}` |
| `google-api` | `AIza[A-Za-z0-9_\-]{35}` |
| `github-pat` | `ghp_[A-Za-z0-9]{30,}` |
| `github-fine` | `github_pat_[A-Za-z0-9_]{30,}` |
| `jwt` | three base64url segments |
| `slack` | `xox[baprs]-...` |
| `pem` | `-----BEGIN (RSA \| EC \| OPENSSH \| ...) PRIVATE KEY-----` |

The matched bytes are **never** returned by `secrets.Detect`, never logged, and never written to any artifact. Only the pattern name is recorded.
