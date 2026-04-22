// Package errcode defines the sentinel errors that map to obconverge's
// CLI exit codes.
//
// Exit codes (from SPEC.md "Skills descriptor" → "Exit codes"):
//
//	0  success
//	1  usage error
//	2  validation error
//	3  plan required (ran apply with no approved plan)
//	4  hash drift since plan
//	5  refused by a safety invariant (linked note move, referenced attachment, etc.)
//
// Callers wrap errors with fmt.Errorf("...: %w", errcode.ErrX); main calls
// CodeFor on the returned error to pick the exit code.
package errcode

import "errors"

// Sentinel errors, one per exit code.
var (
	ErrUsage        = errors.New("usage error")
	ErrValidation   = errors.New("validation error")
	ErrPlanRequired = errors.New("plan required")
	ErrHashDrift    = errors.New("hash drift since plan was written")
	ErrRefused      = errors.New("refused: safety invariant")
)

// CodeFor returns the CLI exit code for err. nil → 0; unrecognized → 1.
func CodeFor(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, ErrUsage):
		return 1
	case errors.Is(err, ErrValidation):
		return 2
	case errors.Is(err, ErrPlanRequired):
		return 3
	case errors.Is(err, ErrHashDrift):
		return 4
	case errors.Is(err, ErrRefused):
		return 5
	default:
		return 1
	}
}
