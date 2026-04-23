package frontmatter

import "bytes"

// StripTags returns the frontmatter YAML with the top-level `tags` key
// removed. Other keys (including nested maps containing keys named
// "tags") are untouched. Used by scan to compute a frontmatter-minus-
// tags hash that lets classify distinguish TAG-DELTA (only tags differ)
// from FRONTMATTER-ONLY (anything else differs).
//
// Empty input or frontmatter-with-no-tags-key returns the input bytes
// unchanged (minus re-emission whitespace differences when the input
// was parseable). A parse error returns the error.
func StripTags(fm []byte) ([]byte, error) {
	if len(bytes.TrimSpace(fm)) == 0 {
		return fm, nil
	}
	mapping, err := parseMapping(fm)
	if err != nil {
		return nil, err
	}
	if mapping == nil || len(mapping.Content) == 0 {
		return fm, nil
	}

	// Rebuild Content without any "tags" key. Content pairs are
	// [key, value, key, value, ...] at the mapping level.
	stripped := *mapping
	stripped.Content = stripped.Content[:0]
	for i := 0; i < len(mapping.Content); i += 2 {
		keyNode := mapping.Content[i]
		if keyNode.Value == "tags" {
			continue
		}
		stripped.Content = append(stripped.Content, keyNode, mapping.Content[i+1])
	}
	if len(stripped.Content) == 0 {
		// Nothing but tags. Return empty bytes so the caller doesn't
		// trip over "---\n---\n" noise.
		return nil, nil
	}
	return reemit(&stripped)
}
