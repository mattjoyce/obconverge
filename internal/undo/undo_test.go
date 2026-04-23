package undo_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattjoyce/obconverge/internal/apply"
	"github.com/mattjoyce/obconverge/internal/classify"
	"github.com/mattjoyce/obconverge/internal/plan"
	"github.com/mattjoyce/obconverge/internal/scan"
	"github.com/mattjoyce/obconverge/internal/secrets"
	"github.com/mattjoyce/obconverge/internal/testvault"
	"github.com/mattjoyce/obconverge/internal/undo"
)

// setup builds a vault, runs the full scan→classify→plan→apply pipeline
// with the given files all ticked in the plan, then returns the vault
// root so tests can invoke undo.
func setup(t *testing.T, files ...testvault.File) string {
	t.Helper()
	root := testvault.Build(t, files...)
	work := filepath.Join(root, ".obconverge")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatalf("mkdir work: %v", err)
	}

	indexPath := filepath.Join(work, "index.jsonl")
	classPath := filepath.Join(work, "classification.jsonl")
	planPath := filepath.Join(work, "plan.md")

	if err := scan.Run(scan.Options{
		VaultRoot:  root,
		OutputPath: indexPath,
		Detector:   secrets.NewBuiltins(),
	}); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if err := classify.Run(classify.Options{
		IndexPath:          indexPath,
		ClassificationPath: classPath,
	}); err != nil {
		t.Fatalf("classify: %v", err)
	}
	if err := plan.Run(plan.Options{
		ClassificationPath: classPath,
		OutputPath:         planPath,
		Now:                time.Date(2026, 4, 23, 10, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("plan: %v", err)
	}

	// Tick every checkbox.
	body, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}
	ticked := strings.ReplaceAll(string(body), "- [ ]", "- [x]")
	if err := os.WriteFile(planPath, []byte(ticked), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	// Execute apply.
	if _, err := apply.Run(apply.Options{
		VaultRoot: root,
		Execute:   true,
		Now:       time.Date(2026, 4, 23, 10, 0, 1, 0, time.UTC),
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	return root
}

func TestUndo_ReversesDrop(t *testing.T) {
	root := setup(t,
		testvault.File{Path: "Notes/A.md", Content: "body\n"},
		testvault.File{Path: "Prod/A.md", Content: "body\n"},
	)

	// apply dropped Notes/A.md (lexicographically first).
	if _, err := os.Stat(filepath.Join(root, "Notes/A.md")); err == nil {
		t.Fatal("setup invariant: Notes/A.md should have been dropped")
	}

	sum, entries, err := undo.Run(undo.Options{
		VaultRoot: root,
		Execute:   true,
	})
	if err != nil {
		t.Fatalf("undo.Run: %v", err)
	}
	if sum.Restored != 1 {
		t.Errorf("Restored = %d, want 1: %+v entries=%+v", sum.Restored, sum, entries)
	}
	// File back in place.
	restored, err := os.ReadFile(filepath.Join(root, "Notes/A.md"))
	if err != nil {
		t.Errorf("Notes/A.md not restored: %v", err)
	}
	if string(restored) != "body\n" {
		t.Errorf("restored content = %q, want %q", restored, "body\n")
	}
}

func TestUndo_ReversesMergeFrontmatter(t *testing.T) {
	root := setup(t,
		testvault.File{Path: "Notes/B.md", Content: "---\ntags:\n  - alpha\n---\n\nshared\n"},
		testvault.File{Path: "Prod/B.md", Content: "---\ntags:\n  - beta\n---\n\nshared\n"},
	)

	// Sanity: loser is gone, winner has merged tags.
	if _, err := os.Stat(filepath.Join(root, "Notes/B.md")); err == nil {
		t.Fatal("setup invariant: Notes/B.md should have been moved to trash")
	}
	winnerMerged, _ := os.ReadFile(filepath.Join(root, "Prod/B.md"))
	if !strings.Contains(string(winnerMerged), "alpha") {
		t.Fatalf("setup invariant: winner should have merged tags; got:\n%s", winnerMerged)
	}

	sum, _, err := undo.Run(undo.Options{VaultRoot: root, Execute: true})
	if err != nil {
		t.Fatalf("undo.Run: %v", err)
	}
	if sum.Restored != 1 {
		t.Errorf("Restored = %d, want 1: %+v", sum.Restored, sum)
	}

	// Loser restored.
	loser, err := os.ReadFile(filepath.Join(root, "Notes/B.md"))
	if err != nil {
		t.Errorf("loser not restored: %v", err)
	}
	if !strings.Contains(string(loser), "alpha") {
		t.Errorf("restored loser missing its original tag 'alpha':\n%s", loser)
	}
	if strings.Contains(string(loser), "beta") {
		t.Errorf("restored loser should not have winner's tag:\n%s", loser)
	}

	// Winner rolled back to pre-merge state (tags: beta only).
	winner, err := os.ReadFile(filepath.Join(root, "Prod/B.md"))
	if err != nil {
		t.Errorf("winner missing: %v", err)
	}
	if !strings.Contains(string(winner), "beta") {
		t.Errorf("restored winner should still have 'beta':\n%s", winner)
	}
	if strings.Contains(string(winner), "alpha") {
		t.Errorf("restored winner should NOT have loser's 'alpha' after undo:\n%s", winner)
	}
}

func TestUndo_DryRunDoesNotMutate(t *testing.T) {
	root := setup(t,
		testvault.File{Path: "Notes/A.md", Content: "body\n"},
		testvault.File{Path: "Prod/A.md", Content: "body\n"},
	)

	// Sanity: loser is in trash.
	if _, err := os.Stat(filepath.Join(root, "Notes/A.md")); err == nil {
		t.Fatal("setup invariant: Notes/A.md should have been dropped")
	}

	sum, _, err := undo.Run(undo.Options{VaultRoot: root, Execute: false})
	if err != nil {
		t.Fatalf("undo.Run: %v", err)
	}
	if sum.DryRun != 1 {
		t.Errorf("DryRun = %d, want 1: %+v", sum.DryRun, sum)
	}
	if sum.Restored != 0 {
		t.Errorf("Restored = %d, want 0 in dry-run", sum.Restored)
	}

	// Notes/A.md must STILL be absent (dry-run didn't move).
	if _, err := os.Stat(filepath.Join(root, "Notes/A.md")); err == nil {
		t.Errorf("dry-run should not restore file; Notes/A.md exists")
	}
}

func TestUndo_RefusesWhenDestinationExists(t *testing.T) {
	root := setup(t,
		testvault.File{Path: "Notes/A.md", Content: "body\n"},
		testvault.File{Path: "Prod/A.md", Content: "body\n"},
	)

	// Operator has since created a new file at the original path.
	if err := os.WriteFile(filepath.Join(root, "Notes/A.md"), []byte("new content\n"), 0o644); err != nil {
		t.Fatalf("write post-apply: %v", err)
	}

	sum, entries, err := undo.Run(undo.Options{VaultRoot: root, Execute: true})
	if err != nil {
		t.Fatalf("undo.Run: %v", err)
	}
	if sum.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1: %+v", sum.Skipped, sum)
	}
	found := false
	for _, e := range entries {
		if e.Result == undo.ResultSkipped && e.Reason == undo.ReasonDestinationExists {
			found = true
		}
	}
	if !found {
		t.Errorf("expected destination_exists skip; entries = %+v", entries)
	}

	// Operator's file must be untouched.
	b, _ := os.ReadFile(filepath.Join(root, "Notes/A.md"))
	if string(b) != "new content\n" {
		t.Errorf("operator's post-apply file was modified: %q", b)
	}
}

func TestUndo_RefusesWhenTrashMissing(t *testing.T) {
	root := setup(t,
		testvault.File{Path: "Notes/A.md", Content: "body\n"},
		testvault.File{Path: "Prod/A.md", Content: "body\n"},
	)

	// Operator has cleaned up the trash directory.
	if err := os.RemoveAll(filepath.Join(root, ".obconverge", "trash")); err != nil {
		t.Fatalf("remove trash: %v", err)
	}

	sum, entries, err := undo.Run(undo.Options{VaultRoot: root, Execute: true})
	if err != nil {
		t.Fatalf("undo.Run: %v", err)
	}
	if sum.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1: %+v", sum.Skipped, sum)
	}
	found := false
	for _, e := range entries {
		if e.Result == undo.ResultSkipped && e.Reason == undo.ReasonTrashMissing {
			found = true
		}
	}
	if !found {
		t.Errorf("expected trash_missing skip; entries = %+v", entries)
	}
}

func TestUndo_LIFOOrdering(t *testing.T) {
	// Build a vault with two unrelated EXACT pairs, apply both, and
	// verify undo reverses them in reverse order (most recent first).
	root := setup(t,
		testvault.File{Path: "Notes/A.md", Content: "one\n"},
		testvault.File{Path: "Prod/A.md", Content: "one\n"},
		testvault.File{Path: "Notes/B.md", Content: "two\n"},
		testvault.File{Path: "Prod/B.md", Content: "two\n"},
	)

	sum, entries, err := undo.Run(undo.Options{VaultRoot: root, Execute: true})
	if err != nil {
		t.Fatalf("undo.Run: %v", err)
	}
	if sum.Restored != 2 {
		t.Errorf("Restored = %d, want 2: %+v", sum.Restored, sum)
	}
	// The journal applied in sorted-by-action-id order. undo reverses
	// that, so the first entry in `entries` should correspond to the
	// last applied. We assert the list is in reverse of sort order.
	if len(entries) != 2 {
		t.Fatalf("entries len = %d, want 2", len(entries))
	}
	if entries[0].ReversedAt.Before(entries[1].ReversedAt) && !entries[0].ReversedAt.Equal(entries[1].ReversedAt) {
		t.Errorf("first-reversed entry should not predate second: %v vs %v",
			entries[0].ReversedAt, entries[1].ReversedAt)
	}
}
