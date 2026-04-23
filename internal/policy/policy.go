// Package policy maps classifier buckets to the action obconverge will
// propose in plan.md.
//
// Policy is declarative data — a YAML file the operator edits. This package
// is the loader and validator; the plan phase is the consumer. Separating
// the two is the "policy vs mechanism" decomplection: the classifier
// computes *what* a pair is; policy says *what to do* about each kind.
package policy

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/mattjoyce/obconverge/internal/classify"
)

// Action names what obconverge proposes for a given bucket.
type Action string

const (
	// ActionDrop moves one file to .obconverge/trash/ during apply.
	ActionDrop Action = "drop"
	// ActionMergeFrontmatter union-merges frontmatter keys and drops the
	// losing copy.
	ActionMergeFrontmatter Action = "merge-frontmatter"
	// ActionReview flags a pair for human review; apply will not touch it.
	ActionReview Action = "review"
	// ActionQuarantine is SECRETS-specific: apply opens both files in
	// Obsidian and does not modify them.
	ActionQuarantine Action = "quarantine"
	// ActionKeep is a no-op; the file is recognized and left alone.
	ActionKeep Action = "keep"
)

// SecretResponse controls what apply does when it encounters a SECRETS-
// bucket file whose action has been approved in the plan.
//
//   - SecretBlock   — refuse the action; journal records a refusal with
//     reason secrets_bucket. This is the safe default.
//   - SecretWarn    — proceed with the action but log a warning to
//     stderr; the journal's SecretPattern field is
//     still stamped so the decision is auditable.
//   - SecretSilent  — proceed without logging. The journal still
//     records SecretPattern for audit; only the
//     operator-facing noise is suppressed.
//
// The classifier bucket assignment and plan rendering are unaffected —
// SECRETS always wins at classify time, plan always shows the pattern.
// Only the response at apply time changes.
type SecretResponse string

const (
	SecretBlock  SecretResponse = "block"
	SecretWarn   SecretResponse = "warn"
	SecretSilent SecretResponse = "silent"
)

// Policy maps each classifier bucket to the action obconverge will
// propose, plus global apply-time knobs.
type Policy struct {
	Rules          map[classify.Bucket]Action
	SecretResponse SecretResponse
}

// Default returns conservative defaults: destructive actions only for the
// three "proven-safe" buckets (EXACT, CRLF-ONLY, WHITESPACE-ONLY), merge
// for FRONTMATTER-ONLY and TAG-DELTA, review for anything ambiguous,
// quarantine for SECRETS, keep for UNIQUE. Secret response defaults to
// Block.
func Default() Policy {
	return Policy{
		Rules: map[classify.Bucket]Action{
			classify.BucketExact:            ActionDrop,
			classify.BucketCRLFOnly:         ActionDrop,
			classify.BucketTagDelta:         ActionMergeFrontmatter,
			classify.BucketFrontmatterOnly:  ActionMergeFrontmatter,
			classify.BucketFrontmatterEqual: ActionReview,
			classify.BucketAppendOnly:       ActionReview,
			classify.BucketDiverged:         ActionReview,
			classify.BucketSecrets:          ActionQuarantine,
			classify.BucketUnique:           ActionKeep,
		},
		SecretResponse: SecretBlock,
	}
}

// Load reads a policy YAML file at path and merges its rules over the
// defaults. If the file does not exist, returns Default(). Unknown bucket
// names or unknown action names are a hard error — better to refuse to run
// than to silently do the wrong thing with a vault.
func Load(path string) (Policy, error) {
	p := Default()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return p, fmt.Errorf("policy: read %s: %w", path, err)
	}
	var file struct {
		Rules          map[string]string `yaml:"rules"`
		SecretResponse string            `yaml:"secret_response"`
	}
	if err := yaml.Unmarshal(data, &file); err != nil {
		return p, fmt.Errorf("policy: parse %s: %w", path, err)
	}
	for bucketStr, actionStr := range file.Rules {
		b, err := parseBucket(bucketStr)
		if err != nil {
			return p, err
		}
		a, err := parseAction(actionStr)
		if err != nil {
			return p, err
		}
		p.Rules[b] = a
	}
	if file.SecretResponse != "" {
		sr, err := parseSecretResponse(file.SecretResponse)
		if err != nil {
			return p, err
		}
		p.SecretResponse = sr
	}
	return p, nil
}

// ParseSecretResponse validates and returns a SecretResponse.
// Exposed so the CLI can validate the --secrets flag override.
func ParseSecretResponse(s string) (SecretResponse, error) {
	return parseSecretResponse(s)
}

func parseSecretResponse(s string) (SecretResponse, error) {
	switch SecretResponse(s) {
	case SecretBlock, SecretWarn, SecretSilent:
		return SecretResponse(s), nil
	}
	return "", fmt.Errorf("policy: unknown secret_response %q (want block|warn|silent)", s)
}

// ActionFor returns the action for the given bucket, falling back to the
// default if the bucket is not in the policy map.
func (p Policy) ActionFor(b classify.Bucket) Action {
	if a, ok := p.Rules[b]; ok {
		return a
	}
	if a, ok := Default().Rules[b]; ok {
		return a
	}
	// An unknown bucket that the classifier somehow produced should land in
	// the most-conservative action.
	return ActionReview
}

// validBuckets accepts all buckets the classifier *may* produce, including
// ones not yet implemented (TAG-DELTA, APPEND-ONLY, WHITESPACE-ONLY) so
// that forward-looking policy files validate cleanly.
var validBuckets = []classify.Bucket{
	classify.BucketExact,
	classify.BucketCRLFOnly,
	classify.BucketFrontmatterOnly,
	classify.BucketFrontmatterEqual,
	classify.BucketDiverged,
	classify.BucketSecrets,
	classify.BucketUnique,
	// Not yet emitted by classify — accepted for forward compatibility.
	"WHITESPACE-ONLY",
	"TAG-DELTA",
	"APPEND-ONLY",
}

var validActions = []Action{
	ActionDrop,
	ActionMergeFrontmatter,
	ActionReview,
	ActionQuarantine,
	ActionKeep,
}

func parseBucket(s string) (classify.Bucket, error) {
	for _, k := range validBuckets {
		if string(k) == s {
			return k, nil
		}
	}
	return "", fmt.Errorf("policy: unknown bucket %q", s)
}

func parseAction(s string) (Action, error) {
	for _, k := range validActions {
		if string(k) == s {
			return k, nil
		}
	}
	return "", fmt.Errorf("policy: unknown action %q", s)
}
