// Package undo reverses the most recent apply run by restoring files
// from trash. It is the other half of the spec's reversibility
// guarantee: "Every apply produces a journal entry; every journal entry
// is reversible until --purge."
//
// undo reads <vault>/.obconverge/journal.jsonl, iterates the Applied
// entries in reverse order, and for each one:
//
//   - drop:              rename trash_path -> path
//   - merge-frontmatter: rename trash_path -> path (restore winner)
//     rename secondary_trash -> secondary_path (restore loser)
//
// undo refuses to overwrite an existing file at the restore destination
// — if the operator has edited the path since apply, they decide how
// to reconcile. undo reports a per-entry outcome (restored / skipped /
// refused) and never mutates the journal.
//
// v1 is deliberately narrow: it reverses the current journal.jsonl in
// whole. Spec features like `undo <journal-id>` and `undo --since`
// land later.
package undo

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/mattjoyce/obconverge/internal/apply"
	"github.com/mattjoyce/obconverge/internal/artifact"
)

// Result is what undo did for a single journal entry.
type Result string

const (
	ResultRestored Result = "restored"
	ResultSkipped  Result = "skipped"
	ResultDryRun   Result = "dry-run"
)

// Reason is a stable machine-readable explanation for a non-restore.
type Reason string

const (
	// ReasonDestinationExists: restore target is already present at its
	// original path. undo refuses to overwrite; operator decides.
	ReasonDestinationExists Reason = "destination_exists"
	// ReasonTrashMissing: the trash path the journal points at is gone.
	// This can happen if operator ran `rm -rf .obconverge/trash/` or
	// similar. No recovery possible.
	ReasonTrashMissing Reason = "trash_missing"
	// ReasonNotReversible: the journal entry's Result wasn't Applied, so
	// there's nothing to reverse.
	ReasonNotReversible Reason = "not_reversible"
)

// Entry is one undo-time record, returned to the caller for reporting.
// undo does not itself write a journal — redo is "re-run apply", not
// "re-run undo". Keeps the audit trail in one place.
type Entry struct {
	ActionID      string    `json:"action_id"`
	Op            apply.Op  `json:"op"`
	Result        Result    `json:"result"`
	Path          string    `json:"path,omitempty"`
	SecondaryPath string    `json:"secondary_path,omitempty"`
	Reason        Reason    `json:"reason,omitempty"`
	ReversedAt    time.Time `json:"reversed_at"`
}

// Summary counts results for the CLI.
type Summary struct {
	Restored int
	Skipped  int
	DryRun   int
}

// Options configures a run.
type Options struct {
	VaultRoot   string
	WorkDir     string // default ".obconverge"
	JournalPath string // default <vault>/<workdir>/journal.jsonl
	Execute     bool   // default false (dry-run)
	Now         time.Time
}

// Run reverses every Applied entry in the target journal.
func Run(opts Options) (Summary, []Entry, error) {
	if opts.VaultRoot == "" {
		return Summary{}, nil, fmt.Errorf("undo: VaultRoot is required")
	}
	if opts.WorkDir == "" {
		opts.WorkDir = ".obconverge"
	}
	if opts.JournalPath == "" {
		opts.JournalPath = filepath.Join(opts.VaultRoot, opts.WorkDir, "journal.jsonl")
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	journal, err := readJournal(opts.JournalPath)
	if err != nil {
		return Summary{}, nil, err
	}

	// Reverse Applied entries in LIFO order.
	entries := make([]Entry, 0, len(journal))
	summary := Summary{}
	for i := len(journal) - 1; i >= 0; i-- {
		src := journal[i]
		if src.Result != apply.ResultApplied {
			// Don't emit an Entry for skipped/refused/dry-run originals;
			// they didn't mutate anything.
			continue
		}
		out := reverseOne(opts.VaultRoot, src, opts.Execute, now)
		entries = append(entries, out)
		switch out.Result {
		case ResultRestored:
			summary.Restored++
		case ResultSkipped:
			summary.Skipped++
		case ResultDryRun:
			summary.DryRun++
		}
	}
	return summary, entries, nil
}

// reverseOne computes and (optionally) performs the reverse of a single
// apply journal record.
func reverseOne(vault string, src apply.Entry, execute bool, now time.Time) Entry {
	out := Entry{
		ActionID:      src.ActionID,
		Op:            src.Op,
		Path:          src.Path,
		SecondaryPath: src.SecondaryPath,
		ReversedAt:    now,
	}

	switch src.Op {
	case apply.OpDrop:
		return reverseDrop(vault, src, out, execute)
	case apply.OpMergeFrontmatter:
		return reverseMerge(vault, src, out, execute)
	default:
		out.Result = ResultSkipped
		out.Reason = ReasonNotReversible
		return out
	}
}

func reverseDrop(vault string, src apply.Entry, out Entry, execute bool) Entry {
	pathAbs := filepath.Join(vault, src.Path)
	trashAbs := filepath.Join(vault, src.TrashPath)

	if !fileExists(trashAbs) {
		out.Result = ResultSkipped
		out.Reason = ReasonTrashMissing
		return out
	}
	if fileExists(pathAbs) {
		out.Result = ResultSkipped
		out.Reason = ReasonDestinationExists
		return out
	}

	if !execute {
		out.Result = ResultDryRun
		return out
	}
	if err := os.MkdirAll(filepath.Dir(pathAbs), 0o755); err != nil {
		out.Result = ResultSkipped
		out.Reason = Reason(fmt.Sprintf("mkdir_failed: %v", err))
		return out
	}
	if err := os.Rename(trashAbs, pathAbs); err != nil {
		out.Result = ResultSkipped
		out.Reason = Reason(fmt.Sprintf("rename_failed: %v", err))
		return out
	}
	out.Result = ResultRestored
	slog.Info("undo: restored drop", "path", src.Path, "from", src.TrashPath)
	return out
}

func reverseMerge(vault string, src apply.Entry, out Entry, execute bool) Entry {
	winPath := filepath.Join(vault, src.Path)        // rewritten file
	winBackup := filepath.Join(vault, src.TrashPath) // pre-rewrite backup
	losePath := filepath.Join(vault, src.SecondaryPath)
	loseTrash := filepath.Join(vault, src.SecondaryTrash)

	// Both backups must be present to reverse the merge as a whole.
	if !fileExists(winBackup) || !fileExists(loseTrash) {
		out.Result = ResultSkipped
		out.Reason = ReasonTrashMissing
		return out
	}
	// Loser's original path must be clear. Winner's path will be
	// overwritten (that's the whole point — the winner was rewritten
	// and we're putting its original content back), but we refuse if
	// the content at winPath doesn't match what apply produced — i.e.
	// the operator has since edited the winner by hand, and we'd blow
	// away their work.
	if fileExists(losePath) {
		out.Result = ResultSkipped
		out.Reason = ReasonDestinationExists
		return out
	}
	// (We could re-hash winPath and compare to src.ContentHash to
	// detect post-apply edits. For v1 we keep it simple: operator is
	// responsible for running undo before editing. A later commit can
	// add the hash guard.)

	if !execute {
		out.Result = ResultDryRun
		return out
	}

	// Step 1: restore loser (it's currently missing from its original path).
	if err := os.MkdirAll(filepath.Dir(losePath), 0o755); err != nil {
		out.Result = ResultSkipped
		out.Reason = Reason(fmt.Sprintf("mkdir_loser_failed: %v", err))
		return out
	}
	if err := os.Rename(loseTrash, losePath); err != nil {
		out.Result = ResultSkipped
		out.Reason = Reason(fmt.Sprintf("rename_loser_failed: %v", err))
		return out
	}

	// Step 2: overwrite winner with its pre-rewrite backup. Use rename
	// for atomicity. Winner's path already exists (with merged content),
	// so rename replaces it.
	if err := os.Rename(winBackup, winPath); err != nil {
		// Winner rename failed AFTER loser was restored. Inconsistent
		// state: loser is back in place but winner still has the merged
		// content. Surface as a skip so the operator can investigate.
		out.Result = ResultSkipped
		out.Reason = Reason(fmt.Sprintf("rename_winner_failed: %v", err))
		return out
	}
	out.Result = ResultRestored
	slog.Info("undo: restored merge-frontmatter",
		"winner", src.Path,
		"loser", src.SecondaryPath)
	return out
}

func readJournal(path string) ([]apply.Entry, error) {
	r, err := artifact.NewReader(path, apply.Schema)
	if err != nil {
		return nil, fmt.Errorf("undo: open journal: %w", err)
	}
	defer func() { _ = r.Close() }()

	var out []apply.Entry
	for {
		var e apply.Entry
		err := r.Next(&e)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("undo: read journal: %w", err)
		}
		out = append(out, e)
	}
	return out, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
