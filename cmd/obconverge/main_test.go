package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/mattjoyce/obconverge/internal/skills"
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

func TestCLI_SkillsFlag(t *testing.T) {
	cmd := newRoot()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--skills"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("--skills: %v", err)
	}
	body := out.String()
	if !strings.Contains(body, "obconverge") || !strings.Contains(body, "## Subcommands") {
		t.Errorf("--skills output missing expected sections:\n%s", body)
	}
}

func TestCLI_SkillsJSONFlag(t *testing.T) {
	cmd := newRoot()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--skills-json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("--skills-json: %v", err)
	}
	var raw any
	if err := json.Unmarshal(out.Bytes(), &raw); err != nil {
		t.Fatalf("--skills-json is not valid JSON: %v\n%s", err, out.String())
	}
}

// TestCLI_DescriptorMatchesCobraTree is the drift guard: every subcommand
// and every non-persistent flag declared in the descriptor must exist in
// the cobra tree, and vice versa. If this fails, the descriptor is lying
// about what the binary accepts.
func TestCLI_DescriptorMatchesCobraTree(t *testing.T) {
	d, err := skills.Parse()
	if err != nil {
		t.Fatalf("skills.Parse: %v", err)
	}

	root := newRoot()

	// Subcommand name set.
	cobraSubs := map[string]*cobra.Command{}
	for _, c := range root.Commands() {
		// cobra auto-injects "help" and "completion"; skip them.
		if c.Name() == "help" || c.Name() == "completion" {
			continue
		}
		cobraSubs[c.Name()] = c
	}
	descSubs := map[string]skills.SubcommandSpec{}
	for _, s := range d.Subcommands {
		descSubs[s.Name] = s
	}

	for name := range cobraSubs {
		if _, ok := descSubs[name]; !ok {
			t.Errorf("cobra has subcommand %q not in descriptor", name)
		}
	}
	for name := range descSubs {
		if _, ok := cobraSubs[name]; !ok {
			t.Errorf("descriptor has subcommand %q not in cobra", name)
		}
	}

	// Flags per subcommand: compare descriptor flags against cobra LOCAL
	// flags (inherited persistent flags live in the top-level list).
	for name, cmd := range cobraSubs {
		spec := descSubs[name]

		cobraFlags := map[string]bool{}
		cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
			cobraFlags[f.Name] = true
		})

		descFlags := map[string]bool{}
		for _, f := range spec.Flags {
			descFlags[f.Long] = true
		}

		for flagName := range cobraFlags {
			if !descFlags[flagName] {
				t.Errorf("subcommand %q: cobra has flag --%s not in descriptor", name, flagName)
			}
		}
		for flagName := range descFlags {
			if !cobraFlags[flagName] {
				t.Errorf("subcommand %q: descriptor has flag --%s not in cobra", name, flagName)
			}
		}
	}

	// Top-level persistent flags should exist on root.
	for _, f := range d.PersistentFlags {
		// Persistent flags include version/skills/skills-json which are local
		// to root (not PersistentFlags in cobra terms). Accept either.
		if root.PersistentFlags().Lookup(f.Long) == nil && root.Flags().Lookup(f.Long) == nil {
			t.Errorf("descriptor declares persistent flag --%s but cobra root has no such flag", f.Long)
		}
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
