// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package cmd

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sebastienrousseau/askiso/internal/cbprworkspace"
	"github.com/sebastienrousseau/askiso/internal/converter"
	"github.com/sebastienrousseau/askiso/internal/rules"
	"github.com/sebastienrousseau/askiso/pkg/iso20022"
	"github.com/spf13/cobra"
)

var cbprPackOutput string

var cbprPackCmd = &cobra.Command{
	Use:   "cbpr-pack",
	Short: "Compile locally held CBPR+ Usage Guideline PDFs",
	Long: `CBPR Pack compiles authorised, locally held Usage Guideline PDFs into a
content-minimised rule pack. Source PDFs are read only by the local pdftotext
process; AskISO does not upload, copy, cache, or embed them.

The PDF compiler recognises message identifiers, Usage Identifier variants,
element hierarchy, cardinality, and supported lexical types. It reports this as
PDF-derived coverage: narrative, conditional, external-code-set, and
diagram-only rules are not silently claimed as checked.`,
}

var cbprPackCompileCmd = &cobra.Command{
	Use:   "compile <pdf-or-directory>",
	Short: "Compile PDFs into a reusable local rule pack",
	Args:  cobra.ExactArgs(1),
	Example: `  askiso cbpr-pack compile /secure/CBPR-SR2025 -o ~/.local/share/cbpr-sr2025.cbpr-pack.json
  askiso lint payment.xml --cbpr-pack ~/.local/share/cbpr-sr2025.cbpr-pack.json
  askiso batch ./messages --cbpr-pack /secure/CBPR-SR2025`,
	RunE: func(cmd *cobra.Command, args []string) error {
		pack, err := rules.CompileCBPRPack(args[0])
		if err != nil {
			return err
		}
		if cbprPackOutput != "" {
			output := filepath.Clean(cbprPackOutput)
			if !strings.HasSuffix(strings.ToLower(output), ".cbpr-pack.json") {
				return errorsForPackOutput(output)
			}
			if err := rules.WriteCBPRPack(output, pack); err != nil {
				return err
			}
			info := pack.Info()
			fmt.Printf("compiled %d constraint(s) from %d local PDF(s) into %s\n", info.Constraints, info.Sources, output)
			for _, warning := range info.Warnings {
				fmt.Printf("warning: %s\n", warning)
			}
			return nil
		}
		data, err := json.MarshalIndent(pack, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), string(data))
		return err
	},
}

var cbprPackCompileOverlayCmd = &cobra.Command{
	Use:   "compile-overlay <overlay.json>",
	Short: "Compile explicitly authored conditional and narrative rules",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		pack, err := rules.CompileCBPROverlay(args[0])
		if err != nil {
			return err
		}
		if cbprPackOutput != "" {
			output := filepath.Clean(cbprPackOutput)
			if !strings.HasSuffix(strings.ToLower(output), ".cbpr-pack.json") {
				return errorsForPackOutput(output)
			}
			if err := rules.WriteCBPRPack(output, pack); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "compiled %d operator-authored constraint(s) into %s\n", len(pack.Constraints), output)
			return nil
		}
		data, err := json.MarshalIndent(pack, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), string(data))
		return err
	},
}

func errorsForPackOutput(path string) error {
	return fmt.Errorf("compiled pack output must end in .cbpr-pack.json: %s", path)
}

func resolveRuleProfile(profileName, packPath string) (rules.Profile, error) {
	profile, _, err := resolveRuleProfileWithWorkspace(profileName, packPath, "")
	return profile, err
}

func resolveRuleProfileWithWorkspace(profileName, packPath, workspacePath string) (rules.Profile, *cbprworkspace.Runtime, error) {
	if packPath != "" && workspacePath != "" {
		return rules.Profile{}, nil, fmt.Errorf("--cbpr-pack and --cbpr-workspace cannot be used together")
	}
	if workspacePath != "" {
		runtime, err := cbprworkspace.LoadRuntime(workspacePath)
		if err != nil {
			return rules.Profile{}, nil, fmt.Errorf("loading --cbpr-workspace: %w", err)
		}
		if strings.TrimSpace(profileName) == "" {
			profileName = "cbpr-plus"
		}
		profile, err := rules.Get(profileName)
		if err != nil {
			return rules.Profile{}, nil, err
		}
		if profile.Name != "cbpr-plus" {
			return rules.Profile{}, nil, fmt.Errorf("--cbpr-workspace requires --profile cbpr-plus, not %q", profile.Name)
		}
		if runtime.Pack != nil {
			profile, err = runtime.Pack.Augment(profile)
			if err != nil {
				return rules.Profile{}, nil, err
			}
		}
		return profile, runtime, nil
	}
	if packPath != "" && strings.TrimSpace(profileName) == "" {
		profileName = "cbpr-plus"
	}
	profile, err := rules.Get(profileName)
	if err != nil {
		return rules.Profile{}, nil, err
	}
	if packPath == "" {
		return profile, nil, nil
	}
	pack, err := rules.CompileCBPRPack(packPath)
	if err != nil {
		return rules.Profile{}, nil, fmt.Errorf("compiling --cbpr-pack: %w", err)
	}
	profile, err = pack.Augment(profile)
	return profile, nil, err
}

func runResolvedProfile(data []byte, filePath string, profile rules.Profile) (*rules.Result, error) {
	root, err := converter.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", filepath.Base(filePath), err)
	}
	msgID, _ := iso20022.MessageIDFromInstance(data)
	return rules.Run(profile, root, msgID, filePath), nil
}

func init() {
	cbprPackCompileCmd.Flags().StringVarP(&cbprPackOutput, "output", "o", "",
		"Write a private compiled pack (must end in .cbpr-pack.json); default: stdout")
	cbprPackCompileOverlayCmd.Flags().StringVarP(&cbprPackOutput, "output", "o", "",
		"Write a private compiled pack (must end in .cbpr-pack.json); default: stdout")
	cbprPackCmd.AddCommand(cbprPackCompileCmd, cbprPackCompileOverlayCmd)
	RootCmd.AddCommand(cbprPackCmd)
}
