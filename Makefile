# SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
# SPDX-License-Identifier: Apache-2.0 OR MIT

# Versions increment by 0.0.1 and only by 0.0.1: v0.1.0 follows v0.0.999, not
# v0.0.9. The slow climb is the point — maturity is earned across releases
# rather than declared by a version number. See CONTRIBUTING.md.
#
# Defaults to the newest tag, which is what the Pages workflow resolves too,
# so a local build stamps the same version into the footer as the live site.
# CI passes VERSION explicitly when it builds a tag or a release.
VERSION ?= $(shell git describe --tags --abbrev=0 2>/dev/null | sed 's/^v//' || echo 0.0.0)
BINARY_NAME = askiso
CMD_PATH = ./cmd/askiso
MCP_BINARY = askiso-mcp
MCP_PATH = ./cmd/askiso-mcp
LSP_BINARY = askiso-lsp
LSP_PATH = ./cmd/askiso-lsp
LDFLAGS = -s -w -X github.com/sebastienrousseau/askiso/internal/tui.Version=$(VERSION)
SERVER_LDFLAGS = -s -w -X main.version=$(VERSION)
WASM_LDFLAGS = -s -w -X main.buildVersion=$(VERSION)
# Whole-repository statement coverage, measured without requiring a private
# catalogue. Keep this aligned with CI so local and hosted gates mean the same
# thing; raise it only alongside executable tests that protect useful behavior.
COVERAGE_FLOOR = 97.5
GOVULNCHECK_VERSION ?= v1.7.0
GOSEC_VERSION ?= v2.22.8

.PHONY: all build install test race property checkptr asan msan reliability cover conformance cbpr-local-conformance cbpr-strict-conformance differential fuzz ci fmt vet lint no-binaries readability a11y seo vuln clean run catalog-info web web-test web-interact a11y-axe banner-contrast reflow terminal-swap sitemap-check focus-order web-console web-serve wasm sessions sessions-record links mcp lsp mcp-check lsp-check servers

all: build

build: servers
	go build -ldflags "$(LDFLAGS)" -o $(BINARY_NAME) $(CMD_PATH)

# The two protocol servers are separate binaries because a client launches each
# and takes over its stdin and stdout; neither can share a process with the TUI.
servers: mcp lsp

mcp:
	go build -ldflags "$(SERVER_LDFLAGS)" -o $(MCP_BINARY) $(MCP_PATH)

lsp:
	go build -ldflags "$(SERVER_LDFLAGS)" -o $(LSP_BINARY) $(LSP_PATH)

# A handshake against the real binary: the protocol is easy to break in ways
# unit tests on the server object do not notice, such as writing to stdout.
mcp-check: mcp
	@printf '%s\n' \
	  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}' \
	  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
	  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
	  | ./$(MCP_BINARY) \
	  | grep -q '"askiso_translate"' \
	  && echo "mcp: handshake and tools/list ok" \
	  || { echo "mcp: handshake failed"; exit 1; }

# The LSP transport frames messages with headers rather than newlines, and a
# framing bug looks like the server hanging rather than failing. This drives the
# real binary the way an editor does.
lsp-check: lsp
	@body='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{}}}'; \
	exit_body='{"jsonrpc":"2.0","method":"exit"}'; \
	{ printf 'Content-Length: %d\r\n\r\n%s' $${#body} "$$body"; \
	  printf 'Content-Length: %d\r\n\r\n%s' $${#exit_body} "$$exit_body"; } \
	| ./$(LSP_BINARY) \
	| grep -q 'hoverProvider' \
	&& echo "lsp: handshake and capabilities ok" \
	|| { echo "lsp: handshake failed"; exit 1; }

install:
	go install -ldflags "$(LDFLAGS)" $(CMD_PATH)
	go install -ldflags "$(SERVER_LDFLAGS)" $(MCP_PATH)
	go install -ldflags "$(SERVER_LDFLAGS)" $(LSP_PATH)

test:
	go test ./...

race:
	go test -race -timeout 20m ./...

property:
	go test ./internal/lsp -run '^TestProtocolStateMachineMatchesOracle$$' -rapid.checks=10000
	go test ./internal/mcp -run '^TestProtocolStateMachineMatchesOracle$$' -rapid.checks=10000
	go test ./internal/cbprworkspace -run '^TestGenerationStateMachineMatchesOracle$$' -rapid.checks=250

checkptr:
	go test -gcflags=all=-d=checkptr=2 -timeout 20m ./...

# Go's sanitizers are mutually exclusive and ASan/MSan are Linux-only. Keep
# these separate from race so a green target proves which runtime ran.
asan:
	@test "$$(go env GOOS)" = linux || { echo "asan requires Linux"; exit 2; }
	CGO_ENABLED=1 go test -asan -timeout 20m ./...

msan:
	@test "$$(go env GOOS)" = linux || { echo "msan requires Linux with Clang"; exit 2; }
	CGO_ENABLED=1 CC=clang go test -msan -timeout 20m ./...

reliability: race property checkptr

# -coverpkg=./... credits code executed by any package's tests, not just its
# own, which is the honest measure for a project where the CLI drives the
# internals.
# The examples are demonstration programs: argument handling, a library call,
# and printing. They are proven by examples_test building and running every one
# of them, which is the guarantee that actually matters -- an example that no
# longer compiles is the failure worth catching. Statement coverage cannot see
# work done in a subprocess, so leaving them in the denominator would measure
# nothing except whether somebody wrote unit tests for demo code. They stay in
# `go test ./...`, `go vet ./...` and `gofmt`; only the metric excludes them.
COVERPKG = $(shell go list ./... | grep -v '/examples' | paste -sd, -)

# A compiled binary committed to a public repository is bloat nobody notices
# until the clone is slow, and `go build ./examples/...` drops one per example
# into the working directory where a wide `git add` will sweep them up. This
# caught exactly that, once.
#
# Written for POSIX sh, not bash: CI runs /bin/sh, and the first version of this
# used process substitution, failed to parse there, and still printed its
# success line -- a gate that passes without checking anything is worse than no
# gate at all.
# Prose on a standards site drifts towards the standard's own register, and the
# argument about whether it reads well is unwinnable without a number. The band
# is the one the site is written to: Flesch Reading Ease 55-58, Flesch-Kincaid
# grade 8-9. The generated message pages are reported rather than gated; the
# script explains why.
readability:
	@command -v python3 >/dev/null || { echo "python3 is required"; exit 1; }
	python3 scripts/readability.py $(WEB_OUT)

# The accessibility gate existed only in CI, so a regression was not visible
# until after a push. `make web` prints the plugin's summary, but a line of build
# output scrolling past is not a gate — this reads the report and fails on it,
# the same way the workflow does.
a11y:
	@test -f $(WEB_OUT)/accessibility-report.json || { \
	  echo "no accessibility report; run make web first"; exit 1; }
	@python3 -c "import json,sys; \
r=json.load(open('$(WEB_OUT)/accessibility-report.json')); \
n=r.get('total_issues',0); \
print(f\"WCAG {r.get('wcag_version')}: {r.get('pages_scanned')} page(s), {n} issue(s)\"); \
[print('  ', p['path'], i['criterion'], i['message']) for p in r.get('pages',[])[:10] for i in p.get('issues',[])[:2]]; \
sys.exit(1 if n else 0)"

# Search defects are easy to ship and hard to notice: a social image that was
# never built, a description the results truncate, two pages competing under one
# title. Every rule in the script was something actually wrong here once.
seo:
	@command -v python3 >/dev/null || { echo "python3 is required"; exit 1; }
	python3 scripts/seocheck.py $(WEB_OUT)

no-binaries:
	@tmp=$$(mktemp); \
	git ls-files -z | xargs -0 -n1 sh -c \
	  'case "$$(file -b --mime-type "$$1")" in application/x-mach-binary|application/x-executable|application/x-sharedlib) echo "$$1";; esac' sh \
	  > "$$tmp" 2>/dev/null || true; \
	if [ -s "$$tmp" ]; then \
	  echo "compiled binaries are tracked:"; sed 's/^/  /' "$$tmp"; \
	  echo "remove them with: git rm --cached <file>"; rm -f "$$tmp"; exit 1; \
	fi; \
	rm -f "$$tmp"; \
	echo "no-binaries: no compiled executables are tracked"

cover:
	go test -coverpkg=$(COVERPKG) -coverprofile=coverage.out -covermode=atomic ./...
	@go tool cover -func=coverage.out | tail -1
	@pct=$$(go tool cover -func=coverage.out | tail -1 | grep -oE '[0-9]+\.[0-9]+'); \
	awk -v p="$$pct" -v f="$(COVERAGE_FLOOR)" \
	  'BEGIN { if (p+0 < f+0) { printf "coverage %s%% is below floor %s%%\n", p, f; exit 1 } }'

# Schema conformance needs a catalogue and xmllint; both are absent on a clean
# CI runner, so these tests skip there. Run this locally before tagging.
# Differential agreement with libxml2 across every schema in the catalogue: the
# correctness bar for a validator written from scratch.
differential:
	@command -v xmllint >/dev/null || { echo "xmllint not found - install libxml2"; exit 1; }
	ASKISO_DIFF_LIMIT=0 go test ./internal/validator/ -run Differential -v -timeout 30m

# The parsers all take input nobody vetted: a schema the user downloaded, a
# message that arrived over a wire, an MT file from another bank's system. The
# structured validator target mutates semantic fields and checks an exact
# validity oracle plus tree/stream equivalence; converter fuzzing checks stable
# bidirectional conversion, while the malformed-input targets harden framing.
FUZZTIME ?= 60s
fuzz:
	go test ./internal/xsd/       -run '^$$' -fuzz FuzzParse     -fuzztime $(FUZZTIME)
	go test ./internal/xsd/       -run '^$$' -fuzz FuzzStructuredSchema -fuzztime $(FUZZTIME)
	go test ./internal/validator/ -run '^$$' -fuzz FuzzValidate  -fuzztime $(FUZZTIME)
	go test ./internal/validator/ -run '^$$' -fuzz FuzzStructuredValidation -fuzztime $(FUZZTIME)
	go test ./internal/swift/     -run '^$$' -fuzz FuzzParse     -fuzztime $(FUZZTIME)
	go test ./internal/swift/     -run '^$$' -fuzz FuzzStructuredMT103 -fuzztime $(FUZZTIME)
	go test ./internal/converter/ -run '^$$' -fuzz FuzzRoundTrip -fuzztime $(FUZZTIME)
	go test ./internal/converter/ -run '^$$' -fuzz FuzzStructuredRoundTrip -fuzztime $(FUZZTIME)
	go test ./internal/lsp/       -run '^$$' -fuzz FuzzConnFrameRoundTrip -fuzztime $(FUZZTIME)
	go test ./internal/rules/     -run '^$$' -fuzz FuzzCBPRPackMergeAlgebra -fuzztime $(FUZZTIME)
	go test ./internal/codes/     -run '^$$' -fuzz FuzzExternalJSONSemanticShapes -fuzztime $(FUZZTIME)
	go test ./internal/cbprworkspace/ -run '^$$' -fuzz FuzzWorkspaceMetadataRoundTrip -fuzztime $(FUZZTIME)

conformance:
	@command -v xmllint >/dev/null || { echo "xmllint not found - install libxml2"; exit 1; }
	@askiso_catalog=$${ASKISO_CATALOG:-$$HOME/Library/Application Support/askiso/catalog}; \
	test -d "$$askiso_catalog" || { echo "no catalogue at $$askiso_catalog"; echo "set ASKISO_CATALOG or run: askiso catalog add <zip>"; exit 1; }; \
	echo "Catalogue: $$askiso_catalog"; \
	ASKISO_CATALOG="$$askiso_catalog" go test ./internal/generator/ -run 'Schema|Linter|BAH|RoundTrip' -v; \
	ASKISO_CATALOG="$$askiso_catalog" go test ./internal/swift/ -run 'ConvertedMessagesValidate' -v; \
	ASKISO_CATALOG="$$askiso_catalog" ASKISO_GEN_LIMIT=0 go test ./internal/schemagen/ -run 'Installed|LintClean' -v -timeout 20m; \
	ASKISO_CATALOG="$$askiso_catalog" go test ./internal/validator/ -run 'StreamingAgrees' -v

# Run against an operator-owned MyStandards export without retaining source
# artefacts in the repository. The generated workspace is deliberately placed
# in a temporary directory and can be inspected or removed by the operator.
CBPR_SOURCE ?=
CBPR_EXTERNAL_CODES ?=
CBPR_EVIDENCE ?=
cbpr-local-conformance:
	@test -n "$(CBPR_SOURCE)" || { echo "set CBPR_SOURCE to the private export directory"; exit 1; }
	@ws=$$(mktemp -d "$${TMPDIR:-/tmp}/askiso-cbpr-local.XXXXXX"); \
	trap 'echo "workspace: $$ws"' EXIT; \
	if test -n "$(CBPR_EXTERNAL_CODES)"; then \
	 go run ./cmd/askiso cbpr-pack import "$(CBPR_SOURCE)" --workspace "$$ws" --external-codes "$(CBPR_EXTERNAL_CODES)" --acknowledge-entitlement --generate-samples >/dev/null; \
	else \
	 go run ./cmd/askiso cbpr-pack import "$(CBPR_SOURCE)" --workspace "$$ws" --acknowledge-entitlement --generate-samples >/dev/null; \
	fi; \
	go run ./cmd/askiso cbpr-pack verify "$(CBPR_SOURCE)" --workspace "$$ws"

cbpr-strict-conformance:
	@test -n "$(CBPR_SOURCE)" || { echo "set CBPR_SOURCE to the private export directory"; exit 1; }
	@test -n "$(CBPR_WORKSPACE)" || { echo "set CBPR_WORKSPACE to the imported private workspace"; exit 1; }
	@test -n "$(CBPR_EVIDENCE)" || { echo "set CBPR_EVIDENCE to the content-free external evidence JSON"; exit 1; }
	go run ./cmd/askiso cbpr-pack conformance "$(CBPR_SOURCE)" --workspace "$(CBPR_WORKSPACE)" --evidence "$(CBPR_EVIDENCE)" --require-user-samples --require-external-evidence

# The terminal sessions on the website are executable. Every ```console block
# whose commands are `askiso` is replayed against testdata/sessions and its
# recorded output compared with what the binary actually writes, so the site
# cannot go on showing output the tool stopped producing.
links:
	@test -d $(WEB_OUT) || { echo "build the site first: make web"; exit 1; }
	python3 scripts/linkcheck.py $(WEB_OUT)

sessions:
	go run ./scripts/sessions

sessions-record:
	go run ./scripts/sessions -record

# --- website ---------------------------------------------------------------
# askiso.io is content built by ssg plus pkg/iso20022 compiled to WebAssembly,
# so the browser runs exactly the same engine as the CLI. It ships no schemas:
# light mode only.
#
# `wasm` is the bundle alone — the smoke test needs only that, and building it
# without ssg keeps `make ci` free of a Rust dependency.
WEB_OUT = web/public

wasm:
	@mkdir -p $(WEB_OUT)
	GOOS=js GOARCH=wasm go build -ldflags "$(WASM_LDFLAGS)" -o $(WEB_OUT)/askiso.wasm ./web/wasm
	@# -f matters: the source lives in the read-only module cache, so the copy
	@# it leaves behind is read-only too and a second build cannot overwrite it.
	@cp -f "$$(go env GOROOT)/lib/wasm/wasm_exec.js" $(WEB_OUT)/ 2>/dev/null || \
	 cp -f "$$(go env GOROOT)/misc/wasm/wasm_exec.js" $(WEB_OUT)/ 2>/dev/null || \
	 { echo "could not find wasm_exec.js in GOROOT"; exit 1; }
	@chmod u+w $(WEB_OUT)/wasm_exec.js
	@printf 'wasm: %s (%s gzipped)\n' \
	  "$$(du -h $(WEB_OUT)/askiso.wasm | cut -f1)" \
	  "$$(gzip -9 -c $(WEB_OUT)/askiso.wasm | wc -c | awk '{printf "%.1fM", $$1/1048576}')"

# ssg does not copy its template-directory assets into the output — the theme
# suite's own build script copies them explicitly, and so must this one.
#
# ssg runs first and the WebAssembly bundle is built into the result afterwards:
# the content build clears its output directory, so a bundle written before it
# is deleted rather than published.
web:
	@command -v ssg >/dev/null || { echo "ssg is required: cargo install ssg"; exit 1; }
	@# Always from empty. ssg keeps a plugin cache in its output directory and
	@# an incremental run skips the agentic-discovery files, so a local rebuild
	@# would otherwise produce a different site from CI, which always starts on
	@# a fresh checkout. Reproducibility is worth the second of build time.
	@rm -rf $(WEB_OUT)
	@# One page per message definition, generated from the embedded registry.
	@# They are derived data, so they are not tracked — regenerating is a
	@# second of work and a stale copy in the tree would be worse than none.
	@# An unclosed code fence does not fail the build: the renderer simply keeps
	@# swallowing the page into one <pre>, and the rest of the content silently
	@# disappears. That happened once, to two pages, and was noticed only because
	@# a word count looked wrong.
	@bad=$$(for f in web/content/*.md; do \
	  n=$$(grep -c '^```' "$$f"); \
	  [ $$((n % 2)) -eq 0 ] || echo "$$f ($$n fences)"; \
	done); \
	if [ -n "$$bad" ]; then \
	  echo "unclosed code fence:"; echo "$$bad" | sed 's/^/  /'; exit 1; \
	fi
	go run ./scripts/gen-message-pages -out web/content/messages
	ssg build -f web/ssg.toml
	@$(MAKE) --no-print-directory wasm
	@# The stylesheet the pages actually load: both sources, in order, minified.
	@python3 scripts/minify-css.py $(WEB_OUT)/site.css \
	  web/_layouts/styles.css web/_layouts/brand.css
	@python3 scripts/minify-css.py $(WEB_OUT)/playground.css web/_layouts/playground.css
	@python3 scripts/minify-css.py $(WEB_OUT)/workspace.css web/_layouts/workspace.css
	@for a in main.js theme-init.js logo.svg favicon.ico; do \
	  test -f "web/_layouts/$$a" && cp -f "web/_layouts/$$a" "$(WEB_OUT)/$$a"; \
	done
	@# The site's own scripts, comments stripped. terminal.js is the one the
	@# pages load without `defer` — deferring it measured a layout shift of
	@# 0.064 — so it is on the critical path, where two thirds of it was prose.
	@# main.js and the generator's own _csp bundle are left alone: both are
	@# referenced with an integrity hash computed from the bytes as they are.
	@for a in playground.js catalogue.js evidence.js workspace.js workspace-boot.js terminal.js; do \
	  test -f "web/_layouts/$$a" && python3 scripts/minify-js.py "web/_layouts/$$a" "$(WEB_OUT)/$$a"; \
	done
	@# Independently of what produced them, everything emitted has to parse.
	@if command -v node >/dev/null 2>&1; then \
	  for a in $(WEB_OUT)/*.js; do \
	    node --check "$$a" >/dev/null 2>&1 || { echo "web: $$a does not parse"; exit 1; }; \
	  done; \
	fi
	@# The generator leaves theme_color null, which browsers reject with a
	@# console error on every page load.
	@python3 scripts/fix-manifest.py $(WEB_OUT)/manifest.json
	@# A fenced block scrolls, which makes it a scroll region, which WCAG 2.1.1
	@# requires to be reachable from a keyboard.
	@python3 scripts/focusable-code.py $(WEB_OUT)
	@# The theme bootstrap, inlined and allowed by hash. It has to run before
	@# the first paint, so it cannot be deferred; external it was a round trip
	@# for 711 bytes.
	@python3 scripts/inline-theme.py $(WEB_OUT) web/_layouts/theme-init.js
	@# Question-and-answer markup, read back out of the built page so it cannot
	@# disagree with the visible text.
	@python3 scripts/faq-schema.py $(WEB_OUT)/faq/index.html
	@# The same treatment for the message pages. Each asks and answers what
	@# somebody searching a message identifier actually wants to know, and those
	@# answers are what an assistant lifts; unmarked they were only prose.
	@python3 scripts/faq-schema.py $(WEB_OUT)/messages
	@# A news piece has to say what it is, who wrote it and what it reports on,
	@# or an assistant answering the question it answers has nothing to cite.
	@python3 scripts/article-schema.py $(WEB_OUT)
	@python3 scripts/gen-news-sitemap.py web/content/news $(WEB_OUT)/news-sitemap.xml
	@# The social card. og:image pointed at images/screenshot.png, which was
	@# never built, so every share of every page showed a broken image.
	@mkdir -p $(WEB_OUT)/images/banners
	@cp -f web/_layouts/images/social-card.png $(WEB_OUT)/images/
	@# Banner photography, served from this origin. It began on the project's
	@# CDN, which cost a second DNS lookup and TLS handshake before the largest
	@# element on the page could even begin downloading.
	@cp -f web/_layouts/images/banners/*.webp $(WEB_OUT)/images/banners/
	@# ssg fingerprints its syntax-highlighting stylesheet but emits the page
	@# referencing the bare name, so /highlight.css was a 404 on every page.
	@h=$$(ls $(WEB_OUT)/highlight.*.css 2>/dev/null | head -1); \
	 test -n "$$h" && cp -f "$$h" "$(WEB_OUT)/highlight.css" || true
	@# Stamp the release the site reflects. In CI VERSION comes from the tag
	@# being built, so publishing a release republishes a site that names it.
	@find $(WEB_OUT) -name '*.html' -exec sed -i.bak 's/ASKISO_RELEASE/v$(VERSION)/g' {} + 2>/dev/null || \
	 find $(WEB_OUT) -name '*.html' -exec sed -i '' 's/ASKISO_RELEASE/v$(VERSION)/g' {} +
	@find $(WEB_OUT) -name '*.html.bak' -delete 2>/dev/null || true
	@# Social descriptions read the meta description rather than the first 160
	@# characters of the body, plus breadcrumbs and a SoftwareApplication node.
	@# After the release stamp, so the software markup can name the version.
	@python3 scripts/seo-enrich.py $(WEB_OUT)
	@printf 'askiso.io\n' > $(WEB_OUT)/CNAME
	@# Without this GitHub Pages runs its Jekyll filter over the artefact and
	@# drops anything beginning with a dot or an underscore — which silently
	@# removes /.well-known/mcp.json, the file that tells an assistant AskISO
	@# has an MCP server it can use.
	@touch $(WEB_OUT)/.nojekyll
	@# GitHub Pages will not serve a dot directory even with .nojekyll present:
	@# the files are provably in the uploaded artefact and /.well-known/mcp.json
	@# still returns 404. Publish the same manifests at the site root so they
	@# are reachable at all, and point agents.txt at those copies. The canonical
	@# paths stay in place for the day the site moves to a host that serves them.
	@test -f $(WEB_OUT)/.well-known/mcp.json && cp -f $(WEB_OUT)/.well-known/mcp.json $(WEB_OUT)/mcp.json || true
	@test -f $(WEB_OUT)/.well-known/ai-plugin.json && cp -f $(WEB_OUT)/.well-known/ai-plugin.json $(WEB_OUT)/ai-plugin.json || true
	@# ssg writes a copy of the site-level files into every page directory. With
	@# one page that is invisible; with 2,845 it is 110 MB of duplicated
	@# sitemaps, and every one of them is a wrong URL set for that subdirectory
	@# anyway. Keep the copies at the root and drop the rest.
	@for f in sitemap.xml news-sitemap.xml rss.xml robots.txt manifest.json; do \
	  find $(WEB_OUT) -mindepth 2 -name "$$f" -delete; \
	done
	@# Build bookkeeping, not site content: front matter ssg already rendered
	@# into the pages, plus its incremental caches. Publishing it serves nobody
	@# and adds 12 MB to the artefact.
	@rm -rf $(WEB_OUT)/.meta $(WEB_OUT)/.ssg-cache $(WEB_OUT)/.ssg-plugin-cache.json
	@python3 scripts/gen-sitemap.py $(WEB_OUT)
	@# Two pages answer something that already happened rather than being
	@# destinations: the 404, and the confirmation after the contact form.
	@# Marks both noindex and takes them out of the sitemap just rebuilt above.
	@python3 scripts/noindex.py $(WEB_OUT)
	@# GitHub Pages serves /404.html for any address it cannot match; without
	@# one it serves its own, branded as GitHub and with no way back into this
	@# site. After the sitemap, which is rebuilt above and would otherwise list
	@# the page again, and after the release stamping, so the copy carries it.
	@python3 scripts/notfound.py $(WEB_OUT)
	@printf 'site: %s page(s), %s\n' \
	  "$$(find $(WEB_OUT) -name '*.html' | wc -l | xargs)" \
	  "$$(du -sh $(WEB_OUT) | cut -f1)"

web-test: wasm
	@command -v node >/dev/null || { echo "node is required for the wasm smoke test"; exit 1; }
	node web/wasm/smoke_test.mjs

# Drives the two interactive pages the way a visitor does. The engine now loads
# on first interaction, and an action taken while it is still arriving has to be
# queued and replayed -- a failure mode no static check can see, and one that
# shipped once. Skips rather than fails without puppeteer-core and a Chrome, so
# a contributor who has neither is not blocked.
web-interact: web
	@command -v node >/dev/null || { echo "node is required"; exit 1; }
	@chrome="$${CHROME_PATH:-/Applications/Google Chrome.app/Contents/MacOS/Google Chrome}"; \
	 if [ ! -x "$$chrome" ]; then chrome="$$(command -v google-chrome-stable || command -v google-chrome || true)"; fi; \
	 if [ -z "$$chrome" ] || [ ! -x "$$chrome" ]; then \
	   if [ -n "$$ASKISO_REQUIRE_BROWSER" ]; then echo "web-interact: no Chrome found — refusing to skip because ASKISO_REQUIRE_BROWSER is set"; exit 1; fi; echo "web-interact: no Chrome found, skipping"; exit 0; \
	 fi; \
	 node -e "import('puppeteer-core')" >/dev/null 2>&1 || { \
	   echo "web-interact: puppeteer-core is not installed, skipping"; \
	   echo "  install it with: npm install --no-save puppeteer-core"; exit 0; \
	 }; \
	 (cd $(WEB_OUT) && python3 -m http.server 8899 >/dev/null 2>&1 & echo $$! > /tmp/askiso-interact.pid); \
	 sleep 2; \
	 CHROME_PATH="$$chrome" ASKISO_BASE_URL=http://127.0.0.1:8899 node web/tests/interact.mjs; \
	 status=$$?; \
	 kill "$$(cat /tmp/askiso-interact.pid)" 2>/dev/null; rm -f /tmp/askiso-interact.pid; \
	 exit $$status

# WCAG 2.2 AA in a real browser, with axe-core. The generator's checker reads
# the markup; this renders the page, which is the only way to see a contrast
# failure, and it runs both themes because the palettes are independent.
a11y-axe: web
	@command -v node >/dev/null || { echo "node is required"; exit 1; }
	@chrome="$${CHROME_PATH:-/Applications/Google Chrome.app/Contents/MacOS/Google Chrome}"; \
	 if [ ! -x "$$chrome" ]; then chrome="$$(command -v google-chrome-stable || command -v google-chrome || true)"; fi; \
	 if [ -z "$$chrome" ] || [ ! -x "$$chrome" ]; then \
	   echo "a11y-axe: no Chrome found, skipping"; exit 0; \
	 fi; \
	 node -e "import('puppeteer-core')" >/dev/null 2>&1 || { \
	   echo "a11y-axe: puppeteer-core is not installed, skipping"; exit 0; }; \
	 test -f node_modules/axe-core/axe.min.js || { \
	   echo "a11y-axe: axe-core is not installed, skipping"; \
	   echo "  install both with: npm install --no-save puppeteer-core axe-core"; exit 0; }; \
	 (cd $(WEB_OUT) && python3 -m http.server 8899 >/dev/null 2>&1 & echo $$! > /tmp/askiso-axe.pid); \
	 sleep 2; \
	 CHROME_PATH="$$chrome" ASKISO_BASE_URL=http://127.0.0.1:8899 node web/tests/a11y.mjs; \
	 status=$$?; \
	 kill "$$(cat /tmp/askiso-axe.pid)" 2>/dev/null; rm -f /tmp/askiso-axe.pid; \
	 exit $$status

# WCAG 2.4.3: what is painted first must not be reached last. No automated
# checker reports this one, because focus order is not decidable from markup.
focus-order: web
	@command -v node >/dev/null || { echo "node is required"; exit 1; }
	@chrome="$${CHROME_PATH:-/Applications/Google Chrome.app/Contents/MacOS/Google Chrome}"; \
	 if [ ! -x "$$chrome" ]; then chrome="$$(command -v google-chrome-stable || command -v google-chrome || true)"; fi; \
	 if [ -z "$$chrome" ] || [ ! -x "$$chrome" ]; then \
	   if [ -n "$$ASKISO_REQUIRE_BROWSER" ]; then echo "focus-order: no Chrome found — refusing to skip because ASKISO_REQUIRE_BROWSER is set"; exit 1; fi; echo "focus-order: no Chrome found, skipping"; exit 0; \
	 fi; \
	 node -e "import('puppeteer-core')" >/dev/null 2>&1 || { \
	   if [ -n "$$ASKISO_REQUIRE_BROWSER" ]; then echo "focus-order: puppeteer-core is not installed — refusing to skip because ASKISO_REQUIRE_BROWSER is set"; exit 1; fi; echo "focus-order: puppeteer-core is not installed, skipping"; exit 0; }; \
	 (cd $(WEB_OUT) && python3 -m http.server 8899 >/dev/null 2>&1 & echo $$! > /tmp/askiso-focus.pid); \
	 sleep 2; \
	 CHROME_PATH="$$chrome" ASKISO_BASE_URL=http://127.0.0.1:8899 node web/tests/focus-order.mjs; \
	 status=$$?; \
	 kill "$$(cat /tmp/askiso-focus.pid)" 2>/dev/null; rm -f /tmp/askiso-focus.pid; \
	 exit $$status

# Every page that should be in the sitemap is, and nothing else is.
sitemap-check: web
	@python3 scripts/check-sitemap.py $(WEB_OUT)

# The contrast axe-core will not rule on. Where a banner sets text over a
# photograph behind a gradient scrim, axe cannot resolve the background and
# defers; this measures it from the rendered pixels instead.
banner-contrast: web
	@command -v node >/dev/null || { echo "node is required"; exit 1; }
	@chrome="$${CHROME_PATH:-/Applications/Google Chrome.app/Contents/MacOS/Google Chrome}"; \
	 if [ ! -x "$$chrome" ]; then chrome="$$(command -v google-chrome-stable || command -v google-chrome || true)"; fi; \
	 if [ -z "$$chrome" ] || [ ! -x "$$chrome" ]; then \
	   if [ -n "$$ASKISO_REQUIRE_BROWSER" ]; then echo "banner-contrast: no Chrome found — refusing to skip because ASKISO_REQUIRE_BROWSER is set"; exit 1; fi; echo "banner-contrast: no Chrome found, skipping"; exit 0; \
	 fi; \
	 node -e "import('puppeteer-core')" >/dev/null 2>&1 || { \
	   if [ -n "$$ASKISO_REQUIRE_BROWSER" ]; then echo "banner-contrast: puppeteer-core is not installed — refusing to skip because ASKISO_REQUIRE_BROWSER is set"; exit 1; fi; echo "banner-contrast: puppeteer-core is not installed, skipping"; exit 0; }; \
	 (cd $(WEB_OUT) && python3 -m http.server 8899 >/dev/null 2>&1 & echo $$! > /tmp/askiso-banner.pid); \
	 sleep 2; \
	 CHROME_PATH="$$chrome" ASKISO_BASE_URL=http://127.0.0.1:8899 node web/tests/banner-contrast.mjs; \
	 status=$$?; \
	 kill "$$(cat /tmp/askiso-banner.pid)" 2>/dev/null; rm -f /tmp/askiso-banner.pid; \
	 exit $$status

# The shell block and the terminal it becomes must have the same box, or every
# paragraph below them moves when the script runs.
terminal-swap: web
	@command -v node >/dev/null || { echo "node is required"; exit 1; }
	@chrome="$${CHROME_PATH:-/Applications/Google Chrome.app/Contents/MacOS/Google Chrome}"; \
	 if [ ! -x "$$chrome" ]; then chrome="$$(command -v google-chrome-stable || command -v google-chrome || true)"; fi; \
	 if [ -z "$$chrome" ] || [ ! -x "$$chrome" ]; then \
	   if [ -n "$$ASKISO_REQUIRE_BROWSER" ]; then echo "terminal-swap: no Chrome found — refusing to skip because ASKISO_REQUIRE_BROWSER is set"; exit 1; fi; echo "terminal-swap: no Chrome found, skipping"; exit 0; \
	 fi; \
	 node -e "import('puppeteer-core')" >/dev/null 2>&1 || { \
	   if [ -n "$$ASKISO_REQUIRE_BROWSER" ]; then echo "terminal-swap: puppeteer-core is not installed — refusing to skip because ASKISO_REQUIRE_BROWSER is set"; exit 1; fi; echo "terminal-swap: puppeteer-core is not installed, skipping"; exit 0; }; \
	 (cd $(WEB_OUT) && python3 -m http.server 8899 >/dev/null 2>&1 & echo $$! > /tmp/askiso-swap.pid); \
	 sleep 2; \
	 CHROME_PATH="$$chrome" ASKISO_BASE_URL=http://127.0.0.1:8899 node web/tests/terminal-swap.mjs; \
	 status=$$?; \
	 kill "$$(cat /tmp/askiso-swap.pid)" 2>/dev/null; rm -f /tmp/askiso-swap.pid; \
	 exit $$status

# WCAG 1.4.10: 1280 CSS pixels at 400% zoom is 320, and nothing may scroll
# sideways there. The axe suite only ever renders at 1280, where reflow cannot
# fail, so this is the width that catches it.
reflow: web
	@command -v node >/dev/null || { echo "node is required"; exit 1; }
	@chrome="$${CHROME_PATH:-/Applications/Google Chrome.app/Contents/MacOS/Google Chrome}"; \
	 if [ ! -x "$$chrome" ]; then chrome="$$(command -v google-chrome-stable || command -v google-chrome || true)"; fi; \
	 if [ -z "$$chrome" ] || [ ! -x "$$chrome" ]; then \
	   if [ -n "$$ASKISO_REQUIRE_BROWSER" ]; then echo "reflow: no Chrome found — refusing to skip because ASKISO_REQUIRE_BROWSER is set"; exit 1; fi; echo "reflow: no Chrome found, skipping"; exit 0; \
	 fi; \
	 node -e "import('puppeteer-core')" >/dev/null 2>&1 || { \
	   if [ -n "$$ASKISO_REQUIRE_BROWSER" ]; then echo "reflow: puppeteer-core is not installed — refusing to skip because ASKISO_REQUIRE_BROWSER is set"; exit 1; fi; echo "reflow: puppeteer-core is not installed, skipping"; exit 0; }; \
	 (cd $(WEB_OUT) && python3 -m http.server 8899 >/dev/null 2>&1 & echo $$! > /tmp/askiso-reflow.pid); \
	 sleep 2; \
	 CHROME_PATH="$$chrome" ASKISO_BASE_URL=http://127.0.0.1:8899 node web/tests/reflow.mjs; \
	 status=$$?; \
	 kill "$$(cat /tmp/askiso-reflow.pid)" 2>/dev/null; rm -f /tmp/askiso-reflow.pid; \
	 exit $$status

# Loads every page and fails on anything the browser complains about: errors,
# warnings, failed requests, and any response of 400 or above.
web-console: web
	@command -v node >/dev/null || { echo "node is required"; exit 1; }
	@chrome="$${CHROME_PATH:-/Applications/Google Chrome.app/Contents/MacOS/Google Chrome}"; \
	 if [ ! -x "$$chrome" ]; then chrome="$$(command -v google-chrome-stable || command -v google-chrome || true)"; fi; \
	 if [ -z "$$chrome" ] || [ ! -x "$$chrome" ]; then \
	   if [ -n "$$ASKISO_REQUIRE_BROWSER" ]; then echo "web-console: no Chrome found — refusing to skip because ASKISO_REQUIRE_BROWSER is set"; exit 1; fi; echo "web-console: no Chrome found, skipping"; exit 0; \
	 fi; \
	 node -e "import('puppeteer-core')" >/dev/null 2>&1 || { \
	   if [ -n "$$ASKISO_REQUIRE_BROWSER" ]; then echo "web-console: puppeteer-core is not installed — refusing to skip because ASKISO_REQUIRE_BROWSER is set"; exit 1; fi; echo "web-console: puppeteer-core is not installed, skipping"; exit 0; }; \
	 (cd $(WEB_OUT) && python3 -m http.server 8899 >/dev/null 2>&1 & echo $$! > /tmp/askiso-console.pid); \
	 sleep 2; \
	 CHROME_PATH="$$chrome" ASKISO_BASE_URL=http://127.0.0.1:8899 node web/tests/console.mjs; \
	 status=$$?; \
	 kill "$$(cat /tmp/askiso-console.pid)" 2>/dev/null; rm -f /tmp/askiso-console.pid; \
	 exit $$status

web-serve: web
	@echo "http://127.0.0.1:8765"
	@cd $(WEB_OUT) && python3 -m http.server 8765

fmt:
	gofmt -s -w cmd/ examples/ internal/ pkg/ scripts/ web/

vet:
	go vet ./...

lint:
	@command -v golangci-lint >/dev/null || { echo "golangci-lint not installed"; exit 1; }
	golangci-lint run --max-issues-per-linter 0 --max-same-issues 0

vuln:
	@command -v govulncheck >/dev/null || go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	govulncheck ./...

# The full gate CI runs, minus the catalogue-dependent conformance suite.
ci: fmt vet lint no-binaries test cover vuln build sessions web-test mcp-check lsp-check

catalog-info:
	@./$(BINARY_NAME) doctor || true

clean:
	rm -f $(BINARY_NAME) $(MCP_BINARY) $(LSP_BINARY) coverage.out coverage.html
	rm -rf $(WEB_OUT)
