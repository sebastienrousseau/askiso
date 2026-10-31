// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

// Package registrygen builds the embedded message registry from the ISO 20022
// Registration Authority's published message-set index.
//
// The registry holds facts about the standard -- which message identifiers
// exist, which message set publishes each one, and where the RA hosts the
// download. It contains no schema, report, or specification text, so AskISO
// ships it without redistributing ISO 20022 content.
package registrygen

import (
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type manifestEntry struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Category   string   `json:"category"`
	Version    string   `json:"version"`
	URL        string   `json:"url"`
	FilesCount int      `json:"files_count"`
	Files      []string `json:"files"`
	Status     string   `json:"status"`
}

// RA titles carry a trailing version token, sometimes with scraper artefacts
// glued on: "Account Switching V03", "Corporate Actions V15BAHHas variants",
// "... Variants V09BAHIs variant".
var (
	versionSuffix = regexp.MustCompile(`(?i)\s*V\d{2}(?:BAH)?(?:Has variants|Is variant)?\s*$`)
	extraSpace    = regexp.MustCompile(`\s+`)
)

// Run is the gen-registry command: it parses arguments, builds the registry,
// and reports what it did, returning the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gen-registry", flag.ContinueOnError)
	fs.SetOutput(stderr)
	manifest := fs.String("manifest", "manifest.json", "RA message-set index")
	out := fs.String("out", "internal/registry/registry.tsv.gz", "output blob")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	stats, err := Build(*manifest, *out)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "gen-registry:", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "registry: %d sets, %d messages -> %s (%.1f KB compressed)\n",
		stats.Sets, stats.Messages, *out, float64(stats.Bytes)/1024)
	if stats.Skipped > 0 {
		_, _ = fmt.Fprintf(stdout, "skipped %d set(s) with a non-success status\n", stats.Skipped)
	}
	return 0
}

// Stats reports what a build produced.
type Stats struct {
	Sets     int
	Messages int
	Skipped  int
	Bytes    int64
}

// Build reads the RA manifest and writes the compressed registry blob.
func Build(manifestPath, outPath string) (Stats, error) {
	raw, err := os.ReadFile(filepath.Clean(manifestPath))
	if err != nil {
		return Stats{}, fmt.Errorf("reading manifest: %w", err)
	}

	blob, stats, err := Render(raw)
	if err != nil {
		return Stats{}, err
	}

	out, err := os.Create(filepath.Clean(outPath))
	if err != nil {
		return Stats{}, fmt.Errorf("creating %s: %w", outPath, err)
	}
	defer func() { _ = out.Close() }()

	if err := writeBlob(out, blob); err != nil {
		return Stats{}, err
	}
	if st, err := out.Stat(); err == nil {
		stats.Bytes = st.Size()
	}
	return stats, nil
}

func writeBlob(w io.Writer, blob string) error {
	zw, err := gzip.NewWriterLevel(w, gzip.BestCompression)
	if err != nil {
		return fmt.Errorf("compressing registry: %w", err)
	}
	if _, err := zw.Write([]byte(blob)); err != nil {
		return fmt.Errorf("writing registry: %w", err)
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("closing registry: %w", err)
	}
	return nil
}

// Render turns manifest JSON into the registry blob.
func Render(raw []byte) (string, Stats, error) {
	var entries []manifestEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return "", Stats{}, fmt.Errorf("parsing manifest: %w", err)
	}
	if len(entries) == 0 {
		return "", Stats{}, fmt.Errorf("manifest is empty")
	}

	var (
		setLines []string
		msgToSet = map[string]map[string]bool{}
		stats    Stats
	)

	for _, e := range entries {
		if e.Status != "success" {
			stats.Skipped++
			continue
		}
		display := strings.TrimSpace(extraSpace.ReplaceAllString(
			versionSuffix.ReplaceAllString(e.Title, ""), " "))
		if display == "" {
			display = e.Title
		}
		setLines = append(setLines, strings.Join([]string{
			e.ID, display, e.Category, e.Version, strconv.Itoa(e.FilesCount),
		}, "\t"))

		for _, f := range e.Files {
			if !strings.HasSuffix(f, ".xsd") {
				continue
			}
			id := strings.TrimSuffix(f, ".xsd")
			if msgToSet[id] == nil {
				msgToSet[id] = map[string]bool{}
			}
			msgToSet[id][e.ID] = true
		}
	}

	sort.Slice(setLines, func(i, j int) bool {
		return numericID(setLines[i]) < numericID(setLines[j])
	})

	msgIDs := make([]string, 0, len(msgToSet))
	for id := range msgToSet {
		msgIDs = append(msgIDs, id)
	}
	sort.Strings(msgIDs)

	var sb strings.Builder
	sb.WriteString("#SETS\n")
	for _, l := range setLines {
		sb.WriteString(l)
		sb.WriteByte('\n')
	}
	sb.WriteString("#MESSAGES\n")
	for _, id := range msgIDs {
		sets := make([]string, 0, len(msgToSet[id]))
		for s := range msgToSet[id] {
			sets = append(sets, s)
		}
		sort.Slice(sets, func(i, j int) bool { return atoi(sets[i]) < atoi(sets[j]) })
		sb.WriteString(id)
		sb.WriteByte('\t')
		sb.WriteString(strings.Join(sets, ","))
		sb.WriteByte('\n')
	}

	stats.Sets = len(setLines)
	stats.Messages = len(msgIDs)
	return sb.String(), stats, nil
}

func numericID(line string) int { return atoi(line[:strings.IndexByte(line, '\t')]) }

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
