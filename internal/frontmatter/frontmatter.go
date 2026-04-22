// Package frontmatter splits Obsidian notes into their YAML frontmatter and
// body, and extracts the fields we use for classification.
//
// Obsidian frontmatter must:
//   - start at byte 0 with the delimiter line "---" followed by a newline,
//   - end with another "---" line,
//   - contain YAML between the delimiters.
//
// Anything else (including frontmatter not starting at byte 0) is treated as
// plain body — that matches Obsidian's own parsing.
package frontmatter

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// Split separates a note's frontmatter bytes from its body bytes. If no valid
// frontmatter is present, frontmatterBytes is nil and body is content.
//
// Delimiter lines are not included in either slice. The LF after the closing
// "---" is consumed; a single blank line following it (the convention) is
// also consumed so that "body" starts at the first real body byte.
func Split(content []byte) (frontmatterBytes, body []byte) {
	// Must start with "---" followed by \n or \r\n.
	openEnd := matchDelim(content, 0)
	if openEnd < 0 {
		return nil, content
	}

	// Find the next delimiter line.
	closeStart, closeEnd := findClosingDelim(content, openEnd)
	if closeStart < 0 {
		return nil, content
	}

	fm := content[openEnd:closeStart]
	rest := content[closeEnd:]
	// Drop one leading blank line if present ("---\n\nbody...").
	switch {
	case bytes.HasPrefix(rest, []byte("\n")):
		rest = rest[1:]
	case bytes.HasPrefix(rest, []byte("\r\n")):
		rest = rest[2:]
	}
	return fm, rest
}

// Fields carries the frontmatter values we care about for classification.
// Unknown keys are ignored — obconverge doesn't need to round-trip every key.
type Fields struct {
	Tags    []string `yaml:"tags"`
	Aliases []string `yaml:"aliases"`
	Source  string   `yaml:"source"`
}

// ExtractFields parses a frontmatter block. An empty block returns a zero-value
// Fields and no error.
func ExtractFields(frontmatterBytes []byte) (Fields, error) {
	var f Fields
	if len(bytes.TrimSpace(frontmatterBytes)) == 0 {
		return f, nil
	}
	if err := yaml.Unmarshal(frontmatterBytes, &f); err != nil {
		return f, fmt.Errorf("frontmatter: parse yaml: %w", err)
	}
	return f, nil
}

// matchDelim returns the byte offset immediately after "---\n" or "---\r\n"
// starting at offset i, or -1 if the content does not begin a delimiter line
// at that position.
func matchDelim(content []byte, i int) int {
	if i+3 > len(content) {
		return -1
	}
	if content[i] != '-' || content[i+1] != '-' || content[i+2] != '-' {
		return -1
	}
	rest := content[i+3:]
	switch {
	case bytes.HasPrefix(rest, []byte("\n")):
		return i + 4
	case bytes.HasPrefix(rest, []byte("\r\n")):
		return i + 5
	}
	return -1
}

// findClosingDelim locates the "---" line that closes the frontmatter. It
// returns (lineStart, afterLineEnd) — everything between openEnd and lineStart
// is the frontmatter YAML. Returns (-1, -1) if no closing delimiter is found.
func findClosingDelim(content []byte, openEnd int) (int, int) {
	i := openEnd
	for i < len(content) {
		lineStart := i
		// Find end of line.
		j := lineStart
		for j < len(content) && content[j] != '\n' {
			j++
		}
		lineEnd := j // points at '\n' or len(content)
		// Line content is content[lineStart:lineEnd], possibly with trailing \r.
		line := content[lineStart:lineEnd]
		line = bytes.TrimRight(line, "\r")
		if bytes.Equal(line, []byte("---")) {
			// afterLineEnd includes the '\n' if present.
			after := lineEnd
			if after < len(content) && content[after] == '\n' {
				after++
			}
			return lineStart, after
		}
		if lineEnd >= len(content) {
			return -1, -1
		}
		i = lineEnd + 1
	}
	return -1, -1
}
