// Package logging configures log/slog for obconverge.
//
// The CLI reads log_level and log_format from the assembled config (with CLI
// flag overrides) and installs a single slog handler as the default. Phase
// packages call slog.Info / slog.Debug directly — no logger is threaded
// through options.
package logging

import (
	"io"
	"log/slog"
	"os"
)

// Options configures the handler.
type Options struct {
	// Level is one of "debug", "info", "warn", "error". Unknown values become "info".
	Level string
	// Format is "text" or "json". Unknown values become "text".
	Format string
	// Writer is where records are written. Defaults to os.Stderr.
	Writer io.Writer
}

// New returns a configured logger. It never returns nil and never returns an
// error — invalid options fall back to safe defaults.
func New(opts Options) *slog.Logger {
	w := opts.Writer
	if w == nil {
		w = os.Stderr
	}

	level := parseLevel(opts.Level)
	handlerOpts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	switch opts.Format {
	case "json":
		handler = slog.NewJSONHandler(w, handlerOpts)
	default:
		handler = slog.NewTextHandler(w, handlerOpts)
	}
	return slog.New(handler)
}

func parseLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
