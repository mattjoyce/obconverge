// Package purge removes the trash directory. After a successful purge,
// nothing in the affected apply run is recoverable via undo — this is
// the spec's "reversible until --purge" boundary.
//
// v1 scope: purge removes EVERYTHING under <vault>/<work_dir>/trash/.
// Selective purge by journal timestamp or by action id is a future
// sharpening; the current shape is the simplest thing that honours
// the spec's commitment.
package purge

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
)

// Summary reports what purge saw (and removed, if --execute).
type Summary struct {
	// Files is the number of regular files counted under the trash tree.
	Files int
	// Bytes is the total byte size of those files.
	Bytes int64
	// DryRun is true if Execute was false. The counts reflect what
	// *would* have been removed.
	DryRun bool
	// Removed is true if Execute succeeded (or no files were present).
	Removed bool
}

// Options configures a purge.
type Options struct {
	// VaultRoot is the vault whose trash is being purged. Required.
	VaultRoot string
	// WorkDir defaults to ".obconverge".
	WorkDir string
	// Execute: if false (default), just report what would be removed.
	Execute bool
}

// Run scans the trash directory under VaultRoot/WorkDir, counts files
// and bytes, and — if Execute — removes the directory entirely.
// An absent trash directory is not an error; it's a zero-file result.
func Run(opts Options) (Summary, error) {
	if opts.VaultRoot == "" {
		return Summary{}, fmt.Errorf("purge: VaultRoot is required")
	}
	if opts.WorkDir == "" {
		opts.WorkDir = ".obconverge"
	}
	trashDir := filepath.Join(opts.VaultRoot, opts.WorkDir, "trash")

	summary := Summary{DryRun: !opts.Execute}

	err := filepath.WalkDir(trashDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) {
				return filepath.SkipAll
			}
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			summary.Files++
			summary.Bytes += info.Size()
		}
		return nil
	})
	if err != nil {
		return summary, fmt.Errorf("purge: walk trash: %w", err)
	}

	if !opts.Execute {
		return summary, nil
	}

	// Remove the whole trash tree. If the directory didn't exist,
	// RemoveAll is a no-op.
	if err := os.RemoveAll(trashDir); err != nil {
		return summary, fmt.Errorf("purge: remove trash: %w", err)
	}
	summary.Removed = true
	slog.Info("purge: trash removed", "files", summary.Files, "bytes", summary.Bytes)
	return summary, nil
}
