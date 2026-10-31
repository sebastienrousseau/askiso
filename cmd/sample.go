// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/atotto/clipboard"
	"github.com/sebastienrousseau/askiso/internal/catalog"
	"github.com/sebastienrousseau/askiso/internal/generator"
	"github.com/spf13/cobra"
)

var (
	sampleCopy bool
	sampleRaw  bool
)

var sampleCmd = &cobra.Command{
	Use:   "sample <message-id>",
	Short: "Display syntax-highlighted compliant XML sample message",
	Long: `Sample locates and outputs the 100% schema-compliant XML sample instance 
for any ISO 20022 message with terminal syntax highlighting and clipboard export.`,
	Example: `  askiso sample pacs.008.001.10
  askiso sample pain.001 --copy
  askiso sample camt.053 --raw`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := args[0]

		msg, idx, err := resolveMessage(query, "read its published sample")
		if err != nil {
			// The Registration Authority publishes sample instances for only a
			// handful of messages, so a generated one is often the best answer
			// available -- and it needs no catalogue at all.
			if generated, genErr := generator.Generate(generator.DefaultOptions(query)); genErr == nil {
				fmt.Print(lightModeNotice("generated sample"))
				fmt.Printf("\n%s\n", generated)
				return nil
			}
			return err
		}

		targetPath := msg.XMLSamplePath
		if targetPath == "" {
			for _, m := range idx.Search(query) {
				if m.XMLSamplePath != "" {
					targetPath = m.XMLSamplePath
					break
				}
			}
		}
		if targetPath == "" {
			if generated, genErr := generator.Generate(generator.DefaultOptions(query)); genErr == nil {
				fmt.Printf("\n%s no published sample for %s; showing a generated one.\n\n",
					subtleStyle.Render("note:"), query)
				fmt.Printf("%s\n", generated)
				return nil
			}
			return notInstalled(query, "read its published sample", nil)
		}
		if err := catalog.CheckEvicted(targetPath); err != nil {
			return err
		}

		data, err := os.ReadFile(filepath.Clean(targetPath))
		if err != nil {
			return fmt.Errorf("failed to read sample file: %w", err)
		}

		content := string(data)

		if sampleCopy {
			if err := clipboard.WriteAll(content); err == nil {
				fmt.Printf("\n%s Copied XML sample for %s to clipboard!\n\n", badgeStyle.Render(" COPIED "), query)
			}
		}

		if sampleRaw || os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
			if !sampleCopy {
				fmt.Println(content)
			}
			return nil
		}

		highlighted := highlightXML(content)
		if !sampleCopy {
			fmt.Println(highlighted)
		}
		return nil
	},
}

func highlightXML(source string) string {
	lexer := lexers.Get("xml")
	if lexer == nil {
		lexer = lexers.Fallback
	}
	style := styles.Get("monokai")
	if style == nil {
		style = styles.Fallback
	}
	formatter := formatters.Get("terminal256")
	if formatter == nil {
		formatter = formatters.Fallback
	}

	iterator, err := lexer.Tokenise(nil, source)
	if err != nil {
		return source
	}

	var buf bytes.Buffer
	if err := formatter.Format(&buf, style, iterator); err != nil {
		return source
	}

	return buf.String()
}

func init() {
	sampleCmd.Flags().BoolVarP(&sampleCopy, "copy", "y", false, "Copy XML sample payload to system clipboard")
	sampleCmd.Flags().BoolVarP(&sampleRaw, "raw", "r", false, "Output raw uncolored XML text")
	RootCmd.AddCommand(sampleCmd)
}
