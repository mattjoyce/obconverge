package frontmatter

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// ConflictKind tags the kind of merge conflict that MergeUnion refuses.
type ConflictKind string

const (
	ConflictScalar ConflictKind = "scalar_conflict"
	ConflictType   ConflictKind = "type_conflict"
)

// ConflictError is returned from MergeUnion when the two documents cannot
// be unioned without losing information. The caller (typically apply)
// surfaces this as a refusal so the operator resolves the conflict in
// Obsidian directly.
type ConflictError struct {
	Key  string
	Kind ConflictKind
	Why  string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("frontmatter: %s on key %q: %s", e.Kind, e.Key, e.Why)
}

// MergeUnion merges two frontmatter YAML documents under the "additive
// union" rule from SPEC.md:
//
//   - keys unique to one side: keep.
//   - keys present on both with equal scalar values: keep once (winner's
//     node wins so surrounding style is preserved).
//   - list-valued keys on both sides: set-union of scalar elements,
//     preserving winner's order, then appending loser's new items.
//   - scalar conflicts (same key, different scalar values): ConflictError.
//   - type conflicts (e.g. list on one side, scalar on the other): ConflictError.
//
// winner and loser are the raw frontmatter bytes *without* the "---"
// delimiters. The return value is the merged YAML, also without
// delimiters; the caller wraps it for writing back to disk.
//
// Order is preserved: winner's keys appear in their original order,
// followed by any keys unique to loser (in loser's order).
func MergeUnion(winner, loser []byte) ([]byte, error) {
	winMap, err := parseMapping(winner)
	if err != nil {
		return nil, fmt.Errorf("frontmatter: parse winner: %w", err)
	}
	loseMap, err := parseMapping(loser)
	if err != nil {
		return nil, fmt.Errorf("frontmatter: parse loser: %w", err)
	}

	// Identity case: nothing from loser to add.
	if loseMap == nil || len(loseMap.Content) == 0 {
		return reemit(winMap)
	}
	if winMap == nil || len(winMap.Content) == 0 {
		return reemit(loseMap)
	}

	// Start from a shallow clone of the winner mapping so we preserve its
	// key order and any style metadata yaml.v3 tracks on the node.
	merged := *winMap
	merged.Content = append([]*yaml.Node(nil), winMap.Content...)

	// Index winner's keys for lookup (Content pairs are [key, value, ...]).
	idxByKey := make(map[string]int, len(merged.Content)/2)
	for i := 0; i < len(merged.Content); i += 2 {
		idxByKey[merged.Content[i].Value] = i
	}

	for i := 0; i < len(loseMap.Content); i += 2 {
		keyNode := loseMap.Content[i]
		loserVal := loseMap.Content[i+1]
		if idx, ok := idxByKey[keyNode.Value]; !ok {
			// Loser-only key: append (key, value).
			merged.Content = append(merged.Content, keyNode, loserVal)
		} else {
			mergedVal, err := mergeValueNodes(merged.Content[idx+1], loserVal, keyNode.Value)
			if err != nil {
				return nil, err
			}
			merged.Content[idx+1] = mergedVal
		}
	}

	return reemit(&merged)
}

// parseMapping decodes a frontmatter blob into its mapping node.
// Returns nil, nil if the input is empty/whitespace.
func parseMapping(data []byte) (*yaml.Node, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	// doc is typically a DocumentNode whose single Content is the mapping.
	if doc.Kind == yaml.DocumentNode {
		if len(doc.Content) == 0 {
			return nil, nil
		}
		return doc.Content[0], nil
	}
	return &doc, nil
}

// mergeValueNodes applies the union rule to two value nodes that share a key.
func mergeValueNodes(win, lose *yaml.Node, key string) (*yaml.Node, error) {
	switch {
	case win.Kind == yaml.SequenceNode && lose.Kind == yaml.SequenceNode:
		return unionSequences(win, lose), nil
	case win.Kind == yaml.ScalarNode && lose.Kind == yaml.ScalarNode:
		if win.Value == lose.Value {
			return win, nil
		}
		return nil, &ConflictError{
			Key:  key,
			Kind: ConflictScalar,
			Why:  fmt.Sprintf("%q vs %q", win.Value, lose.Value),
		}
	case win.Kind == yaml.MappingNode && lose.Kind == yaml.MappingNode:
		// Recursive map-union keeps future compatibility if we introduce
		// nested properties later. Scalar values inside nested maps still
		// conflict the same way.
		return mergeMappingNodes(win, lose, key)
	default:
		return nil, &ConflictError{
			Key:  key,
			Kind: ConflictType,
			Why:  fmt.Sprintf("%s vs %s", kindName(win.Kind), kindName(lose.Kind)),
		}
	}
}

// mergeMappingNodes is the recursive case. Same rules as MergeUnion but
// on a subtree rather than a whole document.
func mergeMappingNodes(win, lose *yaml.Node, parentKey string) (*yaml.Node, error) {
	out := *win
	out.Content = append([]*yaml.Node(nil), win.Content...)
	idxByKey := make(map[string]int, len(out.Content)/2)
	for i := 0; i < len(out.Content); i += 2 {
		idxByKey[out.Content[i].Value] = i
	}
	for i := 0; i < len(lose.Content); i += 2 {
		keyNode := lose.Content[i]
		loserVal := lose.Content[i+1]
		childKey := parentKey + "." + keyNode.Value
		if idx, ok := idxByKey[keyNode.Value]; !ok {
			out.Content = append(out.Content, keyNode, loserVal)
		} else {
			merged, err := mergeValueNodes(out.Content[idx+1], loserVal, childKey)
			if err != nil {
				return nil, err
			}
			out.Content[idx+1] = merged
		}
	}
	return &out, nil
}

// unionSequences returns a sequence containing winner's scalar elements
// (in order) followed by any scalar loser elements not already present.
// Non-scalar sequence elements are kept from winner as-is; new non-scalar
// loser elements are appended (order-preserving), since we don't have a
// canonical equality for them.
func unionSequences(win, lose *yaml.Node) *yaml.Node {
	out := *win
	out.Content = append([]*yaml.Node(nil), win.Content...)
	seen := make(map[string]bool, len(out.Content))
	for _, item := range out.Content {
		if item.Kind == yaml.ScalarNode {
			seen[item.Value] = true
		}
	}
	for _, item := range lose.Content {
		if item.Kind == yaml.ScalarNode {
			if seen[item.Value] {
				continue
			}
			seen[item.Value] = true
		}
		out.Content = append(out.Content, item)
	}
	return &out
}

// reemit writes a mapping node back to YAML bytes. Indent is two spaces
// (conventional and matches Obsidian's output).
func reemit(mapping *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(mapping); err != nil {
		_ = enc.Close()
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func kindName(k yaml.Kind) string {
	switch k {
	case yaml.DocumentNode:
		return "document"
	case yaml.SequenceNode:
		return "sequence"
	case yaml.MappingNode:
		return "mapping"
	case yaml.ScalarNode:
		return "scalar"
	case yaml.AliasNode:
		return "alias"
	default:
		return "unknown"
	}
}
