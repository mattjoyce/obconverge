// Package invariants holds cross-package tests that enforce spec-level
// stance — the stuff that would otherwise be prose in SPEC.md. This is
// the "stance as code" commitment from the Invariant Tests section of
// the spec.
package invariants

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// TestPurityImportGraph enforces SPEC.md's purity invariant: pure read-
// phase packages must not transitively import any package capable of
// mutating the vault. Only internal/apply and internal/undo are allowed
// to mutate; everything else is read-only by construction. If this test
// ever fails, someone tried to smuggle a writer into a pure phase.
func TestPurityImportGraph(t *testing.T) {
	pureRoots := []string{
		"github.com/mattjoyce/obconverge/internal/scan",
		"github.com/mattjoyce/obconverge/internal/classify",
		"github.com/mattjoyce/obconverge/internal/plan",
		"github.com/mattjoyce/obconverge/internal/links",
		"github.com/mattjoyce/obconverge/internal/frontmatter",
		"github.com/mattjoyce/obconverge/internal/secrets",
		"github.com/mattjoyce/obconverge/internal/policy",
		"github.com/mattjoyce/obconverge/internal/hashing",
		"github.com/mattjoyce/obconverge/internal/artifact",
		"github.com/mattjoyce/obconverge/internal/config",
		"github.com/mattjoyce/obconverge/internal/logging",
		"github.com/mattjoyce/obconverge/internal/errcode",
		"github.com/mattjoyce/obconverge/internal/testvault",
		"github.com/mattjoyce/obconverge/internal/skills",
	}
	forbidden := map[string]bool{
		"github.com/mattjoyce/obconverge/internal/apply": true,
		"github.com/mattjoyce/obconverge/internal/undo":  true,
	}

	for _, pkg := range pureRoots {
		shortName := strings.TrimPrefix(pkg, "github.com/mattjoyce/obconverge/")
		t.Run(shortName, func(t *testing.T) {
			for _, dep := range transitiveDeps(t, pkg) {
				if forbidden[dep] {
					t.Errorf("%s transitively imports forbidden write-phase package %s", pkg, dep)
				}
			}
		})
	}
}

// transitiveDeps returns the set of import paths reachable from pkg,
// using `go list -deps`. The list includes pkg itself plus every
// direct and transitive dependency.
func transitiveDeps(t *testing.T, pkg string) []string {
	t.Helper()
	// #nosec G204 — pkg comes from our own hardcoded list, not input.
	cmd := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", pkg)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go list -deps %s: %v\nstderr: %s", pkg, err, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	return lines
}

// TestOnlyWritePhasesUseOsWrite is a companion assertion: it's the
// inverse test — apply and undo SHOULD appear in the set of packages
// that reach os.WriteFile / os.Rename / os.Remove. This catches the
// scenario where someone refactors a writer out of apply into a helper
// package that then gets imported by a pure phase.
func TestOnlyWritePhasesUseOsWrite(t *testing.T) {
	// Packages expected to perform file mutations.
	expectedWriters := map[string]bool{
		"github.com/mattjoyce/obconverge/internal/apply": true,
		"github.com/mattjoyce/obconverge/internal/undo":  true,
		// testvault legitimately writes fixture files in tests. Allowed
		// because it only exists at test-time; it isn't linked into the
		// binary (the compiler elides test-only packages from the final
		// executable).
		"github.com/mattjoyce/obconverge/internal/testvault": true,
		// artifact writes .jsonl files — but only to paths its callers
		// pass. scan / classify / plan produce artifacts *about* the
		// vault, not inside the vault's user-note tree; the spec
		// explicitly allows this.
		"github.com/mattjoyce/obconverge/internal/artifact": true,
	}
	_ = expectedWriters
	// This is a sanity comment, not a currently-enforceable assertion
	// at the import level — go/packages can't tell "imports os" from
	// "uses os.WriteFile specifically". A stronger invariant would use
	// golang.org/x/tools/go/analysis to look for actual call-sites.
	// For now the test is aspirational; the commit message is the
	// spec. Leaving the skeleton here so a future commit can sharpen
	// it without bikeshedding where to put the file.
	t.Skip("call-site analysis deferred; purity import-graph test is the live guard")
}
