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
	"github.com/mattjoyce/obconverge/internal/policy"
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

// runApplyPolicy runs apply with a custom policy override (useful for
// exercising secret-response modes where the default action for SECRETS
// is quarantine).
func runApplyPolicy(t *testing.T, root string, execute bool, pol *policy.Policy, mode policy.SecretResponse) apply.Summary {
	t.Helper()
	sum, err := apply.Run(apply.Options{
		VaultRoot:      root,
		Execute:        execute,
		Policy:         pol,
		SecretResponse: mode,
		Now:            time.Date(2026, 4, 23, 10, 0, 1, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	return sum
}

// dropPolicy returns a policy that maps SECRETS -> drop so secrets
// response mode actually kicks in (the default quarantine is a no-op).
func dropPolicy() *policy.Policy {
	p := policy.Default()
	p.Rules[classify.BucketSecrets] = policy.ActionDrop
	return &p
}

func TestApply_RefusesSecretsBucket(t *testing.T) {
	root := setup(t,
		testvault.File{Path: "Notes/Keys.md", Content: "AKIAIOSFODNN7EXAMPLE\n"},
		testvault.File{Path: "Prod/Keys.md", Content: "AKIAIOSFODNN7EXAMPLE\n"},
	)
	tickAll(t, root)

	// Use drop policy so the SECRETS action is mutating; block mode
	// should then refuse.
	sum := runApplyPolicy(t, root, true, dropPolicy(), policy.SecretBlock)
	if sum.Refused != 1 {
		t.Errorf("Refused = %d, want 1: summary=%+v", sum.Refused, sum)
	}
	if sum.Applied != 0 {
		t.Errorf("Applied = %d, want 0 (SECRETS bucket, block mode)", sum.Applied)
	}

	journal := readJournal(t, root)
	found := false
	for _, e := range journal {
		if e.Result == apply.ResultRefused && e.Reason == apply.ReasonSecretsBucket {
			found = true
			if e.SecretPattern != "aws-access-key" {
				t.Errorf("SecretPattern = %q, want aws-access-key", e.SecretPattern)
			}
		}
	}
	if !found {
		t.Errorf("journal missing secrets_bucket refusal: %+v", journal)
	}
}

func TestApply_SecretsWarnMode_Proceeds(t *testing.T) {
	root := setup(t,
		testvault.File{Path: "Notes/Keys.md", Content: "AKIAIOSFODNN7EXAMPLE\n"},
		testvault.File{Path: "Prod/Keys.md", Content: "AKIAIOSFODNN7EXAMPLE\n"},
	)
	tickAll(t, root)

	sum := runApplyPolicy(t, root, true, dropPolicy(), policy.SecretWarn)
	if sum.Applied != 1 {
		t.Errorf("Applied = %d, want 1 (warn mode proceeds): summary=%+v", sum.Applied, sum)
	}
	if sum.Refused != 0 {
		t.Errorf("Refused = %d, want 0 in warn mode", sum.Refused)
	}

	// Journal must still stamp the secret pattern so the decision is auditable.
	journal := readJournal(t, root)
	found := false
	for _, e := range journal {
		if e.Result == apply.ResultApplied && e.SecretPattern == "aws-access-key" {
			found = true
		}
	}
	if !found {
		t.Errorf("journal should record SecretPattern on applied entry in warn mode: %+v", journal)
	}

	// Dropped file should have moved to trash.
	trashMatches, _ := filepath.Glob(filepath.Join(root, ".obconverge", "trash", "*", "Notes", "Keys.md"))
	if len(trashMatches) != 1 {
		t.Errorf("warn mode should drop Notes/Keys.md; trash = %v", trashMatches)
	}
}

func TestApply_SecretsSilentMode_Proceeds(t *testing.T) {
	root := setup(t,
		testvault.File{Path: "Notes/Keys.md", Content: "AKIAIOSFODNN7EXAMPLE\n"},
		testvault.File{Path: "Prod/Keys.md", Content: "AKIAIOSFODNN7EXAMPLE\n"},
	)
	tickAll(t, root)

	sum := runApplyPolicy(t, root, true, dropPolicy(), policy.SecretSilent)
	if sum.Applied != 1 {
		t.Errorf("Applied = %d, want 1 (silent mode proceeds): summary=%+v", sum.Applied, sum)
	}

	// Journal still records SecretPattern — silent only suppresses
	// operator-facing noise, not audit trail.
	journal := readJournal(t, root)
	found := false
	for _, e := range journal {
		if e.Result == apply.ResultApplied && e.SecretPattern == "aws-access-key" {
			found = true
		}
	}
	if !found {
		t.Errorf("journal should still record SecretPattern in silent mode: %+v", journal)
	}
}

func TestApply_SecretsWithQuarantinePolicy_NoOpRegardlessOfMode(t *testing.T) {
	// Default policy maps SECRETS -> quarantine, which is a no-op. Mode
	// selection is irrelevant here; nothing mutates.
	root := setup(t,
		testvault.File{Path: "Notes/Keys.md", Content: "AKIAIOSFODNN7EXAMPLE\n"},
		testvault.File{Path: "Prod/Keys.md", Content: "AKIAIOSFODNN7EXAMPLE\n"},
	)
	tickAll(t, root)

	for _, mode := range []policy.SecretResponse{policy.SecretBlock, policy.SecretWarn, policy.SecretSilent} {
		t.Run(string(mode), func(t *testing.T) {
			sum := runApplyPolicy(t, root, false, nil, mode) // dry-run, default policy
			if sum.Applied != 0 {
				t.Errorf("Applied = %d, want 0 (quarantine is no-op)", sum.Applied)
			}
			if sum.Refused != 0 {
				t.Errorf("Refused = %d, want 0 (quarantine is not mutating, no refuse needed)", sum.Refused)
			}
		})
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

func TestApply_MergeFrontmatter_TagUnion(t *testing.T) {
	root := setup(t,
		testvault.File{Path: "Notes/B.md", Content: "---\ntags:\n  - alpha\n  - beta\n---\n\nshared body\n"},
		testvault.File{Path: "Prod/B.md", Content: "---\ntags:\n  - beta\n  - gamma\n---\n\nshared body\n"},
	)
	tickAll(t, root)

	sum := runApply(t, root, true)
	if sum.Applied != 1 {
		t.Errorf("Applied = %d, want 1: %+v", sum.Applied, sum)
	}

	// Winner is paths[1] = Prod/B.md (lexicographically second).
	winner := filepath.Join(root, "Prod/B.md")
	loser := filepath.Join(root, "Notes/B.md")

	if _, err := os.Stat(loser); err == nil {
		t.Error("loser Notes/B.md should have moved to trash")
	}
	winBytes, err := os.ReadFile(winner)
	if err != nil {
		t.Fatalf("read winner: %v", err)
	}
	s := string(winBytes)
	for _, tag := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(s, tag) {
			t.Errorf("winner frontmatter missing tag %q; content:\n%s", tag, s)
		}
	}
	if strings.Count(s, "beta") != 1 {
		t.Errorf("tag 'beta' should appear once after set-union; content:\n%s", s)
	}
	if !strings.Contains(s, "shared body") {
		t.Errorf("winner body should be preserved; content:\n%s", s)
	}

	journal := readJournal(t, root)
	found := false
	for _, e := range journal {
		if e.Op == apply.OpMergeFrontmatter && e.Result == apply.ResultApplied {
			found = true
			if e.Path != "Prod/B.md" {
				t.Errorf("journal Path = %q, want Prod/B.md", e.Path)
			}
			if e.SecondaryPath != "Notes/B.md" {
				t.Errorf("journal SecondaryPath = %q, want Notes/B.md", e.SecondaryPath)
			}
			if e.TrashPath == "" {
				t.Error("journal TrashPath (winner backup) should be set for merge")
			}
			if e.SecondaryTrash == "" {
				t.Error("journal SecondaryTrash (loser trash) should be set for merge")
			}
		}
	}
	if !found {
		t.Errorf("journal missing applied merge-frontmatter: %+v", journal)
	}

	// The winner backup must contain the pre-rewrite bytes.
	backupPath := filepath.Join(root, ".obconverge", "trash", "20260423-100001", "Prod", "B.md")
	backupBytes, berr := os.ReadFile(backupPath)
	if berr != nil {
		t.Fatalf("read winner backup: %v", berr)
	}
	bs := string(backupBytes)
	if strings.Contains(bs, "alpha") {
		t.Errorf("winner backup should not contain merged tags (loser's alpha); got:\n%s", bs)
	}
	if !strings.Contains(bs, "beta") || !strings.Contains(bs, "gamma") {
		t.Errorf("winner backup should have winner's original tags beta/gamma; got:\n%s", bs)
	}
}

func TestApply_MergeFrontmatter_ScalarConflictRefuses(t *testing.T) {
	root := setup(t,
		testvault.File{Path: "Notes/C.md", Content: "---\ntitle: Win\n---\n\nshared body\n"},
		testvault.File{Path: "Prod/C.md", Content: "---\ntitle: Other\n---\n\nshared body\n"},
	)
	tickAll(t, root)

	sum := runApply(t, root, true)
	if sum.Refused != 1 {
		t.Errorf("Refused = %d, want 1 (scalar conflict): %+v", sum.Refused, sum)
	}
	if sum.Applied != 0 {
		t.Errorf("Applied = %d, want 0", sum.Applied)
	}

	// Both files must still exist — refused merges never mutate.
	if _, err := os.Stat(filepath.Join(root, "Notes/C.md")); err != nil {
		t.Errorf("Notes/C.md should remain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Prod/C.md")); err != nil {
		t.Errorf("Prod/C.md should remain: %v", err)
	}

	journal := readJournal(t, root)
	found := false
	for _, e := range journal {
		if e.Op == apply.OpMergeFrontmatter && e.Reason == apply.ReasonFrontmatterConflict {
			found = true
		}
	}
	if !found {
		t.Errorf("journal missing frontmatter_conflict refusal: %+v", journal)
	}
}

func TestApply_MergeFrontmatter_HashDrift(t *testing.T) {
	root := setup(t,
		testvault.File{Path: "Notes/D.md", Content: "---\ntags:\n  - a\n---\n\nshared body\n"},
		testvault.File{Path: "Prod/D.md", Content: "---\ntags:\n  - b\n---\n\nshared body\n"},
	)
	tickAll(t, root)

	// Mutate the loser (paths[0]) after plan was written.
	if err := os.WriteFile(filepath.Join(root, "Notes/D.md"), []byte("drifted\n"), 0o644); err != nil {
		t.Fatalf("mutate loser: %v", err)
	}

	sum := runApply(t, root, true)
	if sum.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1 (drift): %+v", sum.Skipped, sum)
	}
	// Winner must not have been modified.
	winBytes, err := os.ReadFile(filepath.Join(root, "Prod/D.md"))
	if err != nil {
		t.Fatalf("read winner: %v", err)
	}
	if !strings.Contains(string(winBytes), "- b") {
		t.Errorf("winner should be unchanged; content:\n%s", winBytes)
	}
	if strings.Contains(string(winBytes), "- a") {
		t.Errorf("winner must not have received loser's tag under drift; content:\n%s", winBytes)
	}
}

func TestApply_MergeFrontmatter_AddsLoserOnlyKey(t *testing.T) {
	root := setup(t,
		testvault.File{Path: "Notes/E.md", Content: "---\ntags:\n  - x\n---\n\nshared body\n"},
		testvault.File{Path: "Prod/E.md", Content: "---\ntags:\n  - x\nsource: https://example.com\n---\n\nshared body\n"},
	)
	tickAll(t, root)

	sum := runApply(t, root, true)
	if sum.Applied != 1 {
		t.Errorf("Applied = %d, want 1: %+v", sum.Applied, sum)
	}

	// Hmm wait — winner is paths[1] (Prod/E.md) which already has source:.
	// The merge should be a no-op for source: and preserve the tag.
	// Let's flip the fixture for clearer semantics: put the loser-only key
	// on Notes/E.md (paths[0]) so the merge has to graft it onto winner.
	// ... This is easier as a separate test case.
	// For now assert the file is still consistent.
	winBytes, err := os.ReadFile(filepath.Join(root, "Prod/E.md"))
	if err != nil {
		t.Fatalf("read winner: %v", err)
	}
	s := string(winBytes)
	if !strings.Contains(s, "source: https://example.com") {
		t.Errorf("source key should be present post-merge:\n%s", s)
	}
}

func TestApply_MergeFrontmatter_LoserOnlyKeyGraftedOntoWinner(t *testing.T) {
	// Loser (paths[0]=Notes/F.md) carries an extra key that must graft
	// onto winner (paths[1]=Prod/F.md).
	root := setup(t,
		testvault.File{Path: "Notes/F.md", Content: "---\ntags:\n  - shared\nsource: https://example.com\n---\n\nshared body\n"},
		testvault.File{Path: "Prod/F.md", Content: "---\ntags:\n  - shared\n---\n\nshared body\n"},
	)
	tickAll(t, root)

	sum := runApply(t, root, true)
	if sum.Applied != 1 {
		t.Errorf("Applied = %d, want 1: %+v", sum.Applied, sum)
	}

	winBytes, err := os.ReadFile(filepath.Join(root, "Prod/F.md"))
	if err != nil {
		t.Fatalf("read winner: %v", err)
	}
	s := string(winBytes)
	if !strings.Contains(s, "source: https://example.com") {
		t.Errorf("loser-only key 'source' should be grafted onto winner:\n%s", s)
	}
	if !strings.Contains(s, "shared body") {
		t.Errorf("body should be preserved:\n%s", s)
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
