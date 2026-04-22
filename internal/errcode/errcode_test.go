package errcode

import (
	"errors"
	"fmt"
	"testing"
)

func TestCodeFor(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 0},
		{"usage", ErrUsage, 1},
		{"usage wrapped", fmt.Errorf("bad flags: %w", ErrUsage), 1},
		{"validation", ErrValidation, 2},
		{"validation wrapped", fmt.Errorf("bad config: %w", ErrValidation), 2},
		{"plan required", ErrPlanRequired, 3},
		{"hash drift", ErrHashDrift, 4},
		{"hash drift wrapped", fmt.Errorf("apply refused: %w", ErrHashDrift), 4},
		{"refused", ErrRefused, 5},
		{"refused wrapped", fmt.Errorf("cannot move linked note: %w", ErrRefused), 5},
		{"unknown", errors.New("something else"), 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := CodeFor(tc.err); got != tc.want {
				t.Errorf("CodeFor(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

func TestSentinelsAreDistinct(t *testing.T) {
	sentinels := []error{ErrUsage, ErrValidation, ErrPlanRequired, ErrHashDrift, ErrRefused}
	for i := 0; i < len(sentinels); i++ {
		for j := i + 1; j < len(sentinels); j++ {
			if errors.Is(sentinels[i], sentinels[j]) {
				t.Errorf("sentinels %d and %d are not distinct", i, j)
			}
		}
	}
}
