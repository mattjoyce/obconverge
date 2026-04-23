// Package secrets detects credential-shaped strings in note content.
//
// Patterns live in patterns.json (embedded at build time). Operators may
// ADD patterns at runtime via ~/.config/obconverge/secret_patterns.json;
// they cannot REMOVE or SHADOW built-ins. This is a security tool — the
// shipped patterns are non-negotiable.
//
// The detector is intentionally pattern-based and conservative. False
// positives are cheap (a file routes into the SECRETS bucket and the
// operator reviews it in Obsidian); false negatives leak credentials into
// terminal scrollback and plan files. Err toward over-matching.
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
	"sync"
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

var (
	mu       sync.RWMutex
	patterns []Pattern // mutated only via Reset / LoadUserExtensions
)

func init() {
	built, err := parseSpecs(builtinPatternsJSON)
	if err != nil {
		// A broken built-in file is a build-time failure, not a runtime
		// one — but go:embed happened at build time, so this can only
		// fire if the file was hand-edited into invalid JSON.
		panic(fmt.Sprintf("secrets: built-in patterns.json invalid: %v", err))
	}
	patterns = built
}

// Reset reloads only the built-in patterns, discarding any user extensions.
// Intended for tests; safe in production too.
func Reset() {
	built, err := parseSpecs(builtinPatternsJSON)
	if err != nil {
		panic(fmt.Sprintf("secrets: built-in patterns.json invalid: %v", err))
	}
	mu.Lock()
	patterns = built
	mu.Unlock()
}

// BuiltinNames returns the names of the shipped built-in patterns. Used for
// drift-testing the skills descriptor against this package.
func BuiltinNames() []string {
	built, _ := parseSpecs(builtinPatternsJSON)
	out := make([]string, 0, len(built))
	for _, p := range built {
		out = append(out, p.Name)
	}
	return out
}

// DefaultUserExtensionPath returns the standard location obconverge checks
// for user-defined extra patterns: ~/.config/obconverge/secret_patterns.json.
func DefaultUserExtensionPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "obconverge", "secret_patterns.json"), nil
}

// LoadUserExtensions reads an optional patterns JSON file and appends any
// new patterns to the global list. A missing file is not an error. A
// pattern whose name collides with a built-in (or another user pattern)
// is a hard error — users add, never shadow.
func LoadUserExtensions(path string) error {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("secrets: read %s: %w", path, err)
	}
	extra, err := parseSpecs(data)
	if err != nil {
		return fmt.Errorf("secrets: parse %s: %w", path, err)
	}

	mu.Lock()
	defer mu.Unlock()
	existing := map[string]bool{}
	for _, p := range patterns {
		existing[p.Name] = true
	}
	for _, p := range extra {
		if existing[p.Name] {
			return fmt.Errorf("secrets: user pattern %q collides with existing name (file: %s)", p.Name, path)
		}
		existing[p.Name] = true
	}
	patterns = append(patterns, extra...)
	return nil
}

// Detect scans content and returns (matched, patternName). patternName is
// the name of the first matching pattern; the content itself is never
// returned.
func Detect(content []byte) (bool, string) {
	mu.RLock()
	list := patterns
	mu.RUnlock()
	for _, p := range list {
		if p.Regex.Match(content) {
			return true, p.Name
		}
	}
	return false, ""
}

// Contains is a convenience predicate for callers that only need the verdict.
func Contains(content []byte) bool {
	matched, _ := Detect(content)
	return matched
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
