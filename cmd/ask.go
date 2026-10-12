// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/sebastienrousseau/askiso/internal/ai"
	"github.com/sebastienrousseau/askiso/internal/catalog"
	"github.com/sebastienrousseau/askiso/internal/cbprworkspace"
	"github.com/sebastienrousseau/askiso/internal/rules"
	"github.com/sebastienrousseau/askiso/internal/tui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	rawOutput    bool
	textOutput   bool
	plainOutput  bool
	askCBPRPack  string
	askCBPRLimit int

	stripMarkdownBoldRegex   = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	stripMarkdownItalicRegex = regexp.MustCompile(`\*([^*]+)\*`)
	stripMarkdownCodeRegex   = regexp.MustCompile("`([^`]+)`")
	ansiRegex                = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
)

var askCmd = &cobra.Command{
	Use:     "ask [prompt]",
	Aliases: []string{"chat", "a"},
	Short:   "Ask the ISO 20022 AI Assistant a question or enter interactive REPL",
	Long: `AskISO provides an interactive REPL and CLI interface for querying the 
ISO 20022 knowledge base, comparing messages, inspecting schemas, and interacting
with local or connected AI models.`,
	RunE: runAsk,
}

// runAsk answers a question or opens the REPL. The root command shares it,
// so `askiso ask "…"` and a bare `askiso <question>` take the same path.
func runAsk(cmd *cobra.Command, args []string) error {
	prompt := strings.Join(args, " ")

	// Check if input is being piped
	stat, _ := os.Stdin.Stat()
	isPiped := (stat.Mode() & os.ModeCharDevice) == 0

	if isPiped && prompt == "" {
		scanner := bufio.NewScanner(os.Stdin)
		var sb strings.Builder
		for scanner.Scan() {
			sb.WriteString(scanner.Text() + " ")
		}
		prompt = strings.TrimSpace(sb.String())
	}

	// A local pack query is intentionally resolved before an AI engine exists.
	// This is a hard privacy boundary: neither OpenAI nor Ollama is called, even
	// when provider environment variables are configured.
	if askCBPRPack != "" {
		if prompt == "" {
			return fmt.Errorf("--cbpr-pack needs a question; interactive pack sessions are not supported")
		}
		result, err := cbprworkspace.SearchLocalSources(askCBPRPack, prompt, askCBPRLimit)
		if err != nil {
			return err
		}
		printLocalCBPRHits(prompt, result.Hits)
		printLocalCBPRWarnings(result.Warnings)
		return nil
	}

	idx, err := loadCatalog()
	if err != nil {
		return err
	}
	aiEng := ai.New(idx)

	// Direct One-shot Execution with Actionable Follow-up Loop
	if prompt != "" {
		ans := aiEng.Query(prompt)
		if rawOutput {
			fmt.Println(ans.Details)
			return nil
		}

		// In interactive terminal, render actionable follow-ups and offer prompt
		isInteractive := !isPiped && !textOutput && !plainOutput
		renderAnswerWithContext(ans, isInteractive)

		if isInteractive && len(ans.Suggestions) > 0 {
			askLoop(aiEng, idx, os.Stdin, ans.Suggestions, false)
		}
		return nil
	}

	// Interactive REPL Mode (benchmarked on aichat)
	if !quiet {
		fmt.Print(tui.GetStyledLogo())
	}
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#22D3EE")).Render("=== AskISO Ask AI (REPL) ==="))
	fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("Type your question in natural language (e.g. 'what is pacs008', 'compare pacs.008 vs pacs.009')."))
	fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("Commands: /help, /info <id>, /xml <id>, /xsd <id>, /clear, /exit"))
	fmt.Println()

	askLoop(aiEng, idx, os.Stdin, nil, true)
	return nil
}

func printLocalCBPRHits(query string, hits []rules.CBPRPackHit) {
	if rawOutput || textOutput || plainOutput {
		fmt.Println("Local CBPR+ evidence (no model or network provider used)")
		if len(hits) == 0 {
			fmt.Printf("No matching passage found for %q.\n", query)
			return
		}
		for i, hit := range hits {
			location := hit.Source
			if hit.Page > 0 {
				location += fmt.Sprintf(", page %d", hit.Page)
			}
			fmt.Printf("\n%d. %s\n%s\n", i+1, location, hit.Snippet)
		}
		return
	}

	fmt.Printf("\n%s Local CBPR+ evidence\n", headStyle.Render(" PRIVATE "))
	fmt.Println("  No model or network provider was used; results are extracts from your local files.")
	if len(hits) == 0 {
		fmt.Printf("\n  No matching passage found for %q.\n\n", query)
		return
	}
	for i, hit := range hits {
		location := titleStyle.Render(hit.Source)
		if hit.Page > 0 {
			location += fmt.Sprintf(" — page %d", hit.Page)
		} else if hit.Kind != "" {
			location += " — " + hit.Kind
		}
		fmt.Printf("\n  %d. %s\n", i+1, location)
		for _, line := range wrapAt(hit.Snippet, 76) {
			fmt.Printf("     %s\n", line)
		}
	}
	fmt.Println()
}

func printLocalCBPRWarnings(warnings []string) {
	for _, warning := range warnings {
		fmt.Printf("warning: %s\n", warning)
	}
}

func init() {
	askCmd.Flags().StringVar(&askCBPRPack, "cbpr-pack", "",
		"Search private local CBPR+ PDF, JSON, XML/XSD, and XLSX sources without a model")
	askCmd.Flags().IntVar(&askCBPRLimit, "cbpr-limit", 5,
		"Maximum local CBPR+ evidence passages (1-20)")
}

// askLoop is the interactive conversation: numbered follow-ups, slash commands
// and free-text questions, reading a line at a time from in.
//
// It takes a reader rather than using os.Stdin directly so the whole session can
// be driven from a test. allowSlash distinguishes the full REPL from the shorter
// follow-up prompt offered after a one-shot answer.
func askLoop(eng *ai.Engine, idx *catalog.Index, in io.Reader, suggestions []string, allowSlash bool) {
	lastSuggestions := suggestions
	scanner := bufio.NewScanner(in)

	for {
		bar := lipgloss.NewStyle().Foreground(lipgloss.Color("#06B6D4")).Render("┃")
		promptLabel := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#22D3EE")).Render("AskISO > ")
		fmt.Print(" " + bar + "  " + promptLabel)

		if !scanner.Scan() {
			return
		}
		line := strings.TrimSpace(scanner.Text())

		// A blank line ends the follow-up prompt but is ignored in the REPL,
		// where the user is mid-session.
		if line == "" {
			if allowSlash {
				continue
			}
			sayGoodbye()
			return
		}

		if isQuitWord(line) {
			sayGoodbye()
			return
		}

		if num := parseSuggestionIndex(line); num >= 1 && num <= len(lastSuggestions) {
			selected := lastSuggestions[num-1]
			fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("#06B6D4")).
				Render(fmt.Sprintf("┃ ↪ Running [%d]: %s", num, selected)))
			ans := eng.Query(selected)
			lastSuggestions = ans.Suggestions
			renderAnswerWithContext(ans, true)
			continue
		}

		if allowSlash && strings.HasPrefix(line, "/") {
			if done := runReplSlashCommand(idx, line); done {
				return
			}
			continue
		}

		ans := eng.Query(line)
		lastSuggestions = ans.Suggestions
		renderAnswerWithContext(ans, true)
	}
}

// runReplSlashCommand handles one in-session command, reporting whether the
// session should end.
func runReplSlashCommand(idx *catalog.Index, line string) bool {
	parts := strings.Fields(line)
	name := strings.ToLower(parts[0])

	arg := ""
	if len(parts) > 1 {
		arg = parts[1]
	}

	switch name {
	case "/exit", "/quit", "/q":
		sayGoodbye()
		return true
	case "/clear", "/cls":
		fmt.Print("\033[H\033[2J")
	case "/help", "/h", "/?":
		printReplHelp()
	case "/info":
		if arg == "" {
			fmt.Println("Usage: /info <message-id> (e.g. /info pacs.008)")
		} else {
			showMsgInfo(idx, arg, idx.RootDir)
		}
	case "/xml":
		if arg == "" {
			fmt.Println("Usage: /xml <message-id>")
		} else {
			showMsgFile(idx, arg, true, idx.RootDir)
		}
	case "/xsd":
		if arg == "" {
			fmt.Println("Usage: /xsd <message-id>")
		} else {
			showMsgFile(idx, arg, false, idx.RootDir)
		}
	default:
		fmt.Printf("Unknown command %s. Type /help for available commands.\n", name)
	}
	return false
}

func isQuitWord(line string) bool {
	switch strings.ToLower(line) {
	case "q", "exit", "quit", "bye", "/exit", "/quit", "/q", ":q":
		return true
	}
	return false
}

func sayGoodbye() {
	fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("Goodbye!"))
}

func parseSuggestionIndex(input string) int {
	clean := strings.TrimSpace(input)
	clean = strings.TrimPrefix(clean, "run")
	clean = strings.TrimPrefix(clean, "select")
	clean = strings.TrimPrefix(clean, "option")
	clean = strings.TrimPrefix(clean, "choice")
	clean = strings.TrimSpace(clean)
	clean = strings.TrimPrefix(clean, "[")
	clean = strings.TrimSuffix(clean, "]")
	clean = strings.TrimPrefix(clean, "(")
	clean = strings.TrimSuffix(clean, ")")
	clean = strings.TrimPrefix(clean, "#")
	clean = strings.TrimSuffix(clean, ".")
	clean = strings.TrimSpace(clean)
	if num, err := strconv.Atoi(clean); err == nil {
		return num
	}
	return -1
}

// defaultTerminalWidth is used when the width cannot be discovered, for example
// when output is piped.
const defaultTerminalWidth = 60

var controllingTerminalWidthFn = controllingTerminalWidth

// getTerminalWidth reports the usable column count.
//
// The controlling-terminal probe is platform specific and lives in
// terminal_unix.go / terminal_other.go; the rest of the strategy is shared.
func getTerminalWidth() int {
	// 1. The controlling terminal, which is correct even when output is piped.
	if w := controllingTerminalWidthFn(); w > 0 {
		return w
	}

	// 2. Whichever standard stream is still a terminal.
	for _, fd := range []int{int(os.Stdout.Fd()), int(os.Stderr.Fd()), int(os.Stdin.Fd())} {
		if w, _, err := term.GetSize(fd); err == nil && w > 0 {
			return w
		}
	}

	// 3. Ask the terminal database.
	cmd := exec.Command("tput", "cols")
	cmd.Stdin = os.Stdin
	if out, err := cmd.Output(); err == nil {
		if w, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && w > 0 {
			return w
		}
	}

	return defaultTerminalWidth
}

func applyVerticalDelimiter(text string, color string) string {
	bar := lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render("┃")
	lines := strings.Split(text, "\n")
	var sb strings.Builder
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			sb.WriteString(" " + bar)
		} else {
			sb.WriteString(" " + bar + "  " + line)
		}
		if i < len(lines)-1 {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func renderAnswer(ans ai.MessageAnswer) {
	renderAnswerWithContext(ans, false)
}

func renderAnswerWithContext(ans ai.MessageAnswer, isRepl bool) {
	termWidth := getTerminalWidth()

	// Constrain wrap width to fit safely in 55-80 column windows
	wrapWidth := termWidth - 6
	if wrapWidth > 62 {
		wrapWidth = 62
	}
	if wrapWidth < 30 {
		wrapWidth = 30
	}

	isPlain := textOutput || plainOutput || os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb"

	if isPlain {
		fmt.Println()
		fmt.Printf("=== %s ===\n\n", ans.Summary)
		if ans.ProviderWarning != "" {
			fmt.Printf("Provider warning: %s\n\n", ans.ProviderWarning)
		}
		plainText := renderPlainText(ans.Details, wrapWidth)
		fmt.Println(plainText)
		fmt.Println()
		if len(ans.Suggestions) > 0 {
			fmt.Println("Suggested follow-ups:")
			for _, s := range ans.Suggestions {
				fmt.Printf("  - %s\n", s)
			}
			fmt.Println()
		}
		return
	}

	// Custom Glamour Style: Zero document margin and zero '#' prefixes
	customStyle := styles.DarkStyleConfig
	emptyPrefix := ""
	customStyle.H1.Prefix = emptyPrefix
	customStyle.H2.Prefix = emptyPrefix
	customStyle.H3.Prefix = emptyPrefix
	customStyle.H4.Prefix = emptyPrefix
	customStyle.H5.Prefix = emptyPrefix
	customStyle.H6.Prefix = emptyPrefix

	var zero uint = 0
	customStyle.Document.Margin = &zero

	cyan := "#22D3EE"
	lightBlue := "#38BDF8"
	customStyle.H1.Color = &cyan
	customStyle.H2.Color = &cyan
	customStyle.H3.Color = &lightBlue
	customStyle.H4.Color = &lightBlue

	renderedDetails := ans.Details
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(customStyle),
		glamour.WithWordWrap(wrapWidth),
	)
	if err == nil {
		if out, err := r.Render(ans.Details); err == nil {
			renderedDetails = strings.TrimSpace(out)
		}
	}

	headerBadge := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(lipgloss.Color("#06B6D4")).
		Padding(0, 1).
		Render(" " + ans.Summary + " ")

	fmt.Println()
	fmt.Println(" " + headerBadge)
	fmt.Println()
	if ans.ProviderWarning != "" {
		warning := lipgloss.NewStyle().Foreground(lipgloss.Color("#F59E0B")).Render("Provider warning: " + ans.ProviderWarning)
		fmt.Println(applyVerticalDelimiter(warning, "#F59E0B"))
		fmt.Println()
	}

	// Line-by-line vertical delimiter (never truncates or clips!)
	fmt.Println(applyVerticalDelimiter(renderedDetails, "#06B6D4"))
	fmt.Println()

	if len(ans.Suggestions) > 0 {
		var sb strings.Builder
		title := "💡 Suggested follow-ups:"
		if isRepl {
			title = fmt.Sprintf("💡 Suggested follow-ups (Type 1-%d to run, or 'q' / [Enter] to exit):", len(ans.Suggestions))
		}
		sb.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#22D3EE")).Render(title) + "\n")
		numBadge := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#06B6D4"))
		textStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#94A3B8"))
		cmdStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

		for i, s := range ans.Suggestions {
			numStr := fmt.Sprintf("[%d] ", i+1)
			cliHint := fmt.Sprintf("(./askiso ask %q)", s)
			sb.WriteString(numBadge.Render(numStr) + textStyle.Render(s) + " " + cmdStyle.Render(cliHint) + "\n")
		}
		if isRepl {
			sb.WriteString(numBadge.Render("[q] ") + textStyle.Render("Exit / Quit") + " " + cmdStyle.Render("(or press Enter)") + "\n")
		}
		fmt.Println(applyVerticalDelimiter(strings.TrimRight(sb.String(), "\n"), "#38BDF8"))
		fmt.Println()
	}
}

func renderPlainText(markdown string, width int) string {
	customStyle := styles.ASCIIStyleConfig
	emptyPrefix := ""
	customStyle.H1.Prefix = emptyPrefix
	customStyle.H2.Prefix = emptyPrefix
	customStyle.H3.Prefix = emptyPrefix
	customStyle.H4.Prefix = emptyPrefix
	customStyle.H5.Prefix = emptyPrefix
	customStyle.H6.Prefix = emptyPrefix

	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(customStyle),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return cleanMarkdownSymbols(markdown)
	}

	out, err := r.Render(markdown)
	if err != nil {
		return cleanMarkdownSymbols(markdown)
	}
	cleaned := ansiRegex.ReplaceAllString(out, "")
	cleaned = stripMarkdownBoldRegex.ReplaceAllString(cleaned, "$1")
	cleaned = stripMarkdownItalicRegex.ReplaceAllString(cleaned, "$1")
	cleaned = stripMarkdownCodeRegex.ReplaceAllString(cleaned, "$1")
	return strings.TrimSpace(cleaned)
}

var (
	headingRegex = regexp.MustCompile(`(?m)^#+\s*`)
	// Fences must go before inline code: otherwise the inline rule consumes a
	// pair of backticks from each fence and leaves the rest behind.
	fenceRegex = regexp.MustCompile("```[a-zA-Z]*\n?")
)

func cleanMarkdownSymbols(s string) string {
	s = headingRegex.ReplaceAllString(s, "")
	s = fenceRegex.ReplaceAllString(s, "")
	s = stripMarkdownBoldRegex.ReplaceAllString(s, "$1")
	s = stripMarkdownItalicRegex.ReplaceAllString(s, "$1")
	s = stripMarkdownCodeRegex.ReplaceAllString(s, "$1")
	return strings.TrimSpace(s)
}

func printReplHelp() {
	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#22D3EE")).Render("Available REPL Commands:"))
	fmt.Println("  /info <id>       Display message metadata and file paths (e.g. /info pacs.008)")
	fmt.Println("  /xml  <id>       Print sample XML file path or contents")
	fmt.Println("  /xsd  <id>       Print XSD schema file path or contents")
	fmt.Println("  /clear           Clear the terminal screen")
	fmt.Println("  /help            Show this help menu")
	fmt.Println("  /exit, /quit     Exit the Ask AI session")
	fmt.Println()
}

func showMsgInfo(idx *catalog.Index, query, rootDir string) {
	results := idx.Search(query)
	if len(results) == 0 {
		fmt.Printf("No messages found matching %q\n", query)
		return
	}
	m := results[0]
	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#22D3EE")).Render("Message Metadata: " + m.ID))
	fmt.Printf("  • Category:    %s\n", m.Category)
	fmt.Printf("  • Version:     %s\n", m.Version)
	if m.XMLSamplePath != "" {
		rel, _ := filepath.Rel(rootDir, m.XMLSamplePath)
		fmt.Printf("  • XML Sample:  %s\n", rel)
	}
	if m.XSDPath != "" {
		rel, _ := filepath.Rel(rootDir, m.XSDPath)
		fmt.Printf("  • XSD Schema:  %s\n", rel)
	}
	fmt.Println()
}

func showMsgFile(idx *catalog.Index, query string, isXML bool, rootDir string) {
	results := idx.Search(query)
	if len(results) == 0 {
		fmt.Printf("No messages found matching %q\n", query)
		return
	}
	m := results[0]
	filePath := m.XSDPath
	label := "XSD Schema"
	if isXML {
		filePath = m.XMLSamplePath
		label = "XML Sample"
	}
	if filePath == "" {
		fmt.Printf("No %s file available for %s\n", label, m.ID)
		return
	}
	rel, _ := filepath.Rel(rootDir, filePath)
	fmt.Printf("\n%s for %s:\n  File: %s\n\n", label, m.ID, rel)
}

func init() {
	askCmd.Flags().BoolVarP(&rawOutput, "raw", "r", false, "Print raw unformatted markdown response")
	askCmd.Flags().BoolVarP(&textOutput, "text", "t", false, "Print clean plain text output without ANSI codes")
	askCmd.Flags().BoolVar(&plainOutput, "plain", false, "Print clean plain text output without ANSI codes")
	RootCmd.AddCommand(askCmd)
}
