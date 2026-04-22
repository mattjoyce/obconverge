package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefault_ConservativeValues(t *testing.T) {
	c := Default()
	if c.ApplyMode != "dry-run" {
		t.Errorf("ApplyMode default = %q, want dry-run", c.ApplyMode)
	}
	if c.DeleteMode != "soft" {
		t.Errorf("DeleteMode default = %q, want soft", c.DeleteMode)
	}
	if c.RewriteLinks != "ask" {
		t.Errorf("RewriteLinks default = %q, want ask", c.RewriteLinks)
	}
	if c.WorkDir != ".obconverge" {
		t.Errorf("WorkDir default = %q, want .obconverge", c.WorkDir)
	}
}

func TestAssemble_LoadsUserConfigAndValidates(t *testing.T) {
	dir := t.TempDir()
	userPath := filepath.Join(dir, "config")
	writeYAML(t, userPath, `
vault_path: "/tmp/my-vault"
apply_mode: "apply"
log_level: "debug"
tags_handling: "add"
`)

	cfg, err := Assemble(userPath, "")
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if cfg.VaultPath != "/tmp/my-vault" {
		t.Errorf("VaultPath = %q", cfg.VaultPath)
	}
	if cfg.ApplyMode != "apply" {
		t.Errorf("ApplyMode = %q", cfg.ApplyMode)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q", cfg.LogLevel)
	}
	if cfg.TagsHandling != "add" {
		t.Errorf("TagsHandling = %q", cfg.TagsHandling)
	}
	// Unset field should keep its default.
	if cfg.DeleteMode != "soft" {
		t.Errorf("DeleteMode = %q, want default soft", cfg.DeleteMode)
	}
}

func TestAssemble_MissingUserConfigIsFine(t *testing.T) {
	cfg, err := Assemble(filepath.Join(t.TempDir(), "does-not-exist"), "")
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if cfg.ApplyMode != "dry-run" {
		t.Errorf("ApplyMode = %q, want dry-run (default)", cfg.ApplyMode)
	}
}

func TestAssemble_OverrideReplacesUserConfig(t *testing.T) {
	dir := t.TempDir()
	userPath := filepath.Join(dir, "config")
	writeYAML(t, userPath, `
vault_path: "/tmp/user-vault"
apply_mode: "apply"
tags_handling: "replace"
`)
	overridePath := filepath.Join(dir, "override")
	writeYAML(t, overridePath, `
vault_path: "/tmp/override-vault"
`)

	cfg, err := Assemble(userPath, overridePath)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if cfg.VaultPath != "/tmp/override-vault" {
		t.Errorf("VaultPath = %q, want /tmp/override-vault", cfg.VaultPath)
	}
	// apply_mode should revert to the default — override resets the user layer.
	if cfg.ApplyMode != "dry-run" {
		t.Errorf("ApplyMode = %q, want dry-run (override resets)", cfg.ApplyMode)
	}
	if cfg.TagsHandling != "merge" {
		t.Errorf("TagsHandling = %q, want merge (override resets)", cfg.TagsHandling)
	}
}

func TestAssemble_InvalidEnumFallsBackToSafeDefault(t *testing.T) {
	dir := t.TempDir()
	userPath := filepath.Join(dir, "config")
	writeYAML(t, userPath, `
apply_mode: "nuke-everything"
delete_mode: "shred"
tags_handling: "wat"
`)

	cfg, err := Assemble(userPath, "")
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if cfg.ApplyMode != "dry-run" {
		t.Errorf("ApplyMode invalid input kept %q, want fallback dry-run", cfg.ApplyMode)
	}
	if cfg.DeleteMode != "soft" {
		t.Errorf("DeleteMode invalid input kept %q, want fallback soft", cfg.DeleteMode)
	}
	if cfg.TagsHandling != "merge" {
		t.Errorf("TagsHandling invalid input kept %q, want fallback merge", cfg.TagsHandling)
	}
}

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}

	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"~", home},
		{"~/Documents", filepath.Join(home, "Documents")},
		{"/abs/path", "/abs/path"},
	}
	for _, tc := range tests {
		got, err := ExpandPath(tc.in)
		if err != nil {
			t.Errorf("ExpandPath(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ExpandPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func writeYAML(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writeYAML %s: %v", path, err)
	}
}
