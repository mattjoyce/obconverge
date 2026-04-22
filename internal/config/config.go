// Package config loads obconverge's user-level YAML config.
//
// The pattern mirrors obsave: hard-coded defaults, then a YAML file at
// ~/.config/obconverge/config, then an optional named override file. CLI
// flag overrides are applied by the caller after Assemble returns.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config holds obconverge CLI defaults loaded from YAML.
type Config struct {
	VaultPath          string `yaml:"vault_path"`
	WorkDir            string `yaml:"work_dir"`
	PolicyFile         string `yaml:"policy_file"`
	IgnoreFile         string `yaml:"ignore_file"`
	ApplyMode          string `yaml:"apply_mode"`
	RewriteLinks       string `yaml:"rewrite_links"`
	DeleteMode         string `yaml:"delete_mode"`
	LogLevel           string `yaml:"log_level"`
	LogFormat          string `yaml:"log_format"`
	TagsHandling       string `yaml:"tags_handling"`
	AliasesHandling    string `yaml:"aliases_handling"`
	PropertiesHandling string `yaml:"properties_handling"`
}

// Default returns the hard-coded safe defaults. Layer 1 of precedence.
// Every enum field carries the conservative value.
func Default() Config {
	return Config{
		WorkDir:            ".obconverge",
		PolicyFile:         "policy.yaml",
		IgnoreFile:         "ignore",
		ApplyMode:          "dry-run",
		RewriteLinks:       "ask",
		DeleteMode:         "soft",
		LogLevel:           "info",
		LogFormat:          "text",
		TagsHandling:       "merge",
		AliasesHandling:    "merge",
		PropertiesHandling: "merge",
	}
}

// DefaultUserConfigPath returns ~/.config/obconverge/config.
func DefaultUserConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "obconverge", "config"), nil
}

// Load reads a YAML config file and merges its fields over c. A missing file
// is not an error — it's the steady-state for a user who hasn't created one.
func (c *Config) Load(path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("config: read %s: %w", path, err)
	}
	return yaml.Unmarshal(data, c)
}

// LoadRequired reads a YAML config file; missing is an error.
func (c *Config) LoadRequired(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("config: read %s: %w", path, err)
	}
	return yaml.Unmarshal(data, c)
}

// Assemble runs the three-layer precedence: defaults → user config → override.
// If overridePath is non-empty it *replaces* the user config layer entirely,
// starting from defaults. This matches obsave's semantics.
//
// CLI flag overrides happen in the caller (layer 4).
func Assemble(userConfigPath, overrideConfigPath string) (Config, error) {
	cfg := Default()
	if userConfigPath != "" {
		if err := cfg.Load(userConfigPath); err != nil {
			return cfg, err
		}
	}
	if overrideConfigPath != "" {
		cfg = Default()
		if err := cfg.LoadRequired(overrideConfigPath); err != nil {
			return cfg, err
		}
	}
	cfg.validate()
	return cfg, nil
}

// ExpandPath handles ~ and ~/foo, then cleans and absolutizes.
func ExpandPath(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	switch {
	case p == "~":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = home
	case strings.HasPrefix(p, "~/"):
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(home, p[2:])
	}
	return filepath.Abs(filepath.Clean(p))
}

// validate coerces invalid enum values back to conservative defaults. The
// spec says: "Unknown enum values fall back to the safe default with a log
// line, not a panic." Logging is the caller's job; this just corrects.
func (c *Config) validate() {
	c.ApplyMode = validOr(c.ApplyMode, []string{"dry-run", "apply"}, "dry-run")
	c.RewriteLinks = validOr(c.RewriteLinks, []string{"never", "ask", "always"}, "ask")
	c.DeleteMode = validOr(c.DeleteMode, []string{"soft", "hard"}, "soft")
	c.LogLevel = validOr(c.LogLevel, []string{"debug", "info", "warn", "error"}, "info")
	c.LogFormat = validOr(c.LogFormat, []string{"text", "json"}, "text")
	c.TagsHandling = validOr(c.TagsHandling, []string{"replace", "add", "merge"}, "merge")
	c.AliasesHandling = validOr(c.AliasesHandling, []string{"replace", "add", "merge"}, "merge")
	c.PropertiesHandling = validOr(c.PropertiesHandling, []string{"replace", "add", "merge"}, "merge")
}

func validOr(got string, allowed []string, fallback string) string {
	if got == "" {
		return fallback
	}
	for _, a := range allowed {
		if got == a {
			return got
		}
	}
	return fallback
}
