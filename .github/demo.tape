# SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
# SPDX-License-Identifier: Apache-2.0 OR MIT
#
# The README demo (AGENTS.md §7.1.1), rendered by `make demo` with VHS
# (https://github.com/charmbracelet/vhs). It runs the real askiso binary,
# built by the target into DEMO_BIN, against the fixtures in
# testdata/sessions. Nothing here needs a catalogue or the network: search
# reads the embedded index, and lint and translate need no schemas.

Output .github/demo.gif

Set Shell "bash"
Set FontSize 16
Set Width 1200
Set Height 720
Set Padding 20
Set TypingSpeed 40ms
Set Theme "Catppuccin Mocha"

Hide
Type `export PATH="$DEMO_BIN:$PATH" PS1='$ '; cd testdata/sessions; clear`
Enter
Show

Type "askiso search pacs.008 | head -12"
Sleep 300ms
Enter
Sleep 3s

Type "clear; askiso lint payment.xml --profile cbpr-2026"
Sleep 300ms
Enter
Sleep 5s

Type "clear; askiso translate payment.mt103 --report | tail -20"
Sleep 300ms
Enter
Sleep 5s
