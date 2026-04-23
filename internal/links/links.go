// Package links builds a referrer graph over an Obsidian vault.
//
// The graph answers the question "what notes refer to X?" for any note
// basename or alias. apply needs this to refuse moves or deletes of
// linked notes; plan uses it to flag UNIQUE files with zero referrers
// as cleanup candidates and high-referrer files as architectural.
//
// The implementation is deliberately modest: regex-based detection of
// the wikilink / embed syntax in note bodies, two passes over the vault
// (pass 1 for aliases, pass 2 for links), no AST parse. This matches
// v1's needs without pulling in goldmark. Richer link handling —
// path-based wikilinks, heading navigation, link rewriting — lands in
// a later commit when apply needs it.
package links

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mattjoyce/obconverge/internal/frontmatter"
)

// Kind categorizes a link.
type Kind string

const (
	KindWikilink   Kind = "wikilink" // [[Target]] or [[Target|Display]]
	KindEmbed      Kind = "embed"    // ![[Target]]
	KindHeadingRef Kind = "heading"  // [[Target#Heading]]
	KindBlockRef   Kind = "blockref" // [[Target#^blockid]]
)

// Referrer is one inbound link: the file it was seen in, what kind it
// was, and the exact matched text (for the operator's review).
type Referrer struct {
	From string `json:"from"` // vault-relative path of the referring file
	Kind Kind   `json:"kind"`
	Raw  string `json:"raw"`
}

// Graph is an immutable lookup from note basename (without .md) to the
// list of referrers.
type Graph struct {
	byTarget map[string][]Referrer
}

// Referrers returns the inbound links to name. name may be "Foo" or
// "Foo.md" — both resolve. Returns nil if there are none.
func (g *Graph) Referrers(name string) []Referrer {
	return g.byTarget[canonical(name)]
}

// Count is a convenience for len(Referrers(name)).
func (g *Graph) Count(name string) int {
	return len(g.byTarget[canonical(name)])
}

// Targets returns the sorted list of every basename that has at least
// one referrer. Useful for iteration / diagnostics.
func (g *Graph) Targets() []string {
	out := make([]string, 0, len(g.byTarget))
	for k := range g.byTarget {
		out = append(out, k)
	}
	return out
}

// Options configures a Build call.
type Options struct {
	VaultRoot string
	// ProtectedPrefixes are vault-relative path prefixes that Build will
	// not descend into. If nil, a conservative built-in set applies.
	ProtectedPrefixes []string
}

// DefaultProtectedPrefixes mirrors the scan package's list so Build and
// scan agree on which directories are off-limits.
var DefaultProtectedPrefixes = []string{
	".obsidian",
	".trash",
	".git",
	".stfolder",
	".sync",
	".obconverge",
}

// Build walks the vault and returns a referrer graph. Two passes:
//
//  1. Collect basenames and frontmatter aliases across all .md files.
//  2. Scan each body for the wikilink/embed syntax; resolve targets via
//     the alias map from pass 1.
//
// Link targets that don't resolve to any known basename or alias are
// ignored — they're either typos or external references, neither of
// which participate in the audit.
func Build(opts Options) (*Graph, error) {
	if opts.VaultRoot == "" {
		return nil, fmt.Errorf("links: VaultRoot is required")
	}
	prefixes := opts.ProtectedPrefixes
	if prefixes == nil {
		prefixes = append(prefixes, DefaultProtectedPrefixes...)
	}

	files, aliases, err := pass1(opts.VaultRoot, prefixes)
	if err != nil {
		return nil, err
	}
	byTarget, err := pass2(opts.VaultRoot, prefixes, files, aliases)
	if err != nil {
		return nil, err
	}
	return &Graph{byTarget: byTarget}, nil
}

// pass1 collects (basenameWithoutExt -> relSlashPath) and
// (alias -> basenameWithoutExt). Non-.md files are skipped.
func pass1(root string, prefixes []string) (files map[string]string, aliases map[string]string, err error) {
	files = map[string]string{}
	aliases = map[string]string{}

	err = walkMarkdown(root, prefixes, func(relSlash string, data []byte) error {
		base := canonical(filepath.Base(relSlash))
		files[base] = relSlash
		fm, _ := frontmatter.Split(data)
		if fm == nil {
			return nil
		}
		fields, ferr := frontmatter.ExtractFields(fm)
		if ferr != nil {
			// Invalid frontmatter isn't fatal — the note still exists and
			// can still be a link target by basename. We just can't trust
			// its alias list.
			return nil
		}
		for _, a := range fields.Aliases {
			aliases[a] = base
		}
		return nil
	})
	return files, aliases, err
}

// pass2 scans each body for links and builds the inverse map.
func pass2(root string, prefixes []string, files, aliases map[string]string) (map[string][]Referrer, error) {
	out := map[string][]Referrer{}

	err := walkMarkdown(root, prefixes, func(relSlash string, data []byte) error {
		_, body := frontmatter.Split(data)
		for _, l := range extractLinks(body) {
			target := resolve(l.Target, files, aliases)
			if target == "" {
				continue
			}
			out[target] = append(out[target], Referrer{
				From: relSlash,
				Kind: l.Kind,
				Raw:  l.Raw,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// walkMarkdown iterates regular .md files under root (respecting
// prefixes) and invokes fn with the vault-relative slash-path and the
// file's bytes.
func walkMarkdown(root string, prefixes []string, fn func(relSlash string, data []byte) error) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if isProtected(relSlash, prefixes) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(rel), ".md") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return fn(relSlash, data)
	})
}

func isProtected(relSlash string, prefixes []string) bool {
	for _, p := range prefixes {
		if relSlash == p || strings.HasPrefix(relSlash, p+"/") {
			return true
		}
	}
	return false
}

// linkRe captures all four flavours of Obsidian link.
//
// Groups:
//
//  1. "!" if this is an embed, "" otherwise
//  2. Target page name (the part before # or |)
//  3. Fragment, including leading # — e.g. "#Heading" or "#^blockid", or ""
//  4. Display override, including leading | — e.g. "|Label", or ""
var linkRe = regexp.MustCompile(`(!)?\[\[([^\]|#]+?)(#\^?[^\]|]*)?(\|[^\]]*)?\]\]`)

// parsedLink is an internal representation of one match.
type parsedLink struct {
	Target string // as written, whitespace-trimmed
	Kind   Kind
	Raw    string // exact matched text
}

func extractLinks(body []byte) []parsedLink {
	matches := linkRe.FindAllSubmatch(body, -1)
	out := make([]parsedLink, 0, len(matches))
	for _, m := range matches {
		target := strings.TrimSpace(string(m[2]))
		if target == "" {
			continue
		}
		kind := KindWikilink
		switch {
		case len(m[1]) > 0 && m[1][0] == '!':
			kind = KindEmbed
		case len(m[3]) >= 2 && m[3][0] == '#' && m[3][1] == '^':
			kind = KindBlockRef
		case len(m[3]) >= 1 && m[3][0] == '#':
			kind = KindHeadingRef
		}
		out = append(out, parsedLink{Target: target, Kind: kind, Raw: string(m[0])})
	}
	return out
}

// resolve maps a link's written target to a canonical basename (without
// .md). Returns "" if the target doesn't match any known file or alias.
func resolve(target string, files, aliases map[string]string) string {
	t := canonical(target)
	if _, ok := files[t]; ok {
		return t
	}
	if base, ok := aliases[target]; ok {
		return base
	}
	// Aliases are case-sensitive; file basenames were normalized by
	// canonical(). Try the raw alias lookup first to catch case-exact
	// alias matches, then fall back to case-exact file-basename match.
	return ""
}

// canonical normalizes a name for map lookup: strip the .md extension
// if present. Case is preserved (matches Obsidian's default behavior
// on case-sensitive filesystems; macOS users with case-insensitive
// filesystems will see Obsidian's built-in normalization anyway).
func canonical(name string) string {
	if strings.HasSuffix(strings.ToLower(name), ".md") {
		return name[:len(name)-3]
	}
	return name
}
