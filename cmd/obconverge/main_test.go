package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattjoyce/obconverge/internal/testvault"
)

// TestCLI_ScanThenClassify exercises the full cobra tree against a real vault.
// No mocks; no subprocess. We wire the root command, set args, execute.
func TestCLI_ScanThenClassify(t *testing.T) {
	vault := testvault.Build(t,
		testvault.File{Path: "Notes/Alpha.md", Content: "alpha\n"},
		testvault.File{Path: "Prod/Alpha.md", Content: "alpha\n"},
		testvault.File{Path: "Notes/Beta.md", Content: "beta\n"},
		testvault.File{Path: "Prod/Beta.md", Content: "beta\r\n"},
		testvault.File{Path: "Solo.md", Content: "alone\n"},
	)

	// scan
	cmd := newRoot()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"scan", "--vault", vault, "--log-level", "error"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("scan: %v\noutput: %s", err, out.String())
	}

	indexPath := filepath.Join(vault, ".obconverge", "index.jsonl")
	if _, err := os.Stat(indexPath); err != nil {
		t.Fatalf("index.jsonl not created: %v", err)
	}

	// classify
	out.Reset()
	cmd = newRoot()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"classify", "--vault", vault, "--log-level", "error"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("classify: %v\noutput: %s", err, out.String())
	}

	classPath := filepath.Join(vault, ".obconverge", "classification.jsonl")
	data, err := os.ReadFile(classPath)
	if err != nil {
		t.Fatalf("read classification.jsonl: %v", err)
	}
	body := string(data)
	for _, want := range []string{`"bucket":"EXACT"`, `"bucket":"CRLF-ONLY"`, `"bucket":"UNIQUE"`} {
		if !strings.Contains(body, want) {
			t.Errorf("classification.jsonl missing %q\nfull:\n%s", want, body)
		}
	}
}

func TestCLI_VersionFlag(t *testing.T) {
	cmd := newRoot()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--version"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("--version: %v", err)
	}
	got := strings.TrimSpace(out.String())
	// Default version stamp is "dev" unless the caller overrides via ldflags.
	if got == "" {
		t.Fatalf("--version printed nothing")
	}
}

func TestCLI_MissingVaultExitsUsage(t *testing.T) {
	// With no --vault flag and presumably no user config with vault_path,
	// scan should fail with a usage-style error.
	// Point HOME at a tempdir so we don't accidentally read the real config.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	cmd := newRoot()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"scan", "--log-level", "error"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error for missing vault, got nil\noutput: %s", out.String())
	}
	if !strings.Contains(err.Error(), "vault") {
		t.Errorf("error should mention vault, got: %v", err)
	}
}
