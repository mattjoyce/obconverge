// Package plan reads classification.jsonl plus a policy and emits plan.md.
//
// The plan is a markdown note: a human opens it in Obsidian, reads it, ticks
// the checkboxes they approve, and saves it. apply will only act on checked
// items. Unchecked items persist across re-runs — if plan.md already exists,
// each action's checkbox state is preserved for any action-ID that still
// appears in the new plan.
package plan

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mattjoyce/obconverge/internal/artifact"
	"github.com/mattjoyce/obconverge/internal/classify"
	"github.com/mattjoyce/obconverge/internal/policy"
)

// Schema appears in the plan.md's YAML frontmatter.
const Schema = "plan/1"

// Item is one proposed action in a plan.
type Item struct {
	// ID is the first 12 hex chars of sha256(bucket, type, sorted paths).
	// Stable across runs on the same classification.
	ID string
	// Bucket is the classifier verdict this item was derived from.
	Bucket classify.Bucket
	// Action is what policy says to do about this bucket.
	Action policy.Action
	// Type is "pair" or "unique", carried through from classify.
	Type string
	// Paths populated for pair items.
	Paths []string
	// Path populated for unique items.
	Path string
	// SecretPattern is set only for SECRETS items; never contains the
	// secret itself, only a pattern name.
	SecretPattern string
	// ReferrerCount is the number of incoming wikilinks / embeds to this
	// item's basename. Surfaced in the plan so the operator sees whether
	// a candidate is orphaned (0) or architectural (many).
	ReferrerCount int
	// Checked is the operator-approval state. Preserved across runs via
	// plan.md merge.
	Checked bool
}

// Options configures a plan run.
type Options struct {
	ClassificationPath string
	PolicyPath         string
	OutputPath         string
	// Now lets tests inject a deterministic timestamp for golden-file
	// comparison; production callers leave it zero.
	Now time.Time
}

// Run generates plan.md. If the file at OutputPath exists, its checkbox
// state is merged into the new plan.
func Run(opts Options) error {
	if opts.ClassificationPath == "" {
		return fmt.Errorf("plan: ClassificationPath is required")
	}
	if opts.OutputPath == "" {
		return fmt.Errorf("plan: OutputPath is required")
	}

	pol, err := policy.Load(opts.PolicyPath)
	if err != nil {
		return err
	}

	records, err := readClassification(opts.ClassificationPath)
	if err != nil {
		return err
	}

	items := buildItems(records, pol)
	sortItems(items)

	prior := readPriorStates(opts.OutputPath)
	for i := range items {
		if checked, ok := prior[items[i].ID]; ok {
			items[i].Checked = checked
		}
	}

	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return render(opts.OutputPath, items, now)
}

func readClassification(path string) ([]classify.Record, error) {
	r, err := artifact.NewReader(path, classify.Schema)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()

	var out []classify.Record
	for {
		var rec classify.Record
		err := r.Next(&rec)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("plan: read classification: %w", err)
		}
		out = append(out, rec)
	}
	return out, nil
}

func buildItems(records []classify.Record, pol policy.Policy) []Item {
	items := make([]Item, 0, len(records))
	for _, rec := range records {
		item := Item{
			Bucket:        rec.Bucket,
			Action:        pol.ActionFor(rec.Bucket),
			Type:          rec.Type,
			Paths:         append([]string(nil), rec.Paths...),
			Path:          rec.Path,
			SecretPattern: rec.SecretPattern,
			ReferrerCount: rec.ReferrerCount,
		}
		item.ID = itemID(item)
		items = append(items, item)
	}
	return items
}

// itemID is a stable 12-hex fingerprint of (bucket, type, sorted paths).
// Same classification → same IDs → checkbox state survives re-runs.
func itemID(it Item) string {
	return ItemIDFor(string(it.Bucket), it.Type, it.Paths, it.Path)
}

// ItemIDFor computes the stable action ID from the raw classification
// fields. Exposed so apply can rebuild an ID from a classify.Record
// without needing to construct a plan.Item.
func ItemIDFor(bucket, kind string, paths []string, path string) string {
	h := sha256.New()
	h.Write([]byte(bucket))
	h.Write([]byte{0})
	h.Write([]byte(kind))
	h.Write([]byte{0})
	all := append([]string{}, paths...)
	if path != "" {
		all = append(all, path)
	}
	sort.Strings(all)
	for _, p := range all {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum)[:12]
}

// bucketOrder renders SECRETS first (so the operator sees them up top) and
// UNIQUE last.
var bucketOrder = map[classify.Bucket]int{
	classify.BucketSecrets:          0,
	classify.BucketExact:            1,
	classify.BucketCRLFOnly:         2,
	classify.BucketWhitespaceOnly:   3,
	classify.BucketTagDelta:         4,
	classify.BucketFrontmatterOnly:  5,
	classify.BucketFrontmatterEqual: 6,
	classify.BucketAppendOnly:       7,
	classify.BucketDiverged:         8,
	classify.BucketUnique:           9,
}

func sortItems(items []Item) {
	sort.SliceStable(items, func(i, j int) bool {
		bi, okI := bucketOrder[items[i].Bucket]
		bj, okJ := bucketOrder[items[j].Bucket]
		if !okI {
			bi = 99
		}
		if !okJ {
			bj = 99
		}
		if bi != bj {
			return bi < bj
		}
		pi := primaryPath(items[i])
		pj := primaryPath(items[j])
		return pi < pj
	})
}

func primaryPath(it Item) string {
	if it.Path != "" {
		return it.Path
	}
	if len(it.Paths) > 0 {
		return it.Paths[0]
	}
	return ""
}

// actionLineRe matches rendered action lines so we can parse the prior
// plan's checkbox state.
var actionLineRe = regexp.MustCompile("^- \\[([ x])\\] #\\S+ `([a-f0-9]{6,})`")

func readPriorStates(path string) map[string]bool {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	out := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		m := actionLineRe.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		out[m[2]] = m[1] == "x"
	}
	return out
}

func render(path string, items []Item, now time.Time) error {
	var buf strings.Builder

	// Frontmatter.
	buf.WriteString("---\n")
	buf.WriteString("tool: obconverge\n")
	fmt.Fprintf(&buf, "schema: %s\n", Schema)
	fmt.Fprintf(&buf, "created: %s\n", now.Format(time.RFC3339))
	buf.WriteString("---\n\n")

	// Title and counts.
	fmt.Fprintf(&buf, "# obconverge plan — %s\n\n", now.Format("2006-01-02 15:04 MST"))

	auto, review := countByDisposition(items)
	fmt.Fprintf(&buf, "**Total actions**: %d · **Auto**: %d · **Review**: %d\n", len(items), auto, review)

	// Actions grouped by bucket.
	if len(items) == 0 {
		buf.WriteString("\n_No actions — vault is converged._\n")
	} else {
		var prev classify.Bucket
		for i, it := range items {
			if i == 0 || it.Bucket != prev {
				prev = it.Bucket
				fmt.Fprintf(&buf, "\n## %s — `#%s`\n\n", it.Bucket, it.Action)
			}
			renderItem(&buf, it)
		}
	}

	buf.WriteString("\n---\n\n## Legend\n\n")
	buf.WriteString("- `#drop` — file moves to `.obconverge/trash/` on apply (soft delete).\n")
	buf.WriteString("- `#merge-frontmatter` — frontmatter is union-merged; the losing copy is dropped.\n")
	buf.WriteString("- `#review` — human review required; apply will not touch.\n")
	buf.WriteString("- `#quarantine` — SECRETS bucket; apply opens both files in Obsidian and does not modify.\n")
	buf.WriteString("- `#keep` — no action needed.\n")

	return os.WriteFile(path, []byte(buf.String()), 0o644)
}

func countByDisposition(items []Item) (auto, review int) {
	for _, it := range items {
		switch it.Action {
		case policy.ActionReview, policy.ActionQuarantine, policy.ActionKeep:
			review++
		default:
			auto++
		}
	}
	return
}

func renderItem(buf *strings.Builder, it Item) {
	check := " "
	if it.Checked {
		check = "x"
	}
	fmt.Fprintf(buf, "- [%s] #%s `%s` — ", check, it.Action, it.ID)
	switch {
	case it.Type == "unique":
		fmt.Fprintf(buf, "`%s`\n", it.Path)
	case len(it.Paths) == 2:
		fmt.Fprintf(buf, "`%s` ↔ `%s`\n", it.Paths[0], it.Paths[1])
	default:
		fmt.Fprintf(buf, "%q\n", it.Paths)
	}
	fmt.Fprintf(buf, "      %s\n", describe(it))
}

func describe(it Item) string {
	base := baseDescription(it)
	tail := referrerSuffix(it.ReferrerCount)
	if tail == "" {
		return base
	}
	return base + " " + tail
}

func baseDescription(it Item) string {
	switch it.Bucket {
	case classify.BucketSecrets:
		if it.SecretPattern != "" {
			return fmt.Sprintf("SECRETS (%s): do NOT open this file's contents in the terminal.", it.SecretPattern)
		}
		return "SECRETS: do NOT open this file's contents in the terminal."
	case classify.BucketExact:
		return "Byte-identical duplicates."
	case classify.BucketCRLFOnly:
		return "Differ only in line endings (CRLF vs LF)."
	case classify.BucketWhitespaceOnly:
		return "Differ only in trailing whitespace or blank-line padding."
	case classify.BucketTagDelta:
		return "Bodies identical; frontmatter differs only in the tags list."
	case classify.BucketFrontmatterOnly:
		return "Bodies identical; frontmatter differs."
	case classify.BucketFrontmatterEqual:
		return "Frontmatter identical; bodies differ."
	case classify.BucketAppendOnly:
		return "One side is a byte-prefix of the other (only one has been appended to)."
	case classify.BucketDiverged:
		return "Bodies differ non-trivially."
	case classify.BucketUnique:
		return "Single occurrence in the vault."
	default:
		return string(it.Bucket)
	}
}

// referrerSuffix renders a short clause to tell the operator how many
// notes point at this item's basename. SECRETS skips the suffix: we
// don't want to imply a SECRETS file is "safe to drop because no
// referrers" — secrets are quarantined regardless.
func referrerSuffix(count int) string {
	switch count {
	case 0:
		return "Referrers: 0 (no incoming links)."
	case 1:
		return "Referrers: 1 incoming link."
	default:
		return fmt.Sprintf("Referrers: %d incoming links.", count)
	}
}
