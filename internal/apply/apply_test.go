package apply_test

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattjoyce/obconverge/internal/apply"
	"github.com/mattjoyce/obconverge/internal/artifact"
	"github.com/mattjoyce/obconverge/internal/classify"
	"github.com/mattjoyce/obconverge/internal/plan"
	"github.com/mattjoyce/obconverge/internal/scan"
	"github.com/mattjoyce/obconverge/internal/secrets"
	"github.com/mattjoyce/obconverge/internal/testvault"
)

// setup builds a vault, runs scan + classify + plan, and returns the vault
// root. The caller can then tick checkboxes in plan.md and invoke apply.
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
	return root
}

// tickAll replaces every "- [ ]" with "- [x]" in the plan.md.
func tickAll(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, ".obconverge", "plan.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}
	ticked := strings.ReplaceAll(string(data), "- [ ]", "- [x]")
	if err := os.WriteFile(path, []byte(ticked), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}
}

func runApply(t *testing.T, root string, execute bool) apply.Summary {
	t.Helper()
	sum, err := apply.Run(apply.Options{
		VaultRoot: root,
		Execute:   execute,
		Now:       time.Date(2026, 4, 23, 10, 0, 1, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	return sum
}

func readJournal(t *testing.T, root string) []apply.Entry {
	t.Helper()
	path := filepath.Join(root, ".obconverge", "journal.jsonl")
	r, err := artifact.NewReader(path, apply.Schema)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	defer r.Close()
	var out []apply.Entry
	for {
		var e apply.Entry
		err := r.Next(&e)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read journal: %v", err)
		}
		out = append(out, e)
	}
	return out
}

func TestApply_DropExactDuplicate(t *testing.T) {
	root := setup(t,
		testvault.File{Path: "Notes/Alpha.md", Content: "body\n"},
		testvault.File{Path: "Prod/Alpha.md", Content: "body\n"},
	)
	tickAll(t, root)

	sum := runApply(t, root, true)
	if sum.Applied != 1 {
		t.Errorf("Applied = %d, want 1: summary=%+v", sum.Applied, sum)
	}

	// Notes/Alpha.md should be the survivor (Prod/... sorts first alphabetically? actually Notes/ < Prod/).
	// Our rule: drop paths[0] (lexicographically first). Notes/Alpha.md < Prod/Alpha.md.
	if _, err := os.Stat(filepath.Join(root, "Notes/Alpha.md")); err == nil {
		t.Error("Notes/Alpha.md should have been dropped (it sorts first)")
	}
	if _, err := os.Stat(filepath.Join(root, "Prod/Alpha.md")); err != nil {
		t.Errorf("Prod/Alpha.md should remain: %v", err)
	}
	// Dropped file should be in trash.
	trashMatches, _ := filepath.Glob(filepath.Join(root, ".obconverge", "trash", "*", "Notes", "Alpha.md"))
	if len(trashMatches) != 1 {
		t.Errorf("expected trashed Notes/Alpha.md, got %v", trashMatches)
	}
}

func TestApply_DryRunDoesNotMutate(t *testing.T) {
	root := setup(t,
		testvault.File{Path: "Notes/Alpha.md", Content: "body\n"},
		testvault.File{Path: "Prod/Alpha.md", Content: "body\n"},
	)
	tickAll(t, root)

	// Snapshot vault content.
	before := snapshotVault(t, root)

	sum := runApply(t, root, false) // Execute=false
	if sum.DryRun != 1 {
		t.Errorf("DryRun = %d, want 1: summary=%+v", sum.DryRun, sum)
	}
	if sum.Applied != 0 {
		t.Errorf("Applied = %d, want 0 in dry-run", sum.Applied)
	}

	after := snapshotVault(t, root)
	if !equalStringMaps(before, after) {
		t.Errorf("dry-run mutated vault")
	}

	// Dry-run should not create a journal file.
	if _, err := os.Stat(filepath.Join(root, ".obconverge", "journal.jsonl")); err == nil {
		t.Error("dry-run should not create journal.jsonl")
	}
}

func TestApply_RefusesLinkedNote(t *testing.T) {
	root := setup(t,
		testvault.File{Path: "Notes/Alpha.md", Content: "body\n"},
		testvault.File{Path: "Prod/Alpha.md", Content: "body\n"},
		testvault.File{Path: "References.md", Content: "see [[Alpha]]\n"},
	)
	tickAll(t, root)

	sum := runApply(t, root, true)
	if sum.Refused != 1 {
		t.Errorf("Refused = %d, want 1: summary=%+v", sum.Refused, sum)
	}
	if sum.Applied != 0 {
		t.Errorf("Applied = %d, want 0 (file is linked)", sum.Applied)
	}

	// Both originals still present.
	if _, err := os.Stat(filepath.Join(root, "Notes/Alpha.md")); err != nil {
		t.Errorf("Notes/Alpha.md should still exist: %v", err)
	}

	// Journal should record the refusal with reason linked_note.
	journal := readJournal(t, root)
	found := false
	for _, e := range journal {
		if e.Result == apply.ResultRefused && e.Reason == apply.ReasonLinkedNote {
			found = true
			if e.ReferrerCount != 1 {
				t.Errorf("ReferrerCount = %d, want 1", e.ReferrerCount)
			}
		}
	}
	if !found {
		t.Errorf("journal missing linked_note refusal: %+v", journal)
	}
}

func TestApply_RefusesSecretsBucket(t *testing.T) {
	root := setup(t,
		testvault.File{Path: "Notes/Keys.md", Content: "AKIAIOSFODNN7EXAMPLE\n"},
		testvault.File{Path: "Prod/Keys.md", Content: "AKIAIOSFODNN7EXAMPLE\n"},
	)
	tickAll(t, root)

	sum := runApply(t, root, true)
	if sum.Refused != 1 {
		t.Errorf("Refused = %d, want 1: summary=%+v", sum.Refused, sum)
	}
	if sum.Applied != 0 {
		t.Errorf("Applied = %d, want 0 (SECRETS bucket)", sum.Applied)
	}

	journal := readJournal(t, root)
	found := false
	for _, e := range journal {
		if e.Result == apply.ResultRefused && e.Reason == apply.ReasonSecretsBucket {
			found = true
		}
	}
	if !found {
		t.Errorf("journal missing secrets_bucket refusal: %+v", journal)
	}
}

func TestApply_SkipsOnHashDrift(t *testing.T) {
	root := setup(t,
		testvault.File{Path: "Notes/Alpha.md", Content: "body\n"},
		testvault.File{Path: "Prod/Alpha.md", Content: "body\n"},
	)
	tickAll(t, root)

	// Mutate Notes/Alpha.md after the plan was written.
	if err := os.WriteFile(filepath.Join(root, "Notes/Alpha.md"), []byte("drifted!\n"), 0o644); err != nil {
		t.Fatalf("mutate: %v", err)
	}

	sum := runApply(t, root, true)
	if sum.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1: summary=%+v", sum.Skipped, sum)
	}
	if sum.Applied != 0 {
		t.Errorf("Applied = %d, want 0 (drift)", sum.Applied)
	}

	// File should not have been moved.
	if _, err := os.Stat(filepath.Join(root, "Notes/Alpha.md")); err != nil {
		t.Errorf("drifted file should remain in place: %v", err)
	}

	journal := readJournal(t, root)
	found := false
	for _, e := range journal {
		if e.Result == apply.ResultSkipped && e.Reason == apply.ReasonHashDrift {
			found = true
			if e.ExpectedHash == "" || e.ActualHash == "" {
				t.Errorf("drift entry missing hashes: %+v", e)
			}
			if e.ExpectedHash == e.ActualHash {
				t.Errorf("drift entry should have unequal hashes: %+v", e)
			}
		}
	}
	if !found {
		t.Errorf("journal missing hash_drift skip: %+v", journal)
	}
}

func TestApply_UncheckedItemsNotProcessed(t *testing.T) {
	root := setup(t,
		testvault.File{Path: "Notes/A.md", Content: "body\n"},
		testvault.File{Path: "Prod/A.md", Content: "body\n"},
	)
	// Do NOT tick anything.

	sum := runApply(t, root, true)
	if sum.Applied != 0 || sum.Skipped != 0 || sum.Refused != 0 {
		t.Errorf("unchecked plan should do nothing: %+v", sum)
	}
	if sum.Unchecked != 1 {
		t.Errorf("Unchecked = %d, want 1", sum.Unchecked)
	}

	// Both files untouched.
	if _, err := os.Stat(filepath.Join(root, "Notes/A.md")); err != nil {
		t.Errorf("A.md should remain: %v", err)
	}
}

func TestApply_FrontmatterOnlyNotImplemented(t *testing.T) {
	root := setup(t,
		testvault.File{Path: "Notes/B.md", Content: "---\ntags: [x]\n---\n\nshared body\n"},
		testvault.File{Path: "Prod/B.md", Content: "---\ntags: [y]\n---\n\nshared body\n"},
	)
	tickAll(t, root)

	sum := runApply(t, root, true)
	if sum.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1 (merge-frontmatter not implemented): %+v", sum.Skipped, sum)
	}

	journal := readJournal(t, root)
	found := false
	for _, e := range journal {
		if e.Op == apply.OpMergeFrontmatter && e.Reason == apply.ReasonNotImplemented {
			found = true
		}
	}
	if !found {
		t.Errorf("journal missing merge-frontmatter skip with not_implemented: %+v", journal)
	}
}

func TestApply_JournalPreservedAcrossRuns(t *testing.T) {
	root := setup(t,
		testvault.File{Path: "Notes/A.md", Content: "body\n"},
		testvault.File{Path: "Prod/A.md", Content: "body\n"},
	)
	tickAll(t, root)

	_ = runApply(t, root, true)

	// Second run should rename the previous journal and write fresh.
	_ = runApply(t, root, true)

	workDir := filepath.Join(root, ".obconverge")
	bakMatches, _ := filepath.Glob(filepath.Join(workDir, "journal.jsonl.*.bak"))
	if len(bakMatches) != 1 {
		t.Errorf("expected exactly one .bak journal from the previous run, got %v", bakMatches)
	}
	if _, err := os.Stat(filepath.Join(workDir, "journal.jsonl")); err != nil {
		t.Errorf("current journal missing: %v", err)
	}
}

// --- helpers ---

func snapshotVault(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		// Skip .obconverge artifacts — apply legitimately creates journals there.
		if strings.HasPrefix(rel, ".obconverge") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	return out
}

func equalStringMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
