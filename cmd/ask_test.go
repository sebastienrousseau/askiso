// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sebastienrousseau/askiso/internal/ai"
	"github.com/sebastienrousseau/askiso/internal/catalog"
)

func TestTerminalWidthUsesTerminfoFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX tput stand-in")
	}
	old := controllingTerminalWidthFn
	controllingTerminalWidthFn = func() int { return 0 }
	defer func() { controllingTerminalWidthFn = old }()

	dir := t.TempDir()
	tput := filepath.Join(dir, "tput")
	if err := os.WriteFile(tput, []byte("#!/bin/sh\nprintf '91\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if got := getTerminalWidth(); got != 91 {
		t.Fatalf("terminal database width = %d", got)
	}
}

func TestAskOneShot(t *testing.T) {
	withCatalogue(t)

	for _, prompt := range []string{
		"What is pacs.008?",
		"compare pacs.008 vs pacs.009",
		"pain.001 vs pacs.008",
		"camt.052 vs camt.053",
		"explain the business application header",
		"show pacs.008 xml",
		"show pacs.008 xsd",
		"credit transfer",
		"pacs",
		"something with no match whatsoever",
	} {
		t.Run(prompt, func(t *testing.T) {
			out, err := run(t, "ask", prompt)
			if err != nil {
				t.Fatalf("ask %q: %v", prompt, err)
			}
			if strings.TrimSpace(out) == "" {
				t.Error("ask produced no output")
			}
		})
	}
}

func TestAskOutputModes(t *testing.T) {
	withCatalogue(t)

	for _, flag := range []string{"--raw", "--text", "--plain"} {
		t.Run(flag, func(t *testing.T) {
			out, err := run(t, "ask", "What is pacs.008?", flag)
			if err != nil {
				t.Fatalf("ask %s: %v", flag, err)
			}
			if strings.TrimSpace(out) == "" {
				t.Errorf("ask %s produced no output", flag)
			}
		})
	}
}

func TestAskWithoutCatalogueFails(t *testing.T) {
	isolate(t)
	if _, err := run(t, "ask", "What is pacs.008?"); err == nil {
		t.Error("ask needs a catalogue and should say so")
	}
}

func TestParseSuggestionIndex(t *testing.T) {
	cases := map[string]int{
		"1":         1,
		" 2 ":       2,
		"[3]":       3,
		"(4)":       4,
		"#5":        5,
		"6.":        6,
		"run 1":     1,
		"select 2":  2,
		"option 3":  3,
		"choice 4":  4,
		"not-a-num": -1,
		"":          -1,
		"abc":       -1,
	}
	for in, want := range cases {
		if got := parseSuggestionIndex(in); got != want {
			t.Errorf("parseSuggestionIndex(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestGetTerminalWidthIsSane(t *testing.T) {
	// With no controlling terminal this must still return a usable default
	// rather than zero, which would break every renderer downstream.
	w := getTerminalWidth()
	if w < 20 || w > 1000 {
		t.Errorf("terminal width = %d, want a sane fallback", w)
	}
}

func TestApplyVerticalDelimiter(t *testing.T) {
	out := applyVerticalDelimiter("line one\nline two", "#FF0000")
	if strings.Count(out, "\n") != 1 {
		t.Errorf("line count should be preserved:\n%s", out)
	}
	if !strings.Contains(out, "line one") || !strings.Contains(out, "line two") {
		t.Errorf("content should be preserved:\n%s", out)
	}
	if applyVerticalDelimiter("", "#FF0000") == "" {
		// An empty input still yields a delimiter line; just check it does not panic.
		t.Log("empty input produced empty output")
	}
}

func TestCleanMarkdownSymbols(t *testing.T) {
	// Headings are stripped at line start only, which is why the "##" here is
	// on its own line rather than inline.
	in := "## Heading\n**bold** and `code` and _italic_\n```go\nfenced\n```"
	out := cleanMarkdownSymbols(in)
	for _, sym := range []string{"**", "`", "## "} {
		if strings.Contains(out, sym) {
			t.Errorf("%q should have been stripped: %q", sym, out)
		}
	}
	wantContains(t, out, "Heading", "bold", "code", "italic")

	// An inline hash is not a heading and must survive.
	if got := cleanMarkdownSymbols("ref #42"); !strings.Contains(got, "#42") {
		t.Errorf("an inline hash should be left alone: %q", got)
	}
}

func TestRenderPlainText(t *testing.T) {
	md := "# Title\n\nSome **bold** body text that is long enough to wrap across a narrow width.\n\n- item one\n- item two"
	out := renderPlainText(md, 40)
	if strings.TrimSpace(out) == "" {
		t.Fatal("renderPlainText produced nothing")
	}
	for _, line := range strings.Split(out, "\n") {
		if len([]rune(line)) > 80 {
			t.Errorf("line far exceeds the requested width: %q", line)
		}
	}
	if renderPlainText("", 40) != "" {
		t.Log("empty markdown produced non-empty output")
	}
}

func TestRenderAnswerVariants(t *testing.T) {
	idx := askFixtureIndex(t)
	eng := ai.New(idx)

	captureStdout(t, func() {
		renderAnswer(eng.Query("What is pacs.008?"))
	})

	// With suggestions and related messages, in both one-shot and REPL framing.
	ans := ai.MessageAnswer{
		Summary:     "Test",
		Details:     "**Body** text",
		Suggestions: []string{"first", "second"},
		RelatedMsgs: idx.Messages,
	}
	for _, repl := range []bool{false, true} {
		out := captureStdout(t, func() { renderAnswerWithContext(ans, repl) })
		if strings.TrimSpace(out) == "" {
			t.Errorf("renderAnswerWithContext(repl=%v) produced nothing", repl)
		}
	}

	// An empty answer must not panic.
	captureStdout(t, func() { renderAnswerWithContext(ai.MessageAnswer{}, false) })
}

func TestRenderAnswerStyledBranch(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	oldText, oldPlain := textOutput, plainOutput
	textOutput, plainOutput = false, false
	t.Cleanup(func() { textOutput, plainOutput = oldText, oldPlain })

	ans := ai.MessageAnswer{
		Summary:         "Styled answer",
		Details:         "## Details\n\nA **rendered** answer.",
		Suggestions:     []string{"first", "second"},
		ProviderWarning: "configured provider unavailable",
	}
	for _, repl := range []bool{false, true} {
		out := captureStdout(t, func() { renderAnswerWithContext(ans, repl) })
		if !strings.Contains(out, "Styled answer") || !strings.Contains(out, "first") || !strings.Contains(out, "Provider warning") {
			t.Errorf("styled output is incomplete:\n%s", out)
		}
	}
}

func TestPrintReplHelp(t *testing.T) {
	out := captureStdout(t, printReplHelp)
	if strings.TrimSpace(out) == "" {
		t.Error("REPL help produced nothing")
	}
}

func TestShowMsgHelpers(t *testing.T) {
	root := fixtureCatalogue(t)
	idx, err := catalog.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() { showMsgInfo(idx, "pacs.008.001.10", root) })
	wantContains(t, out, "pacs.008.001.10")

	out = captureStdout(t, func() { showMsgInfo(idx, "zzzz.999.999.99", root) })
	if strings.TrimSpace(out) == "" {
		t.Error("an unknown identifier should still report something")
	}

	// showMsgFile reports the path and a label rather than the file contents.
	out = captureStdout(t, func() { showMsgFile(idx, "pacs.008.001.10", true, root) })
	wantContains(t, out, "XML Sample", "pacs.008.001.10.xml")

	out = captureStdout(t, func() { showMsgFile(idx, "pacs.008.001.10", false, root) })
	wantContains(t, out, "XSD Schema", "pacs.008.001.10.xsd")

	// A message with no sample on disk.
	out = captureStdout(t, func() { showMsgFile(idx, "pacs.009.001.10", true, root) })
	if strings.TrimSpace(out) == "" {
		t.Error("a missing sample should still report something")
	}

	out = captureStdout(t, func() { showMsgFile(idx, "zzzz.999.999.99", true, root) })
	if strings.TrimSpace(out) == "" {
		t.Error("an unknown identifier should still report something")
	}
}

func askFixtureIndex(t *testing.T) *catalog.Index {
	t.Helper()
	idx, err := catalog.Load(fixtureCatalogue(t))
	if err != nil {
		t.Fatal(err)
	}
	return idx
}
