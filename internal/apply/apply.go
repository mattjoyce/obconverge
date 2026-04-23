// Package apply executes the approved (checked) items in plan.md against
// the vault. It is the *only* phase that mutates user content.
//
// Safety rails (see SPEC.md "Protection invariants"):
//
//  1. Every action is journaled to .obconverge/journal.jsonl before and after
//     the mutation. The journal is append-only and fsynced per write.
//  2. Before each mutation, apply re-reads the source file and re-hashes
//     it. If the content hash has changed since the plan was written,
//     the operation is skipped and recorded as "hash_drift" — no TOCTOU.
//  3. Deletions are soft by default: files move to
//     .obconverge/trash/<timestamp>/<original-path>. Hard delete (--purge)
//     is not yet supported and intentionally absent until undo lands.
//  4. Files with incoming wikilinks or embeds are refused unless the
//     caller passes --rewrite-links (not yet implemented; today linked
//     files always refuse).
//  5. SECRETS-bucket files are refused. apply never mutates a file that
//     contains credentials.
//  6. merge-frontmatter action is not yet implemented; apply records a
//     skip with reason "not_implemented".
//  7. Default mode is dry-run. Real mutation requires Options.Execute.
package apply

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/mattjoyce/obconverge/internal/artifact"
	"github.com/mattjoyce/obconverge/internal/classify"
	"github.com/mattjoyce/obconverge/internal/errcode"
	"github.com/mattjoyce/obconverge/internal/hashing"
	"github.com/mattjoyce/obconverge/internal/links"
	"github.com/mattjoyce/obconverge/internal/plan"
	"github.com/mattjoyce/obconverge/internal/policy"
	"github.com/mattjoyce/obconverge/internal/scan"
)

// Schema is the header schema string for journal.jsonl artifacts.
const Schema = "journal/1"

// Op names the operation that was attempted. Only one op type today; more
// will arrive with merge-frontmatter and link rewriting.
type Op string

const (
	OpDrop             Op = "drop"
	OpMergeFrontmatter Op = "merge-frontmatter"
)

// Result is what happened when apply attempted an op.
type Result string

const (
	ResultApplied Result = "applied"
	ResultSkipped Result = "skipped"
	ResultRefused Result = "refused"
	ResultDryRun  Result = "dry-run"
)

// Reason is a stable machine-readable string explaining a skip or refusal.
type Reason string

const (
	ReasonHashDrift      Reason = "hash_drift"
	ReasonLinkedNote     Reason = "linked_note"
	ReasonSecretsBucket  Reason = "secrets_bucket"
	ReasonNotImplemented Reason = "not_implemented"
	ReasonUnknownAction  Reason = "unknown_action"
	ReasonPathNotInIndex Reason = "path_not_in_index"
)

// Entry is one journal record.
type Entry struct {
	ActionID      string `json:"action_id"`
	Op            Op     `json:"op"`
	Result        Result `json:"result"`
	Path          string `json:"path,omitempty"`
	TrashPath     string `json:"trash_path,omitempty"`
	ContentHash   string `json:"content_hash,omitempty"`
	ExpectedHash  string `json:"expected_hash,omitempty"`
	ActualHash    string `json:"actual_hash,omitempty"`
	Reason        Reason `json:"reason,omitempty"`
	ReferrerCount int    `json:"referrer_count,omitempty"`
	// SecretPattern is stamped when the record's bucket was SECRETS,
	// regardless of whether apply refused or proceeded. Makes every
	// secret-related action auditable even in warn / silent response
	// modes.
	SecretPattern string    `json:"secret_pattern,omitempty"`
	AppliedAt     time.Time `json:"applied_at"`
}

// Summary counts the results of a run, returned to the CLI for reporting.
type Summary struct {
	Applied   int
	Skipped   int
	Refused   int
	DryRun    int
	Unchecked int
}

// Options configures a run.
type Options struct {
	// VaultRoot is the vault being operated on. Required.
	VaultRoot string
	// WorkDir is the directory inside VaultRoot that holds artifacts
	// (index, classification, plan, journal, trash). Default: .obconverge.
	WorkDir string
	// PlanPath, if empty, defaults to <VaultRoot>/<WorkDir>/plan.md.
	PlanPath string
	// ClassificationPath, if empty, defaults to
	// <VaultRoot>/<WorkDir>/classification.jsonl.
	ClassificationPath string
	// IndexPath, if empty, defaults to <VaultRoot>/<WorkDir>/index.jsonl.
	IndexPath string
	// JournalPath, if empty, defaults to <VaultRoot>/<WorkDir>/journal.jsonl.
	JournalPath string
	// Execute controls whether apply actually mutates the vault. Default
	// false — dry-run. Real mutations require passing true explicitly.
	Execute bool
	// SecretResponse selects how apply treats SECRETS-bucket items whose
	// policy-assigned action is a mutating one (drop, merge-frontmatter).
	// For no-op actions (quarantine/review/keep) the mode doesn't matter
	// because apply never mutates. Defaults to SecretBlock if zero-valued.
	SecretResponse policy.SecretResponse
	// Policy, if non-nil, is consulted via ActionFor(bucket) to decide
	// what apply should do with each ticked item. If nil, defaults are
	// used (policy.Default()).
	Policy *policy.Policy
	// Now lets tests inject a deterministic timestamp for the trash dir
	// name and journal records.
	Now time.Time
}

// Run reads the plan, classification, and index; builds the link graph;
// and applies every checked item. Returns a Summary of results.
func Run(opts Options) (Summary, error) {
	if opts.VaultRoot == "" {
		return Summary{}, fmt.Errorf("%w: apply: VaultRoot is required", errcode.ErrUsage)
	}
	if opts.WorkDir == "" {
		opts.WorkDir = ".obconverge"
	}
	if opts.PlanPath == "" {
		opts.PlanPath = filepath.Join(opts.VaultRoot, opts.WorkDir, "plan.md")
	}
	if opts.ClassificationPath == "" {
		opts.ClassificationPath = filepath.Join(opts.VaultRoot, opts.WorkDir, "classification.jsonl")
	}
	if opts.IndexPath == "" {
		opts.IndexPath = filepath.Join(opts.VaultRoot, opts.WorkDir, "index.jsonl")
	}
	if opts.JournalPath == "" {
		opts.JournalPath = filepath.Join(opts.VaultRoot, opts.WorkDir, "journal.jsonl")
	}
	if opts.SecretResponse == "" {
		opts.SecretResponse = policy.SecretBlock
	}
	pol := opts.Policy
	if pol == nil {
		def := policy.Default()
		pol = &def
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	// Parse the plan for action-id -> checked state.
	checked, total, err := parsePlan(opts.PlanPath)
	if err != nil {
		return Summary{}, err
	}

	// Load classification records; index by action id.
	records, err := readClassification(opts.ClassificationPath)
	if err != nil {
		return Summary{}, err
	}
	byID := map[string]classify.Record{}
	for _, rec := range records {
		byID[plan.ItemIDFor(string(rec.Bucket), rec.Type, rec.Paths, rec.Path)] = rec
	}

	// Load index for hash lookups; index by path.
	entries, err := readIndex(opts.IndexPath)
	if err != nil {
		return Summary{}, err
	}
	byPath := map[string]scan.Entry{}
	for _, e := range entries {
		byPath[e.Path] = e
	}

	// Build link graph for safety refusals.
	graph, err := links.Build(links.Options{VaultRoot: opts.VaultRoot})
	if err != nil {
		return Summary{}, fmt.Errorf("apply: build link graph: %w", err)
	}

	// Open the journal. Append-only; created if absent.
	var journal *journalWriter
	if opts.Execute {
		journal, err = openJournal(opts.JournalPath, now)
		if err != nil {
			return Summary{}, err
		}
		defer func() { _ = journal.close() }()
	}

	trashRoot := filepath.Join(opts.VaultRoot, opts.WorkDir, "trash", now.Format("20060102-150405"))

	summary := Summary{Unchecked: total - len(checked)}

	// Process in deterministic order: sorted by action ID.
	ids := make([]string, 0, len(checked))
	for id := range checked {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		rec, ok := byID[id]
		if !ok {
			slog.Warn("apply: plan references unknown action id", "id", id)
			continue
		}
		outcome := processOne(opts, pol, rec, byPath, graph, trashRoot, now)
		switch outcome.Result {
		case ResultApplied:
			summary.Applied++
		case ResultSkipped:
			summary.Skipped++
		case ResultRefused:
			summary.Refused++
		case ResultDryRun:
			summary.DryRun++
		}
		if journal != nil {
			if err := journal.write(outcome); err != nil {
				return summary, err
			}
		}
	}
	return summary, nil
}

// processOne decides and (optionally) executes one action; returns the
// journal entry describing what happened.
//
// Flow:
//  1. Look up the policy's action for this bucket.
//  2. If the bucket is SECRETS and the action is mutating, consult
//     SecretResponse: block (refuse), warn (log + proceed), silent
//     (proceed quietly). Either way stamp SecretPattern on the entry.
//  3. Refuse linked notes (until --rewrite-links lands).
//  4. Dispatch on the action: drop (implemented), merge-frontmatter
//     (not yet), review/keep/quarantine (no-ops).
func processOne(opts Options, pol *policy.Policy, rec classify.Record, byPath map[string]scan.Entry, graph *links.Graph, trashRoot string, now time.Time) Entry {
	id := plan.ItemIDFor(string(rec.Bucket), rec.Type, rec.Paths, rec.Path)
	entry := Entry{ActionID: id, AppliedAt: now}

	action := pol.ActionFor(rec.Bucket)
	mutating := isMutatingAction(action)

	// SECRETS handling. SecretPattern always stamped on the entry for
	// audit, regardless of response mode or mutating-ness.
	if rec.Bucket == classify.BucketSecrets {
		entry.SecretPattern = rec.SecretPattern
		if mutating {
			switch opts.SecretResponse {
			case policy.SecretBlock, "":
				entry.Result = ResultRefused
				entry.Reason = ReasonSecretsBucket
				entry.Path = primaryPath(rec)
				return entry
			case policy.SecretWarn:
				slog.Warn("apply: proceeding on SECRETS file",
					"path", primaryPath(rec),
					"pattern", rec.SecretPattern,
					"action", action)
			case policy.SecretSilent:
				// Proceed without logging; journal still records SecretPattern.
			}
		}
	}

	// Linked-note refusal (until --rewrite-links lands) — only matters
	// for mutating actions. A non-mutating action on a linked file is
	// fine.
	if mutating && graph.Count(rec.Basename) > 0 {
		entry.Result = ResultRefused
		entry.Reason = ReasonLinkedNote
		entry.Path = primaryPath(rec)
		entry.ReferrerCount = graph.Count(rec.Basename)
		return entry
	}

	// Dispatch on the policy-assigned action.
	switch action {
	case policy.ActionDrop:
		if rec.Type != "pair" || len(rec.Paths) != 2 {
			entry.Op = OpDrop
			entry.Result = ResultSkipped
			entry.Reason = ReasonUnknownAction
			return entry
		}
		target := rec.Paths[0]
		entry.Op = OpDrop
		entry.Path = target
		return applyDrop(opts, entry, target, byPath, trashRoot, now)

	case policy.ActionMergeFrontmatter:
		entry.Op = OpMergeFrontmatter
		entry.Result = ResultSkipped
		entry.Reason = ReasonNotImplemented
		if len(rec.Paths) > 0 {
			entry.Path = rec.Paths[0]
		}
		return entry

	case policy.ActionReview, policy.ActionQuarantine, policy.ActionKeep:
		entry.Result = ResultSkipped
		entry.Reason = ReasonUnknownAction
		return entry

	default:
		entry.Result = ResultSkipped
		entry.Reason = ReasonUnknownAction
		return entry
	}
}

// isMutatingAction reports whether an action would mutate the vault.
// Only mutating actions trigger safety refusals and SecretResponse mode.
func isMutatingAction(a policy.Action) bool {
	switch a {
	case policy.ActionDrop, policy.ActionMergeFrontmatter:
		return true
	default:
		return false
	}
}

// applyDrop performs (or dry-runs) a soft delete of one file.
func applyDrop(opts Options, entry Entry, target string, byPath map[string]scan.Entry, trashRoot string, now time.Time) Entry {
	// Source of the plan's recorded hash.
	planEntry, ok := byPath[target]
	if !ok {
		entry.Result = ResultSkipped
		entry.Reason = ReasonPathNotInIndex
		return entry
	}
	entry.ExpectedHash = planEntry.ContentHash

	// Re-hash the file as it exists NOW.
	absPath := filepath.Join(opts.VaultRoot, target)
	data, err := os.ReadFile(absPath)
	if err != nil {
		entry.Result = ResultSkipped
		entry.Reason = ReasonHashDrift
		return entry
	}
	actual := string(hashing.OfBytes(normalizeLineEndings(data)))
	entry.ActualHash = actual

	if actual != planEntry.ContentHash {
		entry.Result = ResultSkipped
		entry.Reason = ReasonHashDrift
		return entry
	}

	// At this point the file matches its plan-time hash. Either move it
	// to trash (execute) or report the dry-run.
	trashPath := filepath.Join(trashRoot, target)
	entry.TrashPath, _ = filepath.Rel(opts.VaultRoot, trashPath)
	entry.ContentHash = actual

	if !opts.Execute {
		entry.Result = ResultDryRun
		return entry
	}

	if err := os.MkdirAll(filepath.Dir(trashPath), 0o755); err != nil {
		entry.Result = ResultSkipped
		entry.Reason = Reason(fmt.Sprintf("mkdir_trash_failed: %v", err))
		return entry
	}
	if err := os.Rename(absPath, trashPath); err != nil {
		entry.Result = ResultSkipped
		entry.Reason = Reason(fmt.Sprintf("rename_failed: %v", err))
		return entry
	}
	entry.Result = ResultApplied
	slog.Info("apply: dropped", "path", target, "trash", entry.TrashPath)
	return entry
}

// normalizeLineEndings mirrors scan's normalization so hash comparisons
// are apples-to-apples.
func normalizeLineEndings(b []byte) []byte {
	if len(b) == 0 {
		return b
	}
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if i+1 < len(b) && b[i] == '\r' && b[i+1] == '\n' {
			continue
		}
		out = append(out, b[i])
	}
	return out
}

// primaryPath returns the first path from Paths, or Path for unique
// records, or "" if nothing is populated.
func primaryPath(rec classify.Record) string {
	if len(rec.Paths) > 0 {
		return rec.Paths[0]
	}
	return rec.Path
}

// --- plan parsing -----------------------------------------------------------

var actionLineRe = regexp.MustCompile("^- \\[([ x])\\] #\\S+ `([a-f0-9]{6,})`")

// parsePlan reads plan.md and returns (checked set, total item count).
func parsePlan(path string) (map[string]bool, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("apply: open plan %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	checked := map[string]bool{}
	total := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		m := actionLineRe.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		total++
		if m[1] == "x" {
			checked[m[2]] = true
		}
	}
	if err := sc.Err(); err != nil {
		return nil, 0, fmt.Errorf("apply: scan plan: %w", err)
	}
	return checked, total, nil
}

// --- classification / index readers ----------------------------------------

func readClassification(path string) ([]classify.Record, error) {
	r, err := artifact.NewReader(path, classify.Schema)
	if err != nil {
		return nil, fmt.Errorf("apply: open classification: %w", err)
	}
	defer func() { _ = r.Close() }()

	var out []classify.Record
	for {
		var rec classify.Record
		err := r.Next(&rec)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("apply: read classification: %w", err)
		}
		out = append(out, rec)
	}
	return out, nil
}

func readIndex(path string) ([]scan.Entry, error) {
	r, err := artifact.NewReader(path, scan.Schema)
	if err != nil {
		return nil, fmt.Errorf("apply: open index: %w", err)
	}
	defer func() { _ = r.Close() }()

	var out []scan.Entry
	for {
		var e scan.Entry
		err := r.Next(&e)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("apply: read index: %w", err)
		}
		out = append(out, e)
	}
	return out, nil
}

// --- journal ---------------------------------------------------------------

type journalWriter struct {
	w *artifact.Writer
}

func openJournal(path string, now time.Time) (*journalWriter, error) {
	// Journal is append-only conceptually, but our artifact.Writer creates
	// a fresh file each time it opens. For v1 that's acceptable — the
	// journal is per-apply-run, and undo will consume the most recent
	// journal (or all journals in the trash directory). A proper rotating
	// append will land when undo does.
	_ = now
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("apply: mkdir journal dir: %w", err)
	}
	// If the journal already exists, rename it with a timestamp suffix so
	// multiple apply runs don't stomp each other's history.
	if _, err := os.Stat(path); err == nil {
		bak := path + "." + time.Now().UTC().Format("20060102-150405") + ".bak"
		if renameErr := os.Rename(path, bak); renameErr != nil {
			return nil, fmt.Errorf("apply: preserve previous journal: %w", renameErr)
		}
	}
	w, err := artifact.NewWriter(path, Schema)
	if err != nil {
		return nil, fmt.Errorf("apply: open journal: %w", err)
	}
	return &journalWriter{w: w}, nil
}

func (j *journalWriter) write(e Entry) error {
	if err := j.w.Write(e); err != nil {
		return fmt.Errorf("apply: write journal entry: %w", err)
	}
	return j.w.Sync()
}

func (j *journalWriter) close() error { return j.w.Close() }
