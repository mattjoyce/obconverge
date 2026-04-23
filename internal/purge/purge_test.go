package purge_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mattjoyce/obconverge/internal/apply"
	"github.com/mattjoyce/obconverge/internal/classify"
	"github.com/mattjoyce/obconverge/internal/plan"
	"github.com/mattjoyce/obconverge/internal/purge"
	"github.com/mattjoyce/obconverge/internal/scan"
	"github.com/mattjoyce/obconverge/internal/secrets"
	"github.com/mattjoyce/obconverge/internal/testvault"
	"github.com/mattjoyce/obconverge/internal/undo"
)

// appliedVault builds a vault, runs the pipeline, ticks the plan, and
// executes apply so there's real content in the trash directory.
func appliedVault(t *testing.T) string {
	t.Helper()
	root := testvault.Build(t,
		testvault.File{Path: "Notes/A.md", Content: "body one\n"},
		testvault.File{Path: "Prod/A.md", Content: "body one\n"},
		testvault.File{Path: "Notes/B.md", Content: "body two\n"},
		testvault.File{Path: "Prod/B.md", Content: "body two\n"},
	)
	work := filepath.Join(root, ".obconverge")
	_ = os.MkdirAll(work, 0o755)

	indexPath := filepath.Join(work, "index.jsonl")
	classPath := filepath.Join(work, "classification.jsonl")
	planPath := filepath.Join(work, "plan.md")

	if err := scan.Run(scan.Options{VaultRoot: root, OutputPath: indexPath, Detector: secrets.NewBuiltins()}); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if err := classify.Run(classify.Options{IndexPath: indexPath, ClassificationPath: classPath}); err != nil {
		t.Fatalf("classify: %v", err)
	}
	if err := plan.Run(plan.Options{
		ClassificationPath: classPath,
		OutputPath:         planPath,
		Now:                time.Date(2026, 4, 23, 10, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("plan: %v", err)
	}
	data, _ := os.ReadFile(planPath)
	_ = os.WriteFile(planPath, []byte(replaceAll(string(data), "- [ ]", "- [x]")), 0o644)
	if _, err := apply.Run(apply.Options{VaultRoot: root, Execute: true, Now: time.Date(2026, 4, 23, 10, 0, 1, 0, time.UTC)}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	return root
}

func TestPurge_DryRunReportsWithoutRemoving(t *testing.T) {
	root := appliedVault(t)
	trashDir := filepath.Join(root, ".obconverge", "trash")

	sum, err := purge.Run(purge.Options{VaultRoot: root, Execute: false})
	if err != nil {
		t.Fatalf("purge.Run: %v", err)
	}
	if !sum.DryRun {
		t.Error("DryRun should be true when Execute=false")
	}
	if sum.Files == 0 {
		t.Error("Files should be > 0 after applied vault")
	}
	if sum.Bytes == 0 {
		t.Error("Bytes should be > 0")
	}
	if sum.Removed {
		t.Error("Removed should be false in dry-run")
	}
	if _, err := os.Stat(trashDir); err != nil {
		t.Errorf("trash dir should still exist after dry-run: %v", err)
	}
}

func TestPurge_ExecuteRemovesTrash(t *testing.T) {
	root := appliedVault(t)
	trashDir := filepath.Join(root, ".obconverge", "trash")

	sum, err := purge.Run(purge.Options{VaultRoot: root, Execute: true})
	if err != nil {
		t.Fatalf("purge.Run: %v", err)
	}
	if !sum.Removed {
		t.Error("Removed should be true after --execute")
	}
	if _, err := os.Stat(trashDir); !os.IsNotExist(err) {
		t.Errorf("trash dir should be gone: err=%v", err)
	}
}

func TestPurge_AbsentTrashIsNoOp(t *testing.T) {
	// Fresh vault, no apply run, no trash dir.
	root := testvault.Build(t, testvault.File{Path: "Solo.md", Content: "alone\n"})
	sum, err := purge.Run(purge.Options{VaultRoot: root, Execute: true})
	if err != nil {
		t.Errorf("absent trash should not error, got %v", err)
	}
	if sum.Files != 0 {
		t.Errorf("Files = %d, want 0", sum.Files)
	}
}

func TestPurge_UndoFailsAfterPurge(t *testing.T) {
	// The "reversible until --purge" boundary: undo after purge reports
	// trash_missing for every Applied entry.
	root := appliedVault(t)

	_, err := purge.Run(purge.Options{VaultRoot: root, Execute: true})
	if err != nil {
		t.Fatalf("purge: %v", err)
	}

	sum, entries, err := undo.Run(undo.Options{VaultRoot: root, Execute: true})
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	if sum.Restored != 0 {
		t.Errorf("after purge, Restored should be 0, got %d", sum.Restored)
	}
	if sum.Skipped == 0 {
		t.Error("after purge, expected skipped entries with trash_missing")
	}
	for _, e := range entries {
		if e.Reason != undo.ReasonTrashMissing {
			t.Errorf("entry reason = %s, want trash_missing", e.Reason)
		}
	}
}

// replaceAll is a tiny local helper to avoid pulling strings into tests
// that only need ReplaceAll once.
func replaceAll(s, old, new string) string {
	out := ""
	for {
		i := index(s, old)
		if i < 0 {
			return out + s
		}
		out += s[:i] + new
		s = s[i+len(old):]
	}
}

func index(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
