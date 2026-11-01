<!-- SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com> -->
<!-- SPDX-License-Identifier: Apache-2.0 OR MIT -->

# AskISO architecture

This is how AskISO is put together, for someone about to change it. What
each command does is in the [README](../README.md); the reasons behind each
package are in its package comment, which `go doc` prints.

## One engine, four front ends

AskISO answers questions about ISO 20022 messages. The answers come from one
set of Go packages under `internal/`, and four programs put them in front of
different readers:

| Program | Source | Reader |
| :--- | :--- | :--- |
| `askiso` | `cmd/`, `cmd/askiso` | A person at a terminal: a cobra command tree, and a Bubble Tea UI (`internal/tui`) when run bare |
| `askiso-mcp` | `cmd/askiso-mcp`, `internal/mcp` | An AI assistant, over the Model Context Protocol on stdio |
| `askiso-lsp` | `cmd/askiso-lsp`, `internal/lsp` | An editor, as a language server for ISO 20022 XML |
| `askiso.wasm` | `web/wasm` | The website's playground, compiled with `GOOS=js GOARCH=wasm` |

The GitHub Action (`action.yml`) runs the `askiso` binary and uploads its
SARIF output to code scanning. `pkg/iso20022` is the only public Go API, for
programs that embed AskISO.

None of the front ends holds logic of its own beyond parsing input and
formatting output. A fix to how a message is validated is made once, in
`internal/validator`, and reaches all four.

## The catalogue is the user's, not ours

AskISO redistributes no ISO 20022 specification content. The schemas,
message definition reports and samples come from the Registration
Authority's downloads, which the user fetches and AskISO indexes on their
machine:

- `internal/registry` is the embedded list of which message identifiers
  exist, which message set publishes each, and where the Registration
  Authority hosts it. `internal/registrygen` rebuilds it from the published
  index (`go run ./scripts/gen-registry`).
- `internal/importer` turns a downloaded archive into a catalogue directory.
  Entry names are reduced to their base name and every destination is
  checked to stay under the catalogue root.
- `internal/catalog` finds the catalogue (`catalog.go`, `resolve.go`) and
  scans it into an in-memory index.
- `internal/cbprworkspace` does the same for CBPR+ usage guidelines an
  institution holds under its own Swift licence, keeping a content-minimised
  index so no Swift text leaves the machine.

Everything AskISO writes there is private to the user: files 0600,
directories narrowed by `internal/fsperm`, and replaced atomically by
`internal/atomicfile` so a concurrent reader never sees a truncated file.

## From schema to answer

| Package | Does |
| :--- | :--- |
| `internal/xsd` | Parses the XML Schema subset ISO 20022 uses |
| `internal/validator` | Checks an XML instance against a parsed schema |
| `internal/rules` | Checks scheme-level requirements (CBPR+ and others) beyond the schema |
| `internal/linter` | Field-level checks: IBAN (mod 97), BIC, UETR |
| `internal/codes` | ISO 20022 code sets, both those inside the schemas and the external sets the Registration Authority publishes separately |
| `internal/diff` | Compares two schemas structurally and classifies what breaks |
| `internal/converter` | Translates between ISO 20022 XML and JSON, preserving document order |
| `internal/schemagen`, `internal/generator` | Build a valid instance of a message from its schema, or a synthetic payment from options |
| `internal/swift`, `internal/translator` | Parse SWIFT MT messages and map MT to MX |
| `internal/flow`, `internal/graph` | Model a payment's lifecycle across messages and draw it (Mermaid or ASCII) |
| `internal/mock` | A local clearing rail, so an integration can be exercised end to end |
| `internal/ai` | The assistant behind `askiso ask`: a configured LLM endpoint (OpenAI-compatible, Ollama, Gemini or Claude) when there is one, and built-in answers otherwise |

## The website

`web/` is the source of <https://askiso.io>, built with ssg and deployed to
GitHub Pages by `.github/workflows/pages.yml`. Pages under `web/content/` are
written by hand; one page per message definition is generated from the
registry by `scripts/gen-message-pages`, which states only what the registry
and the code can support and never describes what a message means. The post-processing scripts in
`scripts/` inline the render-blocking assets under a hashed content policy,
add structured data, and build the sitemaps. The workflow fails the deploy on
an accessibility, readability, link or sitemap regression.

## Releases

A signed `v*` tag runs `.github/workflows/release.yml`: the test gate again
at the tag, then goreleaser builds the binaries, packages, SBOMs and the
Sigstore-signed checksum file, `actions/attest-build-provenance` records
SLSA provenance, and `scripts/releasepage` writes the release page from
`docs/releases/<tag>.md`.

## Verifying a change

`make ci` runs what CI runs for the Go code; `make web` and the targets after
it in the Makefile run the website checks. [CONTRIBUTING.md](../CONTRIBUTING.md)
has the details.
