// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

// Package tui provides an interactive Bubble Tea terminal user interface for AskISO.
package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/atotto/clipboard"

	"os/exec"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/sebastienrousseau/askiso/internal/ai"
	"github.com/sebastienrousseau/askiso/internal/catalog"
	"github.com/sebastienrousseau/askiso/internal/codes"
	"github.com/sebastienrousseau/askiso/internal/flow"
	"github.com/sebastienrousseau/askiso/internal/generator"
	"github.com/sebastienrousseau/askiso/internal/graph"
)

// Version is the build version of AskISO, rendered in the TUI footer.
var Version = "0.0.1"

var logoLines = []string{
	`           ⣀⣤⣤⣤⣀            `,
	`          ⢠⣾⠟⠉⠉⠻⣷⡄          `,
	`         ⠸⠟⠁   ⢸⣿⠇          `,
	`              ⣠⣾⠟⠁          `,
	`             ⢀⣾⠟⠁           `,
	`              ⢸⣿⠁           `,
	`                            `,
	`              ⢠⣤⡄           `,
}

// GetStyledLogo returns the colored ASCII logo art as a string.
func getCustomGlamourStyle() ansi.StyleConfig {
	customStyle := styles.DarkStyleConfig
	var zero uint = 0
	customStyle.Document.Margin = &zero
	customStyle.Document.BlockPrefix = ""
	customStyle.Document.BlockSuffix = ""
	emptyPrefix := ""
	customStyle.H1.Prefix = emptyPrefix
	customStyle.H2.Prefix = emptyPrefix
	customStyle.H3.Prefix = emptyPrefix
	customStyle.H4.Prefix = emptyPrefix
	customStyle.H5.Prefix = emptyPrefix
	customStyle.H6.Prefix = emptyPrefix
	customStyle.Item.BlockPrefix = ""
	return customStyle
}

func applyVerticalDelimiter(text string, color string) string {
	bar := lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render("┃")
	lines := strings.Split(text, "\n")
	var sb strings.Builder
	for i, line := range lines {
		trimmed := strings.TrimRight(line, " \t\r")
		if strings.TrimSpace(trimmed) == "" {
			sb.WriteString(" " + bar)
		} else {
			sb.WriteString(" " + bar + "  " + trimmed)
		}
		if i < len(lines)-1 {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func GetStyledLogo() string {
	colors := []string{
		"#67E8F9",
		"#22D3EE",
		"#06B6D4",
		"#00BDD6",
		"#0891B2",
		"#0E7490",
		"#15728C",
		"#1E90AF",
	}
	var sb strings.Builder
	for i, line := range logoLines {
		sb.WriteString(" " + lipgloss.NewStyle().Foreground(lipgloss.Color(colors[i])).Render(line) + "\n")
	}
	sb.WriteString(" " + lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#22D3EE")).Render("AskISO") + "  " + lipgloss.NewStyle().Foreground(lipgloss.Color("#94A3B8")).Render("Every ISO 20022 message. Just ask.") + "\n\n")
	return sb.String()
}

type mode int

const (
	modeTable mode = iota
	modeViewer
	modeAsk
)

type askMsg struct {
	sender  string
	content string
}

type Model struct {
	idx             *catalog.Index
	aiEngine        *ai.Engine
	mode            mode
	table           table.Model
	spinner         spinner.Model
	progress        progress.Model
	textInput       textinput.Model
	viewport        viewport.Model
	filter          string
	filteredMsgs    []catalog.Message
	selected        map[string]bool
	askHistory      []askMsg
	lastSuggestions []string
	showHelp        bool
	cmdErr          string
	viewingTitle    string
	viewingContent  string
	width           int
	height          int
}

// NewModel creates a new AskISO TUI model.
func NewModel(idx *catalog.Index) Model {
	columns := []table.Column{
		{Title: " ", Width: 3},
		{Title: "Message ID", Width: 22},
		{Title: "Category / Domain", Width: 38},
		{Title: "Version", Width: 14},
		{Title: "XML Sample", Width: 12},
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithFocused(true),
		table.WithHeight(7),
	)

	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("238")).
		BorderBottom(true).
		Bold(true)
	s.Selected = s.Selected.
		Foreground(lipgloss.Color("255")).
		Background(lipgloss.Color("#06B6D4")).
		Bold(true)
	t.SetStyles(s)

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("#06B6D4"))

	ti := textinput.New()
	ti.Placeholder = "Type to filter messages or /command..."
	ti.Prompt = ""
	ti.Focus()
	ti.CharLimit = 100
	ti.Width = 60

	vp := viewport.New(80, 20)

	m := Model{
		idx:          idx,
		aiEngine:     ai.New(idx),
		mode:         modeTable,
		table:        t,
		spinner:      sp,
		progress:     progress.New(progress.WithoutPercentage(), progress.WithGradient("#38BDF8", "#0284C7")),
		textInput:    ti,
		viewport:     vp,
		selected:     make(map[string]bool),
		filteredMsgs: idx.Messages,
		askHistory: []askMsg{
			{
				sender:  "AskISO Assistant",
				content: "TL;DR: Welcome to AskISO Ask AI! Query any ISO 20022 message, comparison, or payment workflow.\n\n💡 **Suggested follow-ups (Type 1-3 to run):**\n• [1] What is pacs.008?\n• [2] Compare pacs.008 vs pacs.009\n• [3] How does camt.053 work?",
			},
		},
		lastSuggestions: []string{
			"What is pacs.008?",
			"Compare pacs.008 vs pacs.009",
			"How does camt.053 work?",
		},
	}

	m.updateTableRows()
	return m
}

func (m *Model) updateTableRows() {
	var rows []table.Row
	for _, msg := range m.filteredMsgs {
		chk := "[ ]"
		if m.selected[msg.ID] {
			chk = "[✓]"
		}
		hasXML := "Yes"
		if msg.XMLSamplePath == "" {
			hasXML = "No"
		}
		rows = append(rows, table.Row{
			chk,
			msg.ID,
			msg.Category,
			msg.Version,
			hasXML,
		})
	}
	m.table.SetRows(rows)
}

func (m *Model) applyFilter() {
	q := strings.TrimSpace(m.filter)
	if strings.HasPrefix(q, "/") {
		return
	}
	m.filteredMsgs = m.idx.Search(q)
	m.updateTableRows()
	if len(m.filteredMsgs) > 0 {
		m.table.SetCursor(0)
	}
}

func (m Model) Init() tea.Cmd {
	return textinput.Blink
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = msg.Width - 6
		m.viewport.Height = msg.Height - 6
		m.textInput.Width = msg.Width - 12
		tableH := msg.Height - 14
		if tableH < 4 {
			tableH = 4
		}
		m.table.SetHeight(tableH)

	case tea.KeyMsg:
		m.cmdErr = ""

		// Global Keybindings
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		}

		switch m.mode {
		case modeTable:
			switch msg.String() {
			case "esc":
				if m.showHelp {
					m.showHelp = false
					return m, nil
				}
				if m.filter != "" {
					m.filter = ""
					m.applyFilter()
					return m, nil
				}
				return m, tea.Quit

			case "q":
				if m.showHelp {
					m.showHelp = false
					return m, nil
				}
				if m.filter == "" {
					return m, tea.Quit
				}
				// Mid-filter it is a letter, not a command: category names such
				// as "Liquidity Management" and "Cheques Management" contain it.
				m.filter += msg.String()
				m.applyFilter()
				return m, nil

			case "?":
				m.showHelp = !m.showHelp
				return m, nil

			case "tab":
				if strings.HasPrefix(m.filter, "/") {
					commands := []string{"/help", "/ask", "/chat", "/catalog", "/check", "/flow", "/code", "/stats", "/doctor", "/graph", "/table", "/sort", "/all", "/none", "/clear", "/exit", "/quit"}
					for _, c := range commands {
						if len(c) > len(m.filter) && strings.HasPrefix(c, m.filter) {
							m.filter = c
							return m, nil
						}
					}
				}

			case "enter":
				if strings.HasPrefix(m.filter, "/") {
					target := m.filter
					cmd := m.executeSlashCommand(target)
					m.filter = ""
					return m, cmd
				}
				if len(m.filteredMsgs) > 0 {
					idx := m.table.Cursor()
					if idx >= 0 && idx < len(m.filteredMsgs) {
						selectedMsg := m.filteredMsgs[idx]
						m.openFile(selectedMsg.XMLSamplePath, selectedMsg.ID+" (XML Sample)")
						m.mode = modeViewer
						return m, nil
					}
				}

			case "ctrl+s":
				// Every plain letter belongs to the filter: a shortcut that
				// takes one makes every message name containing it unreachable.
				// That is why the schema, copy and check keys are all modified.
				if len(m.filteredMsgs) > 0 {
					idx := m.table.Cursor()
					if idx >= 0 && idx < len(m.filteredMsgs) {
						selectedMsg := m.filteredMsgs[idx]
						m.openFile(selectedMsg.XSDPath, selectedMsg.ID+" (XSD Schema)")
						m.mode = modeViewer
						return m, nil
					}
				}

			case "ctrl+k":
				// Check the selected message: business rules, its schema, and
				// the November 2026 address rules, in one pane. Bound to a
				// modifier because every plain letter belongs to the filter.
				if len(m.filteredMsgs) > 0 {
					idx := m.table.Cursor()
					if idx >= 0 && idx < len(m.filteredMsgs) {
						m.checkMessage(m.filteredMsgs[idx])
						m.mode = modeViewer
						return m, nil
					}
				}

			case "ctrl+y":
				if len(m.filteredMsgs) > 0 {
					idx := m.table.Cursor()
					if idx >= 0 && idx < len(m.filteredMsgs) {
						msg := m.filteredMsgs[idx]
						if msg.XMLSamplePath != "" {
							if data, err := os.ReadFile(msg.XMLSamplePath); err == nil {
								_ = clipboard.WriteAll(string(data))
								m.cmdErr = fmt.Sprintf("Copied %s XML sample to clipboard!", msg.ID)
								return m, nil
							}
						}
						_ = clipboard.WriteAll(msg.ID)
						m.cmdErr = fmt.Sprintf("Copied '%s' to clipboard!", msg.ID)
						return m, nil
					}
				}

			// Letters always type into the filter -- filtering the catalogue is
			// what the table is for. Binding bare 'a' and 'c' to the assistant
			// made twelve of the roughly thirty ISO 20022 domains unreachable
			// (camt, acmt, colr, auth, casp, catm, ...), since their names begin
			// with one of those letters. The assistant is on ctrl+a and /ask.
			case "ctrl+a":
				m.mode = modeAsk
				m.textInput.SetValue("")
				m.textInput.Placeholder = "Type a question or 1-3..."
				m.textInput.Prompt = ""
				return m, nil

			case " ", "space":
				if strings.HasPrefix(m.filter, "/") {
					m.filter += " "
					return m, nil
				}
				if len(m.filteredMsgs) > 0 {
					idx := m.table.Cursor()
					if idx >= 0 && idx < len(m.filteredMsgs) {
						id := m.filteredMsgs[idx].ID
						m.selected[id] = !m.selected[id]
						m.updateTableRows()
					}
				}
				return m, nil

			case "backspace":
				if len(m.filter) > 0 {
					m.filter = m.filter[:len(m.filter)-1]
					m.applyFilter()
				}
				return m, nil

			default:
				if len(msg.String()) == 1 && len(msg.Runes) > 0 && msg.Runes[0] >= 32 && msg.Runes[0] <= 126 {
					m.filter += msg.String()
					m.applyFilter()
					return m, nil
				}
			}
			m.table, cmd = m.table.Update(msg)
			cmds = append(cmds, cmd)

		case modeViewer:
			switch msg.String() {
			case "esc", "q":
				m.mode = modeTable
				return m, nil
			case "y":
				if m.viewingContent != "" {
					_ = clipboard.WriteAll(m.viewingContent)
				}
				return m, nil
			default:
				m.viewport, cmd = m.viewport.Update(msg)
				cmds = append(cmds, cmd)
			}

		case modeAsk:
			switch msg.String() {
			case "esc":
				m.mode = modeTable
				m.textInput.SetValue("")
				m.textInput.Placeholder = "Type to filter messages or /command (/help, /ask, /sort)..."
				return m, nil
			case "enter":
				val := strings.TrimSpace(m.textInput.Value())
				if val != "" {
					lowerVal := strings.ToLower(val)

					// Universal Exit Commands
					if lowerVal == "q" || lowerVal == "quit" || lowerVal == "exit" || lowerVal == "/exit" || lowerVal == "/quit" || lowerVal == "/q" || lowerVal == ":q" || lowerVal == "bye" {
						m.mode = modeTable
						m.textInput.SetValue("")
						m.textInput.Placeholder = "Type to filter messages or /command (/help, /ask, /sort)..."
						return m, nil
					}

					// Clear / Reset in Ask mode
					if lowerVal == "/clear" || lowerVal == "/reset" {
						m.askHistory = m.askHistory[:1]
						m.textInput.SetValue("")
						return m, nil
					}

					// Back / Table command
					if lowerVal == "/table" || lowerVal == "/back" || lowerVal == "/list" {
						m.mode = modeTable
						m.textInput.SetValue("")
						m.textInput.Placeholder = "Type to filter messages or /command (/help, /ask, /sort)..."
						return m, nil
					}
					// Support shortcut numbers 1, 2, 3 or [1], [2], [3]
					targetQuery := val
					clean := strings.TrimPrefix(val, "[")
					clean = strings.TrimSuffix(clean, "]")
					clean = strings.TrimPrefix(clean, "(")
					clean = strings.TrimSuffix(clean, ")")
					clean = strings.TrimPrefix(clean, "#")
					clean = strings.TrimSuffix(clean, ".")
					clean = strings.TrimSpace(clean)
					if num, err := strconv.Atoi(clean); err == nil && num >= 1 && num <= len(m.lastSuggestions) {
						targetQuery = m.lastSuggestions[num-1]
					}

					m.askHistory = append(m.askHistory, askMsg{sender: "You", content: targetQuery})
					m.textInput.SetValue("")
					ans := m.aiEngine.Query(targetQuery)
					m.lastSuggestions = ans.Suggestions

					rawMarkdown := fmt.Sprintf("### %s\n\n%s", ans.Summary, ans.Details)
					if len(ans.Suggestions) > 0 {
						rawMarkdown += "\n\n💡 **Suggested follow-ups (Type 1-3 to run):**\n"
						for idx, s := range ans.Suggestions {
							rawMarkdown += fmt.Sprintf("• [%d] %s\n", idx+1, s)
						}
					}

					renderedReply := rawMarkdown
					wrapW := m.width - 16
					if wrapW < 30 {
						wrapW = 30
					}
					if wrapW > 66 {
						wrapW = 66
					}
					r, err := glamour.NewTermRenderer(
						glamour.WithStyles(getCustomGlamourStyle()),
						glamour.WithWordWrap(wrapW),
					)
					if err == nil {
						if out, err := r.Render(rawMarkdown); err == nil {
							renderedReply = strings.TrimSpace(out)
						}
					}

					m.askHistory = append(m.askHistory, askMsg{sender: "AskISO Assistant", content: renderedReply})
				}
				m.textInput, cmd = m.textInput.Update(msg)
				cmds = append(cmds, cmd)
			}
		}
	}

	return m, tea.Batch(cmds...)
}

func (m *Model) openMarkdown(content, label string) {
	m.viewingTitle = label
	wrapW := m.viewport.Width - 4
	if wrapW < 30 {
		wrapW = 60
	}
	r, err := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(wrapW))
	if err == nil {
		if rendered, err := r.Render(content); err == nil {
			content = rendered
		}
	}
	m.viewingContent = content
	m.viewport.SetContent(m.viewingContent)
	m.viewport.GotoTop()
}

func (m *Model) openFile(path, label string) {
	m.viewingTitle = label
	contentBytes, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		m.viewingContent = fmt.Sprintf("Error loading file: %v", err)
	} else {
		content := string(contentBytes)
		if strings.HasSuffix(path, ".md") {
			r, err := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(m.viewport.Width-4))
			if err == nil {
				rendered, err := r.Render(content)
				if err == nil {
					content = rendered
				}
			}
		}
		m.viewingContent = content
	}
	m.viewport.SetContent(m.viewingContent)
	m.viewport.GotoTop()
}

func (m *Model) executeSlashCommand(cmdStr string) tea.Cmd {
	parts := strings.Fields(strings.TrimSpace(cmdStr))
	if len(parts) == 0 {
		return nil
	}
	cmd := parts[0]

	switch cmd {
	case "/exit", "/quit", "/q", ":q":
		return tea.Quit
	case "/help", "/?", "/h":
		m.showHelp = true
	case "/ask", "/chat":
		m.mode = modeAsk
		m.textInput.SetValue("")
		m.textInput.Placeholder = "Type a question or 1-3..."
		m.textInput.Prompt = ""
	case "/table", "/back", "/list":
		m.mode = modeTable
		m.showHelp = false
	case "/clear", "/reset":
		m.filter = ""
		m.applyFilter()
		if len(m.askHistory) > 1 {
			m.askHistory = m.askHistory[:1]
		}
	case "/all":
		for _, msg := range m.filteredMsgs {
			m.selected[msg.ID] = true
		}
		m.updateTableRows()
	case "/none":
		for _, msg := range m.filteredMsgs {
			m.selected[msg.ID] = false
		}
		m.updateTableRows()
	case "/sort":
		if len(parts) >= 2 {
			f := strings.ToLower(parts[1])
			switch f {
			case "id", "code":
				sort.Slice(m.filteredMsgs, func(i, j int) bool {
					return m.filteredMsgs[i].ID < m.filteredMsgs[j].ID
				})
			case "category", "cat", "domain":
				sort.Slice(m.filteredMsgs, func(i, j int) bool {
					return m.filteredMsgs[i].Category < m.filteredMsgs[j].Category
				})
			case "version", "ver":
				sort.Slice(m.filteredMsgs, func(i, j int) bool {
					return m.filteredMsgs[i].Version < m.filteredMsgs[j].Version
				})
			}
			m.updateTableRows()
		} else {
			m.cmdErr = "Usage: /sort <id|category|version>"
		}
	case "/flow", "/simulate":
		preset := "sepa"
		if len(parts) >= 2 {
			preset = parts[1]
		}
		opts := generator.DefaultOptions("pacs.008")
		opts.Preset = preset
		chain, err := flow.GenerateLifecycle(opts)
		if err != nil {
			m.cmdErr = fmt.Sprintf("Flow simulation failed: %v", err)
			return nil
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "# Payment Lifecycle Simulation (%s)\n\n", strings.ToUpper(chain.Preset))
		fmt.Fprintf(&sb, "• **Shared UETR**: `%s`\n", chain.UETR)
		fmt.Fprintf(&sb, "• **EndToEndID**: `%s`\n", chain.EndToEndID)
		fmt.Fprintf(&sb, "• **Amount**: `%s %s`\n\n", chain.Currency, chain.Amount)
		sb.WriteString("---\n\n")
		for _, step := range chain.Steps {
			fmt.Fprintf(&sb, "## Stage %d: %s (`%s`)\n\n", step.Index, step.Title, step.MsgType)
			sb.WriteString(step.Description + "\n\n")
			sb.WriteString("```xml\n" + step.XMLPayload + "\n```\n\n---\n\n")
		}
		m.openMarkdown(sb.String(), fmt.Sprintf("Payment Flow Simulator (%s)", strings.ToUpper(chain.Preset)))
		m.mode = modeViewer
		return nil

	case "/code", "/codes":
		if len(parts) < 2 {
			m.cmdErr = "Usage: /code <code-or-keyword> (e.g. /code AC04, /code SALA)"
			return nil
		}
		query := strings.Join(parts[1:], " ")
		results := codes.Lookup(query)
		if len(results) == 0 {
			m.cmdErr = fmt.Sprintf("No codes found matching '%s'", query)
			return nil
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "# ISO 20022 Code Lookup: %s\n\n", query)
		for _, item := range results {
			fmt.Fprintf(&sb, "## %s — %s\n\n", item.Code, item.Name)
			fmt.Fprintf(&sb, "• **Category**: %s\n", item.Category)
			fmt.Fprintf(&sb, "• **Description**: %s\n", item.Description)
			fmt.Fprintf(&sb, "• **Applies To**: `%s`\n\n---\n\n", item.AppliesTo)
		}
		m.openMarkdown(sb.String(), fmt.Sprintf("Code: %s", query))
		m.mode = modeViewer
		return nil

	case "/stats", "/metrics":
		domainCounts := make(map[string]int)
		for _, msg := range m.idx.Messages {
			parts := strings.Split(msg.ID, ".")
			if len(parts) > 0 {
				dCode := strings.ToLower(parts[0])
				domainCounts[dCode]++
			}
		}
		var sb strings.Builder
		sb.WriteString("# ISO 20022 Repository Metrics & Inventory\n\n")
		fmt.Fprintf(&sb, "• **Total Message Definitions**: %d\n", len(m.idx.Messages))
		fmt.Fprintf(&sb, "• **Total Categories**: %d\n", len(m.idx.Categories))
		fmt.Fprintf(&sb, "• **Active Business Domains**: %d\n\n", len(domainCounts))
		sb.WriteString("| Domain | Message Count | Share |\n| :--- | :--- | :--- |\n")
		type dItem struct {
			code  string
			count int
		}
		var items []dItem
		for c, cnt := range domainCounts {
			items = append(items, dItem{c, cnt})
		}
		sort.Slice(items, func(i, j int) bool { return items[i].count > items[j].count })
		for _, item := range items {
			pct := float64(item.count) / float64(len(m.idx.Messages)) * 100.0
			fmt.Fprintf(&sb, "| `%s` | %d | %.1f%% |\n", item.code, item.count, pct)
		}
		m.openMarkdown(sb.String(), "Repository Metrics & Stats")
		m.mode = modeViewer
		return nil

	case "/catalog", "/sets":
		m.viewingTitle = "Catalogue"
		m.viewingContent = m.renderCatalogue()
		m.viewport.SetContent(m.viewingContent)
		m.viewport.GotoTop()
		m.mode = modeViewer
		return nil

	case "/check", "/validate":
		if len(m.filteredMsgs) == 0 {
			m.cmdErr = "Nothing to check: no message is selected."
			return nil
		}
		idx := m.table.Cursor()
		if idx < 0 || idx >= len(m.filteredMsgs) {
			idx = 0
		}
		m.checkMessage(m.filteredMsgs[idx])
		m.mode = modeViewer
		return nil

	case "/doctor":
		var sb strings.Builder
		sb.WriteString("# AskISO Environment & System Diagnostics\n\n")
		fmt.Fprintf(&sb, "• ✅ **Catalog Index**: %d message definitions loaded across %d categories\n", len(m.idx.Messages), len(m.idx.Categories))
		if xmllintPath, err := exec.LookPath("xmllint"); err == nil {
			fmt.Fprintf(&sb, "• ✅ **XML Schema Validator**: `xmllint` active at `%s`\n", xmllintPath)
		} else {
			sb.WriteString("• ⚠️ **XML Schema Validator**: Using pure-Go fallback validator\n")
		}
		hasClipboard := false
		for _, tool := range []string{"pbcopy", "wl-copy", "xclip", "xsel"} {
			if _, err := exec.LookPath(tool); err == nil {
				hasClipboard = true
				fmt.Fprintf(&sb, "• ✅ **Clipboard Engine**: `%s` detected\n", tool)
				break
			}
		}
		if !hasClipboard {
			sb.WriteString("• ℹ️ **Clipboard Engine**: Standard clipboard interface active\n")
		}
		sb.WriteString("\n*All core systems operating normally!*\n")
		m.openMarkdown(sb.String(), "System Diagnostics (Doctor)")
		m.mode = modeViewer
		return nil

	case "/graph", "/diagram":
		preset := "sepa"
		if len(parts) >= 2 {
			preset = parts[1]
		}
		asciiDiagram := graph.GenerateASCII("pacs.008", preset)
		var sb strings.Builder
		fmt.Fprintf(&sb, "# Payment Lifecycle Flow Diagram (%s)\n\n", strings.ToUpper(preset))
		sb.WriteString("```text\n" + asciiDiagram + "\n```\n\n")
		m.openMarkdown(sb.String(), fmt.Sprintf("Sequence Diagram (%s)", strings.ToUpper(preset)))
		m.mode = modeViewer
		return nil

	default:
		m.cmdErr = fmt.Sprintf("Unknown command: %s. Type /help for help.", cmd)
	}
	return nil
}

func (m Model) View() string {
	if m.mode == modeViewer {
		return m.renderViewer()
	}
	if m.mode == modeAsk {
		return m.renderAsk()
	}

	var header string
	if os.Getenv("ASKISO_SHOW_LOGO") != "0" {
		header = GetStyledLogo()
	} else {
		header = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#22D3EE")).Render("AskISO — ISO 20022 Explorer") + "\n\n"
	}

	out := header

	if m.showHelp {
		out += m.renderHelpPanel()
		out += "\n" + m.renderFooter()
		return out
	}

	// Filter / Search Input
	filterLabel := " Search: "
	if strings.HasPrefix(m.filter, "/") {
		filterLabel = " Command: "
	}
	out += lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#22D3EE")).Render(filterLabel)
	out += lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Render(m.filter)
	out += lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("█\n")

	if m.cmdErr != "" {
		out += lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("  "+m.cmdErr) + "\n"
	}

	// Table
	out += m.table.View() + "\n"

	// Stats
	selectedCount := 0
	for _, v := range m.selected {
		if v {
			selectedCount++
		}
	}
	stats := fmt.Sprintf(" Showing %d of %d messages | %d selected", len(m.filteredMsgs), len(m.idx.Messages), selectedCount)
	out += lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(stats) + "\n"
	out += m.renderFooter()
	return out
}

func (m Model) renderViewer() string {
	var sb strings.Builder
	badge := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#000000")).Background(lipgloss.Color("#06B6D4")).Padding(0, 1)
	sb.WriteString("\n " + badge.Render("VIEWING: "+m.viewingTitle) + " " + lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("[Press 'q' or 'esc' to return]") + "\n\n")
	sb.WriteString(m.viewport.View())
	return sb.String()
}

func (m Model) renderAsk() string {
	var sb strings.Builder

	if os.Getenv("ASKISO_SHOW_LOGO") != "0" {
		sb.WriteString(GetStyledLogo())
	}

	badge := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color("#06B6D4")).Padding(0, 1).Render(" AskISO ISO 20022 Ask AI ")
	hint := lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("[Press 'esc' to return to table]")
	sb.WriteString(" " + badge + " " + hint + "\n\n")

	start := 0
	if len(m.askHistory) > 2 {
		start = len(m.askHistory) - 2
	}
	for i := start; i < len(m.askHistory); i++ {
		c := m.askHistory[i]
		if c.sender == "You" {
			userText := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#22D3EE")).Render("You: ") + c.content
			sb.WriteString(applyVerticalDelimiter(userText, "#22D3EE") + "\n\n")
		} else {
			sb.WriteString(applyVerticalDelimiter(c.content, "#06B6D4") + "\n\n")
		}
	}

	promptLabel := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#22D3EE")).Render("AskISO > ")
	bar := lipgloss.NewStyle().Foreground(lipgloss.Color("#06B6D4")).Render("┃")
	sb.WriteString(" " + bar + "  " + promptLabel + m.textInput.View() + "\n\n")
	sb.WriteString(m.renderFooter())
	return sb.String()
}

func (m Model) renderHelpPanel() string {
	var sb strings.Builder
	sb.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#06B6D4")).Render("   In-Session Commands") + "\n")
	sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("238")).Render("   "+strings.Repeat("─", 68)) + "\n\n")

	commands := [][]string{
		{"[ctrl+k]", "Check the selected message: rules, schema, and CBPR+"},
		{"[ctrl+s]", "Open the selected message's schema"},
		{"[ctrl+y]", "Copy the selected message's sample"},
		{"[ctrl+a]", "Open the assistant"},
		{"/check", "The same check, by command"},
		{"/catalog, /sets", "What is installed against the whole published standard"},
		{"/ask, /chat", "Open conversational ISO 20022 Ask AI Assistant"},
		{"/flow [preset]", "Simulate 4-stage end-to-end payment lifecycle"},
		{"/code <query>", "Lookup ISO 20022 return/purpose/charge codes"},
		{"/stats", "Display domain analytics and catalog metrics"},
		{"/doctor", "Run system diagnostics & health check"},
		{"/graph [preset]", "Display ASCII sequence lifecycle diagram"},
		{"/table, /list", "Return to catalog table view"},
		{"/sort <field>", "Sort messages by id, category (cat), or version (ver)"},
		{"/all, /none", "Select or deselect all messages"},
		{"/clear", "Clear search filter or reset Ask AI conversation"},
		{"/help, /?", "Show this universal in-session help menu"},
		{"/exit, /quit, /q", "Exit AskISO CLI immediately"},
		{"[q] / [esc]", "Universal single-key shortcut to exit or return"},
	}

	for _, c := range commands {
		cmdStr := fmt.Sprintf("   %-16s", c[0])
		descStr := c[1]
		sb.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#06B6D4")).Render(cmdStr))
		sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(descStr) + "\n")
	}

	sb.WriteString("\n")
	sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("240")).
		Render("   Every plain letter filters the table, so the shortcuts are modified.") + "\n")
	sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("240")).
		Render("   Press [?] or [esc] to return to the message table.") + "\n")
	return sb.String()
}

func (m Model) renderFooter() string {
	vStr := Version
	if vStr == "" {
		vStr = "0.0.1"
	}

	left := " [Enter] XML | [^s] XSD | [^k] Check | [^y] Copy | [^a] Ask | [?] Help"
	rightQuit := " [esc]/[q] Exit"
	if m.mode == modeAsk {
		left = " [Enter] Send | [1-3] Run Shortcut | [/help] Help"
		rightQuit = " [esc]/[q] Back"
	} else if m.mode == modeViewer {
		left = " [↑/↓] Scroll | [y] Copy | [g/G] Top/Bottom"
		rightQuit = " [esc]/[q] Back"
	} else if m.showHelp {
		left = " [/command] Navigation"
		rightQuit = " [esc]/[q] Back"
	}

	right := fmt.Sprintf("%s (v%s)", rightQuit, vStr)

	targetW := m.width
	if targetW <= 0 {
		targetW = 72
	}

	leftLen := len(left)
	rightLen := len([]rune(right))

	spacesCount := targetW - leftLen - rightLen - 2
	if spacesCount < 1 {
		right = rightQuit
		rightLen = len([]rune(right))
		spacesCount = targetW - leftLen - rightLen - 2
		if spacesCount < 1 {
			spacesCount = 2
			right = ""
		}
	}

	spaces := strings.Repeat(" ", spacesCount)
	footerText := fmt.Sprintf(" %s%s%s", left, spaces, right)
	return lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Render(footerText) + "\n"
}

// RunSelector starts the interactive AskISO Bubble Tea program.
func RunSelector(ctx context.Context, idx *catalog.Index) error {
	p := tea.NewProgram(NewModel(idx), tea.WithAltScreen(), tea.WithContext(ctx))
	_, err := p.Run()
	return err
}
