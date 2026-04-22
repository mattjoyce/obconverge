// Command obconverge is a vault reconciliation CLI for Obsidian.
//
// Pipeline: scan → classify → plan → apply. This binary currently wires
// scan and classify; plan and apply land in subsequent commits.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/mattjoyce/obconverge/internal/classify"
	"github.com/mattjoyce/obconverge/internal/config"
	"github.com/mattjoyce/obconverge/internal/errcode"
	"github.com/mattjoyce/obconverge/internal/logging"
	"github.com/mattjoyce/obconverge/internal/scan"
)

// version is stamped via -ldflags at release time; "dev" in local builds.
var version = "dev"

// ctxKey is unexported so no other package can read our context values.
type ctxKey string

const cfgKey ctxKey = "obconverge-config"

func main() {
	if err := newRoot().Execute(); err != nil {
		// Cobra already prints the error to stderr; we only need to pick the
		// correct exit code.
		os.Exit(errcode.CodeFor(err))
	}
}

// newRoot constructs the root cobra command. Exposed for tests.
func newRoot() *cobra.Command {
	var (
		configFlag string
		logLevel   string
		logFormat  string
		showVer    bool
	)

	root := &cobra.Command{
		Use:           "obconverge",
		Short:         "Vault reconciliation CLI for Obsidian",
		Long:          "obconverge — walk, classify, plan, apply against an Obsidian vault. See SPEC.md.",
		SilenceUsage:  true,
		SilenceErrors: false,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			userPath, err := config.DefaultUserConfigPath()
			if err != nil {
				return fmt.Errorf("%w: %v", errcode.ErrValidation, err)
			}
			cfg, err := config.Assemble(userPath, configFlag)
			if err != nil {
				return fmt.Errorf("%w: %v", errcode.ErrValidation, err)
			}

			// CLI flag overrides for log settings.
			level := cfg.LogLevel
			if logLevel != "" {
				level = logLevel
			}
			format := cfg.LogFormat
			if logFormat != "" {
				format = logFormat
			}
			slog.SetDefault(logging.New(logging.Options{Level: level, Format: format}))

			// Make config available to subcommands via context.
			cmd.SetContext(context.WithValue(cmd.Context(), cfgKey, cfg))
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if showVer {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), version)
				return nil
			}
			return cmd.Help()
		},
	}

	root.PersistentFlags().StringVarP(&configFlag, "config", "c", "", "Path to an override config file")
	root.PersistentFlags().StringVar(&logLevel, "log-level", "", "Log level: debug|info|warn|error (overrides config)")
	root.PersistentFlags().StringVar(&logFormat, "log-format", "", "Log format: text|json (overrides config)")
	root.Flags().BoolVar(&showVer, "version", false, "Print version and exit")

	root.AddCommand(newScanCmd())
	root.AddCommand(newClassifyCmd())
	return root
}

func newScanCmd() *cobra.Command {
	var vaultFlag, outputFlag string
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Walk the vault and emit index.jsonl",
		Long:  "scan walks the vault (respecting protected paths) and writes an index.jsonl artifact with hashes and metadata for each regular file.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := cfgFromCtx(cmd.Context())
			root, err := resolveVault(vaultFlag, cfg.VaultPath)
			if err != nil {
				return err
			}
			out, err := resolveOutput(root, outputFlag, cfg.WorkDir, "index.jsonl")
			if err != nil {
				return err
			}
			slog.Debug("scan starting", "vault", root, "output", out)
			if err := scan.Run(scan.Options{VaultRoot: root, OutputPath: out}); err != nil {
				return err
			}
			slog.Info("scan complete", "output", out)
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), out)
			return nil
		},
	}
	cmd.Flags().StringVar(&vaultFlag, "vault", "", "Path to Obsidian vault (overrides config)")
	cmd.Flags().StringVarP(&outputFlag, "output", "o", "", "Output path for index.jsonl (default: <vault>/<work_dir>/index.jsonl)")
	return cmd
}

func newClassifyCmd() *cobra.Command {
	var vaultFlag, indexFlag, outputFlag string
	cmd := &cobra.Command{
		Use:   "classify",
		Short: "Read index.jsonl and emit classification.jsonl",
		Long:  "classify groups index entries by basename and emits one classification record per pair (or a unique record for singletons).",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := cfgFromCtx(cmd.Context())
			root, err := resolveVault(vaultFlag, cfg.VaultPath)
			if err != nil {
				return err
			}
			in := indexFlag
			if in == "" {
				in = filepath.Join(root, cfg.WorkDir, "index.jsonl")
			}
			out, err := resolveOutput(root, outputFlag, cfg.WorkDir, "classification.jsonl")
			if err != nil {
				return err
			}
			slog.Debug("classify starting", "index", in, "output", out)
			if err := classify.Run(classify.Options{IndexPath: in, ClassificationPath: out}); err != nil {
				return err
			}
			slog.Info("classify complete", "output", out)
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), out)
			return nil
		},
	}
	cmd.Flags().StringVar(&vaultFlag, "vault", "", "Path to Obsidian vault")
	cmd.Flags().StringVar(&indexFlag, "index", "", "Path to index.jsonl (default: <vault>/<work_dir>/index.jsonl)")
	cmd.Flags().StringVarP(&outputFlag, "output", "o", "", "Output path for classification.jsonl")
	return cmd
}

// cfgFromCtx retrieves the config that PersistentPreRunE stashed.
// If it's missing (should never happen in production), returns defaults.
func cfgFromCtx(ctx context.Context) config.Config {
	if v, ok := ctx.Value(cfgKey).(config.Config); ok {
		return v
	}
	return config.Default()
}

// resolveVault picks the vault root: CLI flag wins over config.VaultPath.
// Always expands and absolutizes the result.
func resolveVault(vaultFlag, configPath string) (string, error) {
	chosen := vaultFlag
	if chosen == "" {
		chosen = configPath
	}
	if chosen == "" {
		return "", fmt.Errorf("%w: no vault path — pass --vault or set vault_path in config", errcode.ErrUsage)
	}
	expanded, err := config.ExpandPath(chosen)
	if err != nil {
		return "", fmt.Errorf("%w: expand vault path: %v", errcode.ErrValidation, err)
	}
	info, err := os.Stat(expanded)
	if err != nil {
		return "", fmt.Errorf("%w: vault path %s: %v", errcode.ErrValidation, expanded, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: vault path %s is not a directory", errcode.ErrValidation, expanded)
	}
	return expanded, nil
}

// resolveOutput resolves an artifact path, defaulting under <vault>/<workDir>/.
// Creates the parent directory if missing.
func resolveOutput(vaultRoot, outputFlag, workDir, defaultFile string) (string, error) {
	out := outputFlag
	if out == "" {
		if workDir == "" {
			workDir = ".obconverge"
		}
		out = filepath.Join(vaultRoot, workDir, defaultFile)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", fmt.Errorf("%w: create output dir: %v", errcode.ErrValidation, err)
	}
	return out, nil
}
