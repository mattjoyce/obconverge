// Package secrets detects credential-shaped strings in note content.
//
// Patterns are values, and a Detector is a value built from a list of them.
// The package has no state: callers build a Detector once (typically in
// main after reading the embed and any user extensions) and pass it to
// whatever phase needs secret detection.
//
// Patterns ship in patterns.json (embedded at build time). Operators may
// ADD patterns via ~/.config/obconverge/secret_patterns.json; they cannot
// REMOVE or SHADOW built-ins. This is a security tool — the shipped
// patterns are non-negotiable.
//
// Per SPEC.md "Secret protection": a file in the SECRETS bucket must NEVER
// have its content printed to stdout, stderr, log, or plan. The API here
// returns only a boolean and a pattern NAME — matched bytes never leave
// the package.
package secrets

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

//go:embed patterns.json
var builtinPatternsJSON []byte

// Pattern is a compiled credential detector.
type Pattern struct {
	Name        string
	Regex       *regexp.Regexp
	Description string
}

type patternFile struct {
	Patterns []patternSpec `json:"patterns"`
}

type patternSpec struct {
	Name        string `json:"name"`
	Regex       string `json:"regex"`
	Description string `json:"description,omitempty"`
}

// Builtins returns the shipped pattern set, freshly parsed from the
// embedded JSON. It is a pure function of the embed.
func Builtins() ([]Pattern, error) {
	return parseSpecs(builtinPatternsJSON)
}

// BuiltinNames returns just the names from Builtins(). Convenience for
// drift-testing against the skills descriptor.
func BuiltinNames() []string {
	built, err := Builtins()
	if err != nil {
		// The embed is bundled with the binary; a failure here means the
		// binary was built from a bad source tree.
		panic(fmt.Sprintf("secrets: built-in patterns invalid: %v", err))
	}
	out := make([]string, 0, len(built))
	for _, p := range built {
		out = append(out, p.Name)
	}
	return out
}

// ParseFile reads a user-supplied patterns file and returns the parsed,
// compiled list. A missing file returns (nil, nil); the caller decides
// whether that's OK for their use case.
func ParseFile(path string) ([]Pattern, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("secrets: read %s: %w", path, err)
	}
	patterns, err := parseSpecs(data)
	if err != nil {
		return nil, fmt.Errorf("secrets: parse %s: %w", path, err)
	}
	return patterns, nil
}

// Combine merges base and extra into a single pattern list. Any name
// collision (between base and extra, or within extra) is a hard error:
// users add, never shadow. This is the "additive, no-shadow" policy.
func Combine(base, extra []Pattern) ([]Pattern, error) {
	seen := make(map[string]bool, len(base)+len(extra))
	out := make([]Pattern, 0, len(base)+len(extra))
	for _, p := range base {
		if seen[p.Name] {
			return nil, fmt.Errorf("secrets: duplicate name %q in base patterns", p.Name)
		}
		seen[p.Name] = true
		out = append(out, p)
	}
	for _, p := range extra {
		if seen[p.Name] {
			return nil, fmt.Errorf("secrets: user pattern %q collides with an existing name", p.Name)
		}
		seen[p.Name] = true
		out = append(out, p)
	}
	return out, nil
}

// DefaultUserExtensionPath returns ~/.config/obconverge/secret_patterns.json.
func DefaultUserExtensionPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "obconverge", "secret_patterns.json"), nil
}

// Detector is an immutable value that matches content against a fixed set
// of patterns. Build one with New; share freely across goroutines.
type Detector struct {
	patterns []Pattern
}

// New constructs a Detector over the given patterns. The caller is
// responsible for having run Combine (or equivalent) beforehand.
func New(patterns []Pattern) *Detector {
	// Defensive copy so the caller's slice can't be mutated through the
	// returned detector's view.
	p := make([]Pattern, len(patterns))
	copy(p, patterns)
	return &Detector{patterns: p}
}

// NewBuiltins is a convenience that returns a Detector configured with
// only the shipped built-in patterns. Useful for tests and for callers
// that don't need user extensions.
func NewBuiltins() *Detector {
	built, err := Builtins()
	if err != nil {
		panic(fmt.Sprintf("secrets: built-in patterns invalid: %v", err))
	}
	return New(built)
}

// Detect scans content and returns (matched, patternName). patternName is
// the name of the first matching pattern; the content itself is never
// returned.
func (d *Detector) Detect(content []byte) (bool, string) {
	for _, p := range d.patterns {
		if p.Regex.Match(content) {
			return true, p.Name
		}
	}
	return false, ""
}

// Contains is a convenience predicate for callers that only need the verdict.
func (d *Detector) Contains(content []byte) bool {
	matched, _ := d.Detect(content)
	return matched
}

// Patterns returns a copy of the patterns this detector was built with.
func (d *Detector) Patterns() []Pattern {
	out := make([]Pattern, len(d.patterns))
	copy(out, d.patterns)
	return out
}

func parseSpecs(data []byte) ([]Pattern, error) {
	var file patternFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	if len(file.Patterns) == 0 {
		return nil, errors.New("no patterns declared")
	}
	out := make([]Pattern, 0, len(file.Patterns))
	seen := map[string]bool{}
	for _, s := range file.Patterns {
		if s.Name == "" {
			return nil, errors.New("pattern missing name")
		}
		if s.Regex == "" {
			return nil, fmt.Errorf("pattern %q: missing regex", s.Name)
		}
		if seen[s.Name] {
			return nil, fmt.Errorf("pattern %q: duplicate name", s.Name)
		}
		seen[s.Name] = true
		re, err := regexp.Compile(s.Regex)
		if err != nil {
			return nil, fmt.Errorf("pattern %q: compile regex: %w", s.Name, err)
		}
		out = append(out, Pattern{Name: s.Name, Regex: re, Description: s.Description})
	}
	return out, nil
}
