// Command obconverge is a vault reconciliation CLI for Obsidian.
//
// Pipeline: scan → classify → plan → apply. This binary currently wires
// scan and classify; plan and apply land in subsequent commits.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/mattjoyce/obconverge/internal/classify"
	"github.com/mattjoyce/obconverge/internal/config"
	"github.com/mattjoyce/obconverge/internal/scan"
)

func main() {
	if err := newRoot().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "obconverge",
		Short:         "Vault reconciliation CLI for Obsidian",
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	root.AddCommand(newScanCmd())
	root.AddCommand(newClassifyCmd())
	return root
}

func newScanCmd() *cobra.Command {
	var vaultFlag, outputFlag, configFlag string
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Walk the vault and emit index.jsonl",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveVault(vaultFlag, configFlag)
			if err != nil {
				return err
			}
			out, err := resolveOutput(root, outputFlag, "index.jsonl")
			if err != nil {
				return err
			}
			if err := scan.Run(scan.Options{VaultRoot: root, OutputPath: out}); err != nil {
				return err
			}
			fmt.Println(out)
			return nil
		},
	}
	cmd.Flags().StringVar(&vaultFlag, "vault", "", "Path to Obsidian vault (overrides config)")
	cmd.Flags().StringVarP(&outputFlag, "output", "o", "", "Output path for index.jsonl (default: <vault>/.obconverge/index.jsonl)")
	cmd.Flags().StringVarP(&configFlag, "config", "c", "", "Path to an override config file")
	return cmd
}

func newClassifyCmd() *cobra.Command {
	var vaultFlag, indexFlag, outputFlag, configFlag string
	cmd := &cobra.Command{
		Use:   "classify",
		Short: "Read index.jsonl and emit classification.jsonl",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveVault(vaultFlag, configFlag)
			if err != nil {
				return err
			}
			in := indexFlag
			if in == "" {
				in = filepath.Join(root, ".obconverge", "index.jsonl")
			}
			out, err := resolveOutput(root, outputFlag, "classification.jsonl")
			if err != nil {
				return err
			}
			if err := classify.Run(classify.Options{IndexPath: in, ClassificationPath: out}); err != nil {
				return err
			}
			fmt.Println(out)
			return nil
		},
	}
	cmd.Flags().StringVar(&vaultFlag, "vault", "", "Path to Obsidian vault")
	cmd.Flags().StringVar(&indexFlag, "index", "", "Path to index.jsonl (default: <vault>/.obconverge/index.jsonl)")
	cmd.Flags().StringVarP(&outputFlag, "output", "o", "", "Output path for classification.jsonl (default: <vault>/.obconverge/classification.jsonl)")
	cmd.Flags().StringVarP(&configFlag, "config", "c", "", "Path to an override config file")
	return cmd
}

// resolveVault figures out the vault root: CLI flag wins, else config, else error.
func resolveVault(vaultFlag, overrideConfigFlag string) (string, error) {
	if vaultFlag != "" {
		return config.ExpandPath(vaultFlag)
	}
	userPath, err := config.DefaultUserConfigPath()
	if err != nil {
		return "", err
	}
	cfg, err := config.Assemble(userPath, overrideConfigFlag)
	if err != nil {
		return "", err
	}
	if cfg.VaultPath == "" {
		return "", fmt.Errorf("no vault path: pass --vault or set vault_path in %s", userPath)
	}
	return config.ExpandPath(cfg.VaultPath)
}

// resolveOutput resolves the output artifact path, creating the parent dir.
func resolveOutput(vaultRoot, outputFlag, defaultFile string) (string, error) {
	out := outputFlag
	if out == "" {
		out = filepath.Join(vaultRoot, ".obconverge", defaultFile)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", fmt.Errorf("resolve output: %w", err)
	}
	return out, nil
}
