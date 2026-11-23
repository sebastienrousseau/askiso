---
name: "AskISO"
short_name: "AskISO"
title: "AskISO documentation — commands, profiles and integrations"
description: "Every AskISO command, which ones need a catalogue, the scheme rule profiles, and how to wire it into your editor, your build or an AI assistant."
keywords: "askiso documentation, ISO 20022 CLI reference, askiso commands, ISO 20022 MCP server, ISO 20022 language server, askiso lint, askiso validate, askiso translate"
author: "Sebastien Rousseau"
date: "2026-08-25"
news_publication_date: "2026-08-25"
layout: "page"
language: "en-GB"
schema: "page"
changefreq: "weekly"
copyright_year: "2026"
form_origin: "https://askiso.io"
nav_docs: "true"
banner: "getty-images-dqHskSJDfe4"
banner_alt: "Lines of code and data rendered as a cyan grid."
eyebrow: "Reference"
headline: "Documentation"
lead: "Every command, what it needs, and how AskISO fits into your editor, your build and your AI assistant."
---

## Install

```bash
go install github.com/sebastienrousseau/askiso/cmd/askiso@latest
```

Tagged releases are on the [releases page](https://github.com/sebastienrousseau/askiso/releases).
Each one carries signed archives for Linux, macOS and Windows, a software bill
of materials, and a Sigstore signature over the checksums. From 0.0.8 the
macOS binaries are signed and notarised. Install with
`brew install --cask sebastienrousseau/tap/askiso` on macOS and Linux, or with
Scoop on Windows from the `sebastienrousseau/scoop-bucket` bucket.

The two protocol servers are separate binaries. A client launches each one
and takes over its standard input and output:

```bash
go install github.com/sebastienrousseau/askiso/cmd/askiso-mcp@latest
go install github.com/sebastienrousseau/askiso/cmd/askiso-lsp@latest
```

Building from source needs **Go 1.26.6 or newer**. That release carries the
standard library security fixes AskISO builds against.

## See it work

Every session here is replayed against the binary on each commit. So what you
read below is what AskISO actually prints, not a transcription of it.

Converting a SWIFT MT message, with the fidelity report that says what survived
the trip:

```console
$ askiso translate payment.mt103 --report
  TRANSLATE    MT103 → pacs.008.001.10

  source   payment.mt103
  fields   5 mapped, 3 derived, 2 truncated, 1 unmapped

  ⚠️  :121:  derived
       → /Document/FIToFICstmrCdtTrf/CdtTrfTxInf/PmtId/UETR
       the source carried no UETR; a new one was generated
  ✅ :20:  mapped
       → /Document/FIToFICstmrCdtTrf/GrpHdr/MsgId
  ❌ :23B:  unmapped
       bank operation code has no direct equivalent; carry it as a local
       instrument if the rail defines one
  ✅ :32A:  mapped
       → /Document/FIToFICstmrCdtTrf/CdtTrfTxInf/IntrBkSttlmAmt
  ✅ :50K:  mapped
       → /Document/FIToFICstmrCdtTrf/CdtTrfTxInf/Dbtr
  ⚠️  :50K (address):  truncated
       → /Document/FIToFICstmrCdtTrf/CdtTrfTxInf/Dbtr/PstlAdr/AdrLine
       MT addresses are unstructured; CBPR+ rejects those once the
       deferred structured address requirement takes effect. Populate
       TwnNm and Ctry.
  ⚠️  :52:  derived
       → /Document/FIToFICstmrCdtTrf/CdtTrfTxInf/DbtrAgt
       taken from the message header
  ⚠️  :57:  derived
       → /Document/FIToFICstmrCdtTrf/CdtTrfTxInf/CdtrAgt
       taken from the message header
  ✅ :59:  mapped
       → /Document/FIToFICstmrCdtTrf/CdtTrfTxInf/Cdtr
  ⚠️  :59 (address):  truncated
       → /Document/FIToFICstmrCdtTrf/CdtTrfTxInf/Cdtr/PstlAdr/AdrLine
       MT addresses are unstructured; CBPR+ rejects those once the
       deferred structured address requirement takes effect. Populate
       TwnNm and Ctry.
  ✅ :71A:  mapped
       → /Document/FIToFICstmrCdtTrf/CdtTrfTxInf/ChrgBr

  ⚠️  this conversion is lossy — review the entries above before relying on it
  → check the CBPR+ address rules:      askiso lint <file> --profile cbpr-2026
```

Conversion between MT and MX is lossy in both directions. The report names
every field and what became of it, so nothing is dropped without saying so.

## Commands

Commands marked ◆ read the XSD files, so they need a catalogue on disk.
Everything else works on its own, from the index built into the binary.

| Command | What it does |
| :--- | :--- |
| `askiso` ◆ | Terminal UI: live search, message table, schema and sample viewers |
| `askiso <question>` | Put a question straight on the command line — no quotes needed |
| `askiso ask` | The assistant, as an explicit subcommand or an interactive REPL |
| `askiso search <query>` | Search by identifier, domain, code or keyword |
| `askiso info <msg-id>` | Metadata and schema paths; names the set to download if absent |
| `askiso validate <xml>` | Full XSD validation, pure Go, no external tools |
| `askiso lint <xml>` | Business rules and scheme profiles |
| `askiso batch <dir>` | Validate and lint many messages at once |
| `askiso translate <file>` | SWIFT MT to ISO 20022 and back, with a fidelity report |
| `askiso generate <type>` | Synthetic messages for testing |
| `askiso diff <from> <to>` ◆ | Schema comparison with breaking-change classification |
| `askiso convert <xml>` | ISO 20022 XML to structured JSON and back |
| `askiso format <xml>` | Pretty-print or minify a message |
| `askiso code <code>` | Look up external codes — reasons, purpose, charges, status |
| `askiso schema <msg-id>` ◆ | Syntax-highlighted XSD |
| `askiso sample <msg-id>` ◆ | Syntax-highlighted sample message |
| `askiso flow` | Generate a linked four-stage payment lifecycle |
| `askiso graph` | Sequence diagrams and flowcharts, Mermaid or ASCII |
| `askiso mock` | A local HTTP mock clearing rail for testing |
| `askiso stats` ◆ | Catalogue metrics and domain distribution |
| `askiso list` | The 56 message set categories |
| `askiso catalog` | Manage the local catalogue: `fetch`, `add`, `status`, `where` |
| `askiso cbpr-pack import` | Build a private release manifest and local conformance sample suite |
| `askiso cbpr-pack status` | Show present and missing CBPR+ Usage Guideline variants |
| `askiso cbpr-pack verify` | Verify pinned local samples, schemas and external codes |
| `askiso doctor` | Diagnostics: what was found, and where it looked |

Every command takes `--json`. Exit status is 0 on success and 1 on failure, so
any of them drops into a pipeline.

## Getting a catalogue

AskISO ships no ISO 20022 specification content. Download the message sets you
need from the Registration Authority, then import them:

```bash
askiso catalog fetch pacs.008     # opens the right page, imports what lands
askiso catalog add ~/Downloads/*.zip
askiso catalog status             # what you have against every published set
askiso catalog where              # every location searched, and which won
```

AskISO looks for a catalogue in this order: `--catalog`, then
`$ASKISO_CATALOG`, then `$XDG_DATA_HOME/askiso/catalog`, then the platform
data directory, and last the working directory and its parents.

## Rule profiles

Schema validity says a message is well formed. It says nothing about whether a
clearing system will accept it. Profiles are the rules on top:

| Profile | What it checks |
| :--- | :--- |
| `base` | Structural sanity checks that apply everywhere |
| `cbpr-plus` | Live SR2025 message and Usage Identifier dispatch, BAH consistency, party/address rules, totals, UETRs, currencies and pacs.009 variants |
| `cbpr-2026` | Readiness for the deferred structured-address requirement; no replacement date is assumed ([briefing](/deadline/)) |
| `cbpr-2027` | Enhanced data: purpose codes, LEI, structured remittance |
| `investigations` | The camt exceptions and investigations family |
| `verification-of-payee` | VoP requirements |
| `all` | Everything above |

```bash
askiso lint payment.xml --profile cbpr-2026
askiso batch ./messages --profile all --format sarif > findings.sarif
```

SARIF output uploads straight into GitHub code scanning.

The built-in `cbpr-plus` rules cover the cross-message layer with no
catalogue. The exact limits on each message still come from the Swift
MyStandards Usage Guidelines that apply to you. A base ISO 20022 schema is not
a CBPR+ Usage Guideline.

### Local CBPR+ packs

The CBPR+ Usage Guideline workflow targets **Standards Release 2025
(SR2025)**. The separate `cbpr-2026` profile is a forward-looking address
readiness check. It is not an SR2025 Usage Guideline pack.

AskISO ships the engine, not Swift publications. If you hold the Usage
Guidelines, point the CLI at a private folder of them.

**Getting the schemas.** Open **CBPR+ SR2025 (Combined)** in MyStandards.
Select all 31 Usage Guidelines, add them to **My Selection**, and ask for the
**XML Schema Package** export. The export holds one folder per guideline, with
the payload and BAH schemas. A prepared export downloads at once. A generated
one appears under **MyDownloads** at the top right. If you cannot export a
selection, use each guideline's **Documentation → Export** action and pick the
XML Schema format. Keep the variant folders as they are: STP, COV, ADV, MLP and
COL.

```bash
askiso ask "Where is UETR mandatory?" --cbpr-pack /secure/CBPRPlus-SR2025
askiso lint payment.xml --cbpr-pack /secure/CBPRPlus-SR2025
askiso batch ./messages --cbpr-pack /secure/CBPRPlus-SR2025 --format sarif

askiso cbpr-pack import /secure/CBPRPlus-SR2025 \
  --workspace ~/.askiso-cbpr/sr2025 \
  --release SR2025 \
  --external-codes ~/Downloads/2Q2026_externalcodesets_v3.json \
  --generate-samples \
  --acknowledge-entitlement
askiso cbpr-pack status ~/.askiso-cbpr/sr2025
askiso cbpr-pack verify /secure/CBPRPlus-SR2025 \
  --workspace ~/.askiso-cbpr/sr2025
askiso cbpr-pack conformance /secure/CBPRPlus-SR2025 \
  --workspace ~/.askiso-cbpr/sr2025 --as-of 2026-09-05
askiso lint payment.xml --cbpr-workspace ~/.askiso-cbpr/sr2025
askiso batch ./messages --cbpr-workspace ~/.askiso-cbpr/sr2025 --schema
askiso validate payment.xml pacs.008.001.08.xsd \
  --external-codes ~/Downloads/2Q2026_externalcodesets_v3.json
```

**Nothing leaves your machine.** The folder is compiled in memory with local
`pdftotext`. It is not uploaded, copied or cached. For repeated runs,
`askiso cbpr-pack compile <folder> --output private.cbpr-pack.json` writes an
owner-readable local pack. Pack files are gitignored by default. Do not
redistribute one unless you hold the rights.

Local-only describes what AskISO does. A folder in iCloud Drive or another
synced drive may still be uploaded by that provider, under your own system
settings.

**Asking the pack a question.** `ask --cbpr-pack` runs a local extractive
search across PDF, MyStandards JSON, XML, XSD and XLSX files. Legacy `.xls`
files are listed, but must be saved as `.xlsx` to be searched. Results cite
relative local filenames and PDF pages. The command bypasses every model
provider, including OpenAI and Ollama, even when credentials are configured.

Every result names its pack fingerprint and its coverage. Checks read from a
PDF cover the explicit hierarchy and cardinality tables and the supported
lexical types. They warn that narrative, conditional, external-code-set and
diagram-only rules may need separate validation.

**Building a workspace.** The workspace adds a content-minimised manifest, exact
source hashes and a versioned sample suite. It copies none of your inputs.
Registration Authority XLSX, record and group JSON, and v3 JSON Schema code-set
publications are all supported. Their enumerated values are enforced wherever a
matching external simple type appears in a local XSD.

The suite reads its expectations from filenames. A name with `.invalid.`,
`-invalid` or `_invalid` in it should be rejected. Any other paired XML sample
should pass. This is a local baseline you can rerun. It is not a verdict from
the Swift Readiness Portal.

`lint --cbpr-workspace` and `batch --cbpr-workspace` load that pinned baseline
directly. Both verify its pack and external-code fingerprints. `batch --schema`
applies the pinned external-code values to matching schema types. Workspace and
ad-hoc pack flags are mutually exclusive.

**What discovery reads.** Private-source discovery supports PDF, Excel
(`.xlsx` and `.xls`), MyStandards Usage Guideline JSON Schema, and XML. AskISO
indexes the release, message and variant metadata of a JSON export without
storing its schema body. JSON Schema and XML Schema stay distinct. A JSON export
counts as guideline JSON, and only a local XSD can be paired with XML samples.
ZIP archives are ignored on purpose.

With `--generate-samples`, each entitled executable XSD yields two owner-only
fixtures inside the private workspace: a schema-valid positive and a
well-formed wrong-namespace negative. The suite hash-pins both, records
`origin: generated` and the mutation applied, and verifies them before use.
These are AskISO validator self-tests. They are not Swift-authored samples, and
they are not certification.

Status reports the overall guideline inventory separately from exact executable
coverage per message and Business Service. Your own samples are paired by an
embedded `BizSvc` or a variant marker in the filename. AskISO refuses an
ambiguous core, ADV, COV or STP match rather than silently choosing the first
schema.

**The strict gate.** The strict gate checks more. It wants private file
permissions and a signed-off entitlement. It wants all 31 executable variants,
your own passing and failing cases, and a spread of failure types. It pins the
quarter of the external code publication, checks the FINplus BAH and payload
bindings, and can take content-free evidence from an outside review. It never
calls an online validator.

AskISO is an independent project. It is not affiliated with, endorsed by or
certified by Swift. Swift and MyStandards are trade marks of S.W.I.F.T. SC. Do
not publish a compiled pack unless the source licence permits it. This
documentation is not legal advice.

## In an editor

`askiso-lsp` is a language server, so diagnostics appear as you type. It syncs whole documents rather than single edits, and it also offers hover and
completion; both need a catalogue, and say so rather than guessing when there
is none.

```lua
-- Neovim
vim.lsp.start({ name = 'askiso', cmd = { 'askiso-lsp' } })
```

## In an AI assistant

`askiso-mcp` speaks the Model Context Protocol over stdio and exposes ten
tools: search, info, lint, check_profile, validate, generate, translate, code,
diff and convert. An assistant set up this way works against the same engine
as the command line, rather than leaning on its own memory of the standard.

```json
{
  "mcpServers": {
    "askiso": { "command": "askiso-mcp" }
  }
}
```

## In continuous integration

```yaml
- run: go install github.com/sebastienrousseau/askiso/cmd/askiso@latest
- run: askiso batch ./messages --profile cbpr-2026 --format sarif > askiso.sarif
- uses: github/codeql-action/upload-sarif@v3
  with:
    sarif_file: askiso.sarif
```

## Known limitations

The [repository README](https://github.com/sebastienrousseau/askiso#known-limitations)
lists every one in detail, stated plainly because a validation tool that
overstates itself is worse than no tool. Four matter most. `translate` covers
seven MT and MX pairs both ways, and the exception family in one direction
only. MT940 transaction types fall back to `NMSC`, because no verified mapping
exists for them. `diff` treats any pattern change as breaking, since deciding
otherwise is not possible. And a message generated from a schema is minimal
and synthetic rather than realistic.

---

*AskISO is a developer tool, not regulatory advice. The authoritative source for
ISO 20022 is [iso20022.org](https://www.iso20022.org/).*
