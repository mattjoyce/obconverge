// Package skills exposes the embedded capability descriptor that
// obconverge ships for agent consumption.
//
// Both the JSON and markdown forms are authored by hand in this package and
// embedded via //go:embed. The CLI exposes them through --skills (markdown)
// and --skills-json (JSON) top-level flags.
//
// A drift test in this package parses the JSON descriptor and cross-checks
// it against the cobra command tree; CI will refuse a commit where the
// descriptor and the actual flag surface disagree.
package skills

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed obconverge.lx.json
var jsonBytes []byte

//go:embed obconverge.lx.md
var markdownBytes []byte

// Markdown returns the human-readable descriptor.
func Markdown() []byte { return markdownBytes }

// JSON returns the machine-readable descriptor as raw bytes.
func JSON() []byte { return jsonBytes }

// Descriptor is the parsed shape of the JSON descriptor. This is the
// contract the drift test asserts against.
type Descriptor struct {
	Tool            string           `json:"tool"`
	Version         string           `json:"version"`
	Description     string           `json:"description"`
	Subcommands     []SubcommandSpec `json:"subcommands"`
	PersistentFlags []FlagSpec       `json:"persistent_flags"`
	Buckets         []BucketSpec     `json:"buckets"`
	Actions         []ActionSpec     `json:"actions"`
	ExitCodes       []ExitCodeSpec   `json:"exit_codes"`
	Artifacts       []ArtifactSpec   `json:"artifacts"`
	SecretPatterns  []SecretSpec     `json:"secret_patterns"`
}

// SubcommandSpec describes one subcommand.
type SubcommandSpec struct {
	Name    string     `json:"name"`
	Summary string     `json:"summary"`
	Reads   []string   `json:"reads,omitempty"`
	Writes  []string   `json:"writes,omitempty"`
	Flags   []FlagSpec `json:"flags"`
	Notes   []string   `json:"notes,omitempty"`
}

// FlagSpec describes one CLI flag.
type FlagSpec struct {
	Long        string `json:"long"`
	Short       string `json:"short,omitempty"`
	Type        string `json:"type"`
	Required    bool   `json:"required,omitempty"`
	Description string `json:"description"`
}

// BucketSpec describes a classifier bucket.
type BucketSpec struct {
	Name          string `json:"name"`
	Condition     string `json:"condition"`
	DefaultAction string `json:"default_action"`
	Implemented   bool   `json:"implemented"`
}

// ActionSpec describes a policy action.
type ActionSpec struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ExitCodeSpec describes a CLI exit code.
type ExitCodeSpec struct {
	Code    int    `json:"code"`
	Meaning string `json:"meaning"`
}

// ArtifactSpec describes one on-disk artifact.
type ArtifactSpec struct {
	Name        string `json:"name"`
	Schema      string `json:"schema"`
	Description string `json:"description"`
}

// SecretSpec names a credential detector pattern.
type SecretSpec struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Parse decodes the embedded JSON into a Descriptor.
func Parse() (Descriptor, error) {
	var d Descriptor
	if err := json.Unmarshal(JSON(), &d); err != nil {
		return d, fmt.Errorf("skills: parse embedded JSON: %w", err)
	}
	return d, nil
}
