<!-- SPDX-License-Identifier: Apache-2.0 OR MIT -->

<p align="center">
  <img src="logo.svg" alt="AskISO" width="128" />
</p>

<h1 align="center">AskISO</h1>

<p align="center">
  <strong>The ISO 20022 command line.</strong><br>
  Search, inspect, validate, lint, and generate ISO 20022 messages from your terminal.
</p>

<p align="center">
  <a href="https://github.com/sebastienrousseau/askiso/actions"><img src="https://img.shields.io/github/actions/workflow/status/sebastienrousseau/askiso/ci.yml?style=for-the-badge&logo=github" alt="Build" /></a>
  <a href="https://www.iso20022.org"><img src="https://img.shields.io/badge/standard-ISO%2020022-blue.svg?style=for-the-badge" alt="ISO 20022" /></a>
  <a href="LICENSE-APACHE"><img src="https://img.shields.io/badge/license-Apache%202.0%20%2F%20MIT-orange.svg?style=for-the-badge" alt="License" /></a>
  <a href="https://scorecard.dev/viewer/?uri=github.com/sebastienrousseau/askiso"><img src="https://img.shields.io/ossf-scorecard/github.com/sebastienrousseau/askiso?style=for-the-badge&label=OpenSSF%20Scorecard&logo=openssf" alt="OpenSSF Scorecard" /></a>
</p>

---

## Try it without installing anything

**[The web version](https://askiso.io)** runs the same engine
compiled to WebAssembly. Lint, generate, browse, convert, look up codes and check
IBAN/BIC/UETR values in the browser.

Your messages never leave the tab — there is no server to send them to. That matters when
the payload is a real payment instruction.

Or run the identical bundle locally with `make web-serve`.

---

## What AskISO is

AskISO is a single Go binary for working with ISO 20022 financial messages. It gives you
fuzzy search across the whole message catalogue, a Bubble Tea TUI, schema and sample
viewers, a semantic business-rule linter, synthetic message generation, MT ⇄ MX
cross-references, and a mock clearing rail — without leaving the terminal.

**AskISO does not redistribute ISO 20022 specifications.** The Registration Authority
publishes them free of charge at [iso20022.org](https://www.iso20022.org/); you download
what you need and point AskISO at it. That keeps the binary small, keeps your schemas
current, and means the specification content you validate against comes from the source
of truth rather than a mirror of unknown age.

> This is not the official ISO 20022 site. The sole source of up-to-date ISO 20022
> material is <https://www.iso20022.org/>

**AskISO is a developer tool, not regulatory advice.** It reports what it can verify
against the schemas and rules it was given. A clean result is not an assurance that a
scheme, a correspondent or a market infrastructure will accept the message — the
Registration Authority and your scheme operator remain authoritative on that. Where a
mapping cannot be verified against a published source, AskISO reports the gap rather
than guessing; [Known limitations](#known-limitations) lists every such case.

---

## Install

```bash
go install github.com/sebastienrousseau/askiso/cmd/askiso@latest
```

Or build from source:

```bash
git clone https://github.com/sebastienrousseau/askiso
cd askiso && make build
```

No external dependencies. Validation is pure Go, so there is no libxml2 or cgo to
install and results are identical on every platform. `xmllint`, if you have it, can be
used as a cross-check with `--engine libxml2`.

---

## Getting a catalogue

AskISO knows about all **2,845 message definitions across 285 published message sets**
out of the box — that index is embedded, so `search`, `info`, `code`, `translate`,
`lint` and `generate` work the moment you install it, with no download and no network.
That includes converting a real SWIFT MT message: `translate payment.mt103` needs no
schemas, because the target document is built rather than read.

Commands that read the actual XSD files — `schema`, `sample`, `diff`, `stats`,
`code --sets`, and full `validate` — need a catalogue. Download the message sets you want from the
[ISO 20022 catalogue of messages](https://www.iso20022.org/iso-20022-message-definitions)
and import them:

```bash
askiso catalog fetch pacs.008        # opens the right page, imports what lands
askiso code --import ~/Downloads/2Q2026_externalcodesets_v3.json # XLSX or JSON Schema
askiso catalog add ~/Downloads/PaymentsClearingAndSettlement_v11.zip
askiso catalog add ~/Downloads/*.zip
askiso catalog add ~/Downloads --dry-run     # see what would happen first
```

`catalog add` unpacks nested archives (the RA ships zips inside zips), sorts every file
into the right place, and matches the archive against the official set names so
`PaymentsClearingAndSettlement_v11.zip` and `payments-clearing-and-settlement.zip` both
land in one canonical directory.

Not sure what you have? `askiso catalog status` compares your install against the full
published standard; `askiso catalog where` shows every location AskISO searches.

The layout it produces, which you can also create by hand:

```text
<catalogue root>/
└── Payments Clearing and Settlement/
    ├── Version 11.0/
    │   ├── Schemas/                    ← required
    │   │   ├── pacs.008.001.10.xsd
    │   │   └── pacs.009.001.10.xsd
    │   ├── Sample Messages/            ← optional
    │   └── Message Definition Reports/ ← optional
    └── Version 12.0/
```

AskISO looks for the catalogue in this order:

| # | Location |
| :-- | :--- |
| 1 | `--catalog <path>` |
| 2 | `$ASKISO_CATALOG` |
| 3 | `$XDG_DATA_HOME/askiso/catalog`, when that variable is set |
| 4 | `~/Library/Application Support/askiso/catalog` (macOS) |
| 5 | `%LocalAppData%\askiso\catalog` (Windows) |
| 6 | `~/.local/share/askiso/catalog` |
| 7 | The working directory and its parents |

`HOME` wins over the operating system's own idea of your home directory wherever it is
set, which matters on Windows, where it is otherwise ignored entirely.

`catalog add` writes to whichever of those already holds a catalogue, so importing more
message sets extends your existing one rather than quietly starting a second.

```bash
askiso doctor    # confirms what AskISO found, and fails if it found nothing
```

If no catalogue is present, commands that need one tell you where AskISO looked and exit
non-zero. It never reports an empty catalogue as a healthy one.

> **Keep the catalogue out of cloud-synced folders.** iCloud Drive, Dropbox and OneDrive
> evict cold files and leave placeholders behind. AskISO detects iCloud placeholders and
> refuses to parse them, but the local data directory is the reliable home.

---

## Commands

Commands marked ◆ need a catalogue; the rest work standalone.

| Command | Description |
| :--- | :--- |
| `askiso catalog fetch <msg\|set>` | Open the right download page, then import the archive when it lands |
| `askiso catalog add <zip\|dir>...` | Import message sets downloaded from iso20022.org (`--dry-run`, `--to`) |
| `askiso catalog status` | Compare what you have installed against all 285 published sets (`--all`) |
| `askiso catalog where` | Show every location AskISO searches, and which one it picked |
| `askiso` ◆ | Interactive TUI: live search, message table, schema and sample viewers |
| `askiso search <query>` | Search by ID, domain, code, or keyword (`--json`); uses the embedded registry when no catalogue is installed |
| `askiso info <msg-id>` | Metadata and schema paths (`--json`); without a catalogue, names the message set to download |
| `askiso schema <msg-id>` ◆ | Syntax-highlighted XSD (`--copy`, `--raw`) |
| `askiso sample <msg-id>` ◆ | Syntax-highlighted sample XML (`--copy`, `--raw`) |
| `askiso stats` ◆ | Catalogue metrics and domain distribution (`--json`) |
| `askiso diff <from> <to>` ◆ | Path-level schema comparison with breaking-change classification (`--breaking`, `--strict`, `--json`) |
| `askiso validate <xml> [xsd]` | XSD and imported external-code validation (`--external-codes`, `--json`, `--stream`) |
| `askiso lint <xml>` | Business rules plus scheme profiles (`--profile all`, `--strict`, `--json`, `--format sarif`) |
| `askiso generate <type>` | Any of the 2,845 messages: templates with rail presets for four, schema-driven for the rest (`--from-schema`, `--optional`) |
| `askiso convert <file>` | ISO 20022 XML ⇄ JSON (`--to-json`, `--to-xml`) |
| `askiso format <xml>` | Pretty-print or minify (`--minify`, `--copy`) |
| `askiso code [query]` | Look up codes: curated, from your schemas, and from an imported publication (`--sets`, `--set`, `--import`) |
| `askiso translate <code>` | SWIFT MT ⇄ ISO 20022 MX field-mapping reference (`--matrix`) |
| `askiso translate <file>` | Convert a real message either way, with a fidelity report (`--out`, `--report`, `--format json`) |
| `askiso translate --matrix` | The full MT ⇄ MX cross-reference, field by field |
| `askiso batch <dir\|glob>` | Lint, validate and profile many messages at once (`--format sarif`, `--workers`) |
| `askiso cbpr-pack compile <dir>` | Compile locally licensed CBPR+ PDFs into a private reusable rule pack |
| `askiso cbpr-pack import <dir>` | Build a release-pinned private workspace, completeness manifest and local sample suite |
| `askiso cbpr-pack status <workspace>` | Report exactly which local Usage Guideline variants are present or missing |
| `askiso cbpr-pack generations <workspace>` | List retained immutable workspace snapshots and their integrity state |
| `askiso cbpr-pack activate <workspace> <fingerprint>` | Atomically roll back to a validated retained snapshot |
| `askiso cbpr-pack prune <workspace> --keep N --confirm` | Explicitly remove old inactive snapshots after operator confirmation |
| `askiso cbpr-pack verify <dir>` | Verify pinned local samples against matching schemas and imported external codes |
| `askiso cbpr-pack export-invalid-samples <dir>` | Derive eight classes of synthetic rejection fixture locally |
| `askiso cbpr-pack audit-samples <dir>` | Check provenance, duplicates, pairing and common live-data patterns |
| `askiso cbpr-pack diff <old> <new>` | Produce a content-free SR2025-to-SR2026 release delta |
| `askiso flow [type]` | Simulate a `pain.001` → `pacs.008` → `pacs.002` → `camt.053` lifecycle |
| `askiso graph [type]` | Sequence diagrams (`--format mermaid/ascii`) |
| `askiso mock` | Local HTTP mock clearing rail (`--port`) |
| `askiso doctor` | Diagnostics: catalogue, toolchain, AI connectivity |
| `askiso completion <shell>` | Shell completions for zsh, bash, fish, powershell |
| `askiso version` | Build version and metadata |

### Using AskISO from an AI assistant

`askiso-mcp` serves the same engine over the [Model Context Protocol](https://modelcontextprotocol.io),
so an assistant can check the specification instead of recalling it. Ten tools:
search, info, lint, profile check, validate, generate, MT translation, code
lookup, schema diff, and XML/JSON conversion.

```json
{
  "mcpServers": {
    "askiso": { "command": "askiso-mcp" }
  }
}
```

It speaks newline-delimited JSON-RPC 2.0 on stdin and stdout, writes nothing
else to stdout, and needs no catalogue for the seven tools that work in light
mode. `askiso-mcp --tools` lists what it exposes.

### Using AskISO from an editor

`askiso-lsp` is a language server for ISO 20022 XML. It publishes diagnostics as
you type — business rules, schema validation against your own downloaded XSDs,
and optional CBPR+ structured-address readiness checks — and adds hover,
completion and a document outline driven by the schema.

Neovim:

```lua
vim.api.nvim_create_autocmd('FileType', {
  pattern = 'xml',
  callback = function()
    vim.lsp.start({ name = 'askiso', cmd = { 'askiso-lsp' }, root_dir = vim.fn.getcwd() })
  end,
})
```

Completion lists the elements the schema allows at the cursor, **in schema
order** — ISO 20022 content models are ordered sequences, so an alphabetical
list would suggest invalid documents. Inside an enumerated element it offers the
code set instead. Without a catalogue it offers nothing rather than guessing.

`--profile` selects the rule profile (default `cbpr-2026`); an empty value turns
scheme rules off. A client can change it at runtime by sending
`workspace/didChangeConfiguration` with `{"askiso": {"profile": "cbpr-plus"}}`.

### Rule profiles

| Profile | What it checks |
| :--- | :--- |
| `base` | Structural sanity, applicable to any message |
| `cbpr-plus` | Live SR2025 CBPR+: message/Usage Identifier dispatch, BAH consistency, address and party rules, totals, UETRs, currencies, and pacs.009 variants |
| `cbpr-2026` | Readiness for the deferred structured-address requirement; no replacement date is asserted |
| `cbpr-2027` | The 2026 rules plus enhanced data: purpose codes, structured remittance, LEIs, UETRs |
| `investigations` | camt.110 / camt.111 — every investigation identifies its payment, every response quotes its request |
| `verification-of-payee` | acmt.023 / acmt.024 — a request says what to check, a failed report says why |
| `all` | Every rule AskISO currently implements; use dated profiles to interpret future-rule severity |

Dates that have not arrived produce **warnings**, not errors, so `--profile
cbpr-2027` tells you what to fix without failing a build for something that is
not yet required. The exception is `ENH-LEI-001`: a legal entity identifier that
fails its own ISO 7064 checksum is not a field awaiting a deadline, it is a
wrong one, and it is reported as an error today.

`cbpr-plus` is the embedded, catalogue-free cross-message layer. Exact element
cardinalities, restricted code sets and patterns remain defined by each Swift
MyStandards Usage Guideline. A base ISO 20022 XSD is not a substitute for a
CBPR+ Usage Guideline.

#### Bring your own CBPR+ guidelines

AskISO does not distribute Swift publications. Users who are authorised to use
their organisation's MyStandards exports can apply them locally:

In MyStandards, open **CBPR+ SR2025 (Combined)**, select all 31 Usage
Guidelines, add them to **My Selection**, and request the **XML Schema Package**
export. The resulting `MySelection_XMLSchemaPackage_...` package contains a
separate directory for each selected Usage Guideline, with its payload and BAH
XSDs. The browser downloads an already-built export immediately; otherwise
MyStandards queues it under the **MyDownloads** icon at the top right. If the
multi-selection export is unavailable to an account, open each Usage
Guideline's **Documentation** page and choose **Export** with the XML Schema
format. Keep each variant directory intact so STP, COV, ADV, MLP and COL remain
distinguishable.

The availability of XML-schema export is controlled by the specification owner
and the user's MyStandards access. Swift documents both downloading XML schemas
for shared FI-owned Usage Guidelines and retrieval of queued exports from
MyDownloads; AskISO cannot grant access or automate an authenticated session.

```bash
# Compile in memory and check one message. Nothing is persisted or uploaded.
askiso lint payment.xml --cbpr-pack /secure/CBPRPlus-SR2025

# Ask for evidence from the private PDFs. This bypasses every model provider.
askiso ask "Where is UETR mandatory in pacs.008?" \
  --cbpr-pack /secure/CBPRPlus-SR2025

# Check a local message directory against the same private source directory.
askiso batch ./messages --cbpr-pack /secure/CBPRPlus-SR2025 --format json

# Optional: make repeated checks faster with a private, content-minimised pack.
askiso cbpr-pack compile /secure/CBPRPlus-SR2025 \
  --output ~/.local/share/cbpr-sr2025.cbpr-pack.json
askiso lint payment.xml --cbpr-pack ~/.local/share/cbpr-sr2025.cbpr-pack.json

# Build a versioned workspace without copying any source artefact.
askiso cbpr-pack import /secure/CBPRPlus-SR2025 \
  --workspace ~/.askiso-cbpr/sr2025 \
  --release SR2025 \
  --external-codes ~/Downloads/2Q2026_externalcodesets_v3.json \
  --generate-samples \
  --acknowledge-entitlement
askiso cbpr-pack status ~/.askiso-cbpr/sr2025
askiso cbpr-pack generations ~/.askiso-cbpr/sr2025
askiso cbpr-pack verify /secure/CBPRPlus-SR2025 \
  --workspace ~/.askiso-cbpr/sr2025

# Enforce independent operator samples and externally recorded evidence.
make cbpr-strict-conformance \
  CBPR_SOURCE=/secure/CBPRPlus-SR2025 \
  CBPR_WORKSPACE=~/.askiso-cbpr/sr2025 \
  CBPR_EVIDENCE=/secure/CBPRPlus-SR2025/evidence.json

# Roll back locally if a later import is unsuitable.
askiso cbpr-pack activate ~/.askiso-cbpr/sr2025 <24-character-fingerprint>

# Prune old snapshots. This is destructive and always requires --confirm.
askiso cbpr-pack prune ~/.askiso-cbpr/sr2025 --keep 2 --confirm

# Materialise one locally generated valid BAH+Document fixture per variant.
askiso cbpr-pack export-valid-samples /secure/CBPRPlus-SR2025 \
  --workspace ~/.askiso-cbpr/sr2025 \
  --output '/secure/CBPRPlus-SR2025/04 Conformance Samples/Valid'

# Derive validated rejection fixtures. Re-import afterwards to pin their hashes.
askiso cbpr-pack export-invalid-samples /secure/CBPRPlus-SR2025 \
  --workspace ~/.askiso-cbpr/sr2025 \
  --output '/secure/CBPRPlus-SR2025/04 Conformance Samples/Invalid (Synthetic)'

# Create the 31-positive/31-negative independent-review work queue.
askiso cbpr-pack review-checklist ~/.askiso-cbpr/sr2025 \
  --output '/secure/CBPRPlus-SR2025/05 Conformance Evidence/Independent Sample Review Checklist.json'

# Inspect user-provided samples before a human reviewer attests to them.
askiso cbpr-pack audit-samples /secure/CBPRPlus-SR2025 \
  --workspace ~/.askiso-cbpr/sr2025

# Optional precautionary copies. The result remains synthetic and needs review.
askiso cbpr-pack anonymise-samples /secure/CBPRPlus-SR2025 \
  --workspace ~/.askiso-cbpr/sr2025 \
  --output '/secure/CBPRPlus-SR2025/04 Conformance Samples/Anonymised (Review Required)'

askiso cbpr-pack conformance /secure/CBPRPlus-SR2025 \
  --workspace ~/.askiso-cbpr/sr2025 \
  --as-of 2026-09-05 \
  --require-user-samples=true

# Reuse the verified, version-pinned workspace for day-to-day checks.
askiso lint payment.xml --cbpr-workspace ~/.askiso-cbpr/sr2025
askiso batch ./messages --cbpr-workspace ~/.askiso-cbpr/sr2025 --schema

# Enforce the same quarterly code publication during a one-off validation.
askiso validate payment.xml pacs.008.001.08.xsd \
  --external-codes ~/Downloads/2Q2026_externalcodesets_v3.json

# Select an effective-dated publication from a private history directory.
askiso cbpr-pack import /secure/CBPRPlus-SR2025 \
  --workspace ~/.askiso-cbpr/sr2025 \
  --external-codes /secure/External-Code-Publications \
  --external-codes-as-of 2026-09-05

# Compare entitled exports when the SR2026 package becomes available.
askiso cbpr-pack diff /secure/CBPRPlus-SR2025 /secure/CBPRPlus-SR2026 \
  --from-release SR2025 --to-release SR2026 \
  --output ~/.askiso-cbpr/sr2025-to-sr2026.json
```

The PDF path requires Poppler's `pdftotext`. AskISO invokes it directly without
a shell, reads the extracted text in memory, and records source filenames and
SHA-256 fingerprints—not PDF prose or absolute paths. It does not contact a
network service or create a cache. A compiled file is written only when the
user explicitly requests `--output`, with owner-only permissions; the standard
`*.cbpr-pack.json` name is gitignored by this repository.

“Local only” describes AskISO's behaviour: it does not send, copy into the
application, or upload the source artefacts. Filesystem providers remain outside
AskISO's control; a source folder in iCloud Drive, OneDrive or another synced
location is still synchronised by that provider according to the user's system
settings.

`ask --cbpr-pack` is an extractive local evidence search, not a connected AI
answer. It searches PDF, MyStandards JSON, XML/XSD and XLSX content in memory;
legacy `.xls` files are inventoried but must be saved as `.xlsx` to be searched.
Its control flow returns before AskISO creates an OpenAI or Ollama client, so
configured provider credentials cannot cause private passages to be sent
elsewhere. Results name relative local filenames and PDF pages where applicable.

PDF extraction covers explicit hierarchy/cardinality tables and supported
lexical types. Results identify themselves as **PDF-derived** and list any
coverage warnings; they do not claim equivalence with Swift's Readiness Portal.
Users remain responsible for their right to use the supplied documents and for
protecting both source and compiled packs. Do not publish or redistribute a
compiled pack unless the source licence permits it.

The importer recognises local PDF, Excel (`.xlsx`/`.xls`), MyStandards
Usage Guideline JSON Schema, and XML inputs. Guideline JSON metadata is used to
pin the exact message and Business Service variant, but a JSON Schema is not
misrepresented as an XML Schema. ZIP archives are deliberately ignored; export
the needed XML/XSD material as ordinary files if it should participate in the
local XML conformance suite.

`--generate-samples` closes the repeatable validator-evidence gap once entitled
XML/XSD Usage Guideline exports are present. For every executable XSD, AskISO
creates one schema-valid message and one well-formed wrong-namespace negative,
validates both before admitting them to the suite, and stores them only under
the owner-readable private workspace. External simple types use a value from
the imported Registration Authority publication when available. These are
explicitly labelled `origin: generated` with their negative mutation; they are
AskISO engine self-tests, not Swift-authored examples or certification evidence.

`export-valid-samples` combines each generated positive payload with a
schema-valid `head.001.001.02` Business Application Header from the paired
private BAH XSD. It validates every header, payload and header-to-payload binding
before writing the collection under the selected source directory. Filenames
include `askiso-generated`, and a later import preserves that provenance so the
fixtures cannot satisfy strict independent user-sample gates.

`export-invalid-samples` derives missing-mandatory, forbidden-element,
cardinality, lexical, restricted-code, external-code, Business Service and
BAH/payload mismatch cases, and admits a file only after the local validator
actually rejects it. Availability varies by schema: an external-code mutation
is emitted only when a suitable external type exists. The filenames retain
`askiso-generated`, so these engine tests cannot satisfy independent evidence
gates either.

The valid exporter supports `--transport envelope`, `--transport
request-payload`, and `--transport swift-datapdu`. DataPDU requires explicit
`--sender-dn` and `--receiver-dn`; its network service defaults to
`swift.finplus`. This is a local transport template, not a claim of validation
against an entitled Swift interface/network schema or a network acceptance
test.

Narrative conditions are never guessed from proprietary prose. An operator can
translate an entitled rule into `askiso-cbpr-rule-overlay/v1`, compile it with
`cbpr-pack compile-overlay`, and merge it during import with `--rule-overlay`.
Conditional constraints use `when_path`, `when_values`, or `when_absent`; their
source hash is retained in the resulting private pack.

The status output separates overall Usage Guideline inventory from executable
XML coverage. An XSD counts toward an exact message/Business Service pair only
when that service or specialised variant is explicit in its export content/path;
an unqualified export must at least identify itself as CBPRPlus before it can map
to the core service. This prevents an unconstrained base ISO XSD from being
reported as the pacs.008 STP or pacs.009 COV Usage Guideline. Add
representative user-held XML messages alongside their matching XSDs to test
business scenarios; use `.invalid.`, `-invalid`, or `_invalid` in expected-
rejection filenames.

The strict `conformance` command fails unless the workspace is local-only and
owner-readable, entitlement was acknowledged at import, all 31 executable
message/Business Service variants are present, the pinned suite passes, and an
external-code publication is appropriate for the requested validation quarter.
By default it also requires user-provided positive and negative samples for all
31 variants plus collection-level missing-mandatory, forbidden-element,
cardinality, lexical, restricted-code, external-code, Business Service and
BAH/payload scenarios. Put those scenario words in invalid filenames so the
content-free suite can classify them without storing message bodies.

Wrapped FINplus fixtures are supported: AskISO extracts and validates the
`Document`, then checks the `head.001.001.02` header namespace, From, To,
BizMsgIdr, MsgDefIdr, BizSvc and CreDt bindings. Bare `Document` fixtures remain
supported for schema-focused unit cases.

Independent portal results can be pinned without retaining proprietary request
or response bodies. Pass `--evidence evidence.json` and optionally
`--require-external-evidence`; the JSON must use
`askiso-cbpr-external-evidence/v1` and contain only provider, workspace/suite
fingerprints, RFC 3339 test time, case count and the passed verdict.
The `record-external-evidence` command requires an explicit acknowledgement and
records only a verdict the operator already obtained. AskISO neither invokes
nor impersonates the portal. Likewise, `attest-samples` records sample hashes
only after `audit-samples` is clean and a named human explicitly acknowledges
the independent review; it cannot create a reviewer or provider assertion.

The workspace manifest records only relative filenames, SHA-256 fingerprints,
message/service identifiers, counts and locally compiled rules. Source PDFs,
schemas, spreadsheets and samples stay in the directory selected by the user.
Manifest, suite, code index and pack files are owner-readable only. XML samples
are paired with matching local XSDs; `.invalid.`, `-invalid` and `_invalid` in a
sample filename mean rejection is expected. These local expectations are not
Swift Readiness Portal verdicts and the report says so explicitly.

Each refresh is built in a private staging directory and published as an
immutable `.generations/<manifest-fingerprint>` snapshot. A same-directory,
fsynced `current.json` replacement activates the complete snapshot; an OS file
lock serialises the compatibility mirror and pointer across processes. Failed
imports leave the previously active generation readable. Retained generations
can be integrity-checked with `generations` and selected with `activate` without
re-reading or copying any entitled source artefact.

Registration Authority external-code publications are accepted as XLSX,
record/group JSON, or the v3 JSON Schema representation. Imported values are
enforced during both buffered and streaming validation when a schema references
the corresponding external simple type. The source publication name, release
marker and SHA-256 fingerprint are retained so quarterly code-set changes cannot
silently alter a test baseline.
When `--external-codes` points to a directory, AskISO inventories every
recognised publication and selects the newest quarter effective on
`--external-codes-as-of`. The history is hash-pinned in the manifest while only
the selected publication is compiled into the runtime index.

`cbpr-pack diff` compares two local exports by relative path, message/service
identifier and SHA-256. It writes no source content. This gives an actionable
SR2025-to-SR2026 migration inventory once the user supplies the entitled target
export; it does not substitute for target-release Usage Guidelines or release
notes. Swift lists **14 November 2026** as the SR2026 live date and directs
users to MyStandards for final Usage Guidelines and schemas; AskISO deliberately
does not embed those artefacts.

`lint --cbpr-workspace` and `batch --cbpr-workspace` verify those fingerprints
before loading the compiled rules. With `batch --schema`, a workspace's pinned
external-code index also overrides the catalogue's built-in code list for matching
types. `--cbpr-workspace` and `--cbpr-pack` are mutually exclusive so a run cannot
silently mix two different baselines.

AskISO is an independent project and is not affiliated with, endorsed by, or
certified by Swift. Swift and MyStandards are trademarks of S.W.I.F.T. SC.
See Swift's [standards IPR policy](https://www.swift.com/about-us/legal/intellectual-property-rights-ipr-policies)
and [MyStandards access options](https://www.swift.com/products/mystandards);
this project documentation is not legal advice.

### In continuous integration

The repository ships a composite action, so a pull request that introduces a
non-compliant message is annotated rather than merged silently:

```yaml
permissions:
  contents: read
  security-events: write

jobs:
  iso20022:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-go@v6
      - uses: sebastienrousseau/askiso@v0.0.3
        with:
          path: ./messages
          profile: cbpr-2026
```

Findings are uploaded as SARIF, so they appear on the diff. Nothing is
downloaded from iso20022.org: linting and the rule profiles run from the
embedded index.

### TUI keys

Type any text to filter the catalogue — **every plain letter belongs to the
filter**, so the shortcuts are modified. `Enter` opens the sample, `ctrl+s` the
schema, `ctrl+k` checks the message (business rules, schema, and the November
2026 address rules in one pane), `ctrl+y` copies, `ctrl+a` opens the assistant,
`?` shows help, `q` quits.

`/catalog` shows what you have installed against the whole published standard,
with a download link beside anything missing. `/check` is the same as `ctrl+k`.

---

## The website

`web/site` is a static page that loads `pkg/iso20022` compiled to WebAssembly, so the
browser runs byte-for-byte the same logic as the CLI. A message linted on the website
gets the identical verdict to one linted in a terminal or a CI pipeline.

```bash
make web         # build the WebAssembly bundle
make web-test    # 30-check smoke test of the Go/JS bridge (needs node)
make web-serve   # http://127.0.0.1:8765
```

The site is light mode only: it carries the embedded index of the standard, never any
schema content. Anything needing the XSD text links to the official download and points
at the CLI. The deploy workflow enforces that: it fails if an `.xsd`, `.pdf` or `.docx`
ever reaches `web/site`.

It publishes to <https://askiso.io> on every push to `main`, and the
Go/JS smoke test gates the deploy — a renamed API or a field that stopped serialising
would break the page silently otherwise.

---

## Go SDK

```go
import "github.com/sebastienrousseau/askiso/pkg/iso20022"

xml, _ := iso20022.Generate(iso20022.GeneratorOptions{
    MsgType: "pacs.008",
    Preset:  "sepa",
    Amount:  "25000.00",
})

res, _ := iso20022.Lint([]byte(xml), "transfer.xml")
fmt.Printf("passed: %d  errors: %d\n", res.Passed, res.Errors)

jsonBytes, _ := iso20022.XMLToJSON([]byte(xml))
```

`pkg/iso20022` is the shared core. The CLI, the website's WebAssembly build, and any Go
service that imports it all run the same code.

Everything above works with no catalogue. To read schema text, open one:

```go
cat, err := iso20022.OpenCatalogue("")     // searches the conventional locations
info, _ := cat.Lookup("pacs.008.001.10")   // works with a nil *Catalogue too

if !info.Installed {
    // info.Sets names the message sets that publish it, each with a DownloadURL()
    for _, s := range info.Sets {
        fmt.Printf("download %s from %s\n", s, s.DownloadURL())
    }
}
```

The API reports `Installed: false` rather than failing, so callers can always tell the
user what to download.

---

## The deferred structured-address requirement

Swift deferred the planned **14 November 2026** cutover on 27 August 2026 and
will confirm replacement timing. The requirement remains, and AskISO checks readiness:

```bash
askiso lint payment.xml --profile cbpr-2026
```

```
  ❌ [CBPR-ADDR-002] the address is fully unstructured (3 address line(s), no structured element)
     at       /Document/CdtTrfTxInf/Dbtr/PstlAdr
     expected hybrid or structured
     fix      Move the town into <TwnNm> and the country into <Ctry>; keep the
              remainder in at most two <AdrLine> elements.

  ❌ [CBPR-ADDR-003] "France" is not an ISO 3166-1 alpha-2 country code
     at       /Document/CdtTrfTxInf/Cdtr/PstlAdr/Ctry
     expected "FR" (the code for France)
```

The pack enforces town and country presence, the ISO 3166-1 alpha-2 code format, the
two-line/70-character hybrid limit, and flags hybrid addresses as workable but less
durable. The exempt message types — `camt.052`, `camt.053`, `camt.054`, `camt.060`,
`camt.025`, `admi.024` — are skipped and reported as out of scope rather than passing.

This matters because schema validation cannot catch it: ISO 20022 constrains `<Ctry>` to
`[A-Z]{2,2}` and nothing more, so `XX` validates. AskISO checks against the 249 assigned
ISO 3166-1 codes.

Available as `--profile cbpr-2026` in the CLI and as the **Nov 2026** tab on the website.
Neither needs a catalogue: the rules are embedded.

---

## Validation

`askiso validate` implements the XML Schema subset ISO 20022 uses — element order,
cardinality, choices, wildcards, patterns, enumerations, length and numeric facets — in
pure Go.

It is checked against the reference implementation: over the whole catalogue AskISO and
libxml2 agree on **4,746 of 4,746** documents, accepting the same 1,035 and rejecting the
same 3,711. `make differential` reproduces that.

Diagnostics say more than the reference does:

```
  payment.xml:20:7
    [pattern]  "EURO" does not match the required format
    at       /Document/FIToFICstmrCdtTrf/CdtTrfTxInf/IntrBkSttlmAmt/@Ccy
    expected [A-Z]{3,3}
    found    EURO
```

The schema is resolved from the document's own namespace, so the second argument is
optional. Exit status is 0 when valid, 1 when not, so it drops straight into CI.

---

## Known limitations

Stated plainly, because a validation tool that overstates itself is worse than no tool.

- **`generate` covers every message, two ways.** `pacs.008`, `pacs.009`, `pain.001` and
  `camt.053` come from templates with rail-aware defaults and need no catalogue. Every
  other message is built by walking its schema, which needs one installed. All 4,746
  installed schemas were generated, validated against themselves, and linted clean —
  that is asserted by `make conformance`, not claimed. A schema-built message is
  minimal and synthetic: it shows the shape, not a realistic payment.
- **`translate` covers seven pairs, both ways.** MT101, MT103, MT104, MT107, MT202, MT204
  and MT940 become `pain.001`, `pacs.008`, `pain.008`, `pacs.009`, `pacs.010` and
  `camt.053`, and each of those converts back. Statements carry their entries in both
  directions. Everything produced validates against its schema and lints clean, and every
  pair round-trips. The rest of the MT suite is a reference table only.
- **The exception family converts one way only.** MT n92, n95 and n96 become `camt.056`,
  `camt.110` and `camt.111` for every category (MT192, MT292, MT592 and the rest). The
  new messages want coded investigation types and reasons where MT carries prose, and
  AskISO will not invent a code it cannot verify: the proprietary branch of the choice
  names the source message and the prose becomes the narrative. Converting back is not
  implemented.
- **The two directions lose different things, and both say so.** MT to MX produces
  unstructured addresses, which CBPR+ stops accepting once the deferred requirement takes effect. MX to MT loses
  purpose codes, legal entity identifiers and structured remittance outright, flattens
  structured addresses into free text, and cuts a 35-character reference to the 16 an MT
  field allows. A statement entry keeps its amount, dates and references, but MT940 wants
  a four-character transaction type from its own vocabulary and no verifiable mapping from
  the ISO 20022 bank transaction code exists — so a structured code becomes `NMSC` and the
  report names what was lost. A proprietary code already shaped like an MT type is passed
  through exactly, which is how a statement generated from an MT940 gets its own codes
  back.
- **Conversion is lossy, by nature and by design.** MT addresses are unstructured, so a
  converted message will not satisfy the deferred CBPR+ address requirement until the addresses are
  enriched. Every source field appears in the fidelity report; nothing is dropped
  silently.
- **`diff` compares patterns conservatively.** Deciding whether one regular expression
  accepts everything another does is not decidable in general, so any pattern change is
  reported as breaking.
- **`code` searches three sources.** A curated dictionary of 33 codes that needs nothing
  installed; every code set enumerated in the schemas you downloaded; and the Registration
  Authority's external code set publication once you import it with
  `askiso code --import <ExternalCodeSets.xlsx-or-json>`. AskISO ships none of the last two —
  they are your download, stored beside your catalogue.
- **`askiso-lsp` synchronises whole documents**, not incremental edits, and offers no
  code actions or formatting. Completion and hover need an installed catalogue; without
  one they say so rather than guessing.
- **Streaming validation releases transaction subtrees, not everything.** A file of
  8 MiB or more is validated as it is read, at roughly 120 bytes per transaction rather
  than the whole document — a 39 MB statement costs about 2.4 MB. The verdict is identical
  to the buffered path, asserted against every sample message in the catalogue. Positions
  beyond the last 16,384 lines are reported as byte offsets rather than line numbers.
- **`catalog fetch` guides a download; it does not perform one.** It finds the right
  message set, opens the Registration Authority's page, and imports the archive when it
  appears in your downloads folder. It never accepts the RA's terms on your behalf, and it
  will not, because those terms are yours to accept.
- **`convert` refuses names that are not valid XML.** Go's XML decoder is more lenient
  than the specification, and a name AskISO accepted on the way in would be one it could
  not emit on the way back. Found by fuzzing, along with an attribute-escaping bug.
- **Non-adjacent repeated elements cannot be converted to JSON.** A JSON object cannot
  express that ordering, so `convert` reports it rather than silently reordering the
  document. No message in the catalogue hits this.

---

## What is built, and what is next

Everything on the original roadmap is built. What follows is what it would take
next, listed so the gaps are visible rather than implied.

| Next | Why it is not there yet |
| :--- | :--- |
| A published bank-transaction-code mapping | MT940 field 61 wants MT's own four-character vocabulary. A mapping exists in scheme documentation; AskISO will carry one when it can be verified against a source, not before |
| Realistic schema-driven output | A schema walk produces a minimal, synthetic message with plausible identifiers. Making one read like a real trade — consistent parties, matching references across a lifecycle — needs domain data AskISO does not carry |

---

## Releases and packages

`v0.0.1` is released, with signed binaries and packages for Linux, macOS and Windows
attached to it. `go install` builds from source and is the quickest way in:

```bash
go install github.com/sebastienrousseau/askiso/cmd/askiso@latest
go install github.com/sebastienrousseau/askiso/cmd/askiso-mcp@latest
go install github.com/sebastienrousseau/askiso/cmd/askiso-lsp@latest
```

Homebrew and Scoop carry nothing yet: publishing to a tap writes to a second
repository, and the token for it is not configured. The cask and the manifest are
built and attached to each release, so adding that token starts publishing them
without any other change. Until then, the archives on the release page and
`go install` are the routes in:

```bash
# macOS and Linux
brew install --cask sebastienrousseau/tap/askiso

# Windows
scoop bucket add sebastienrousseau https://github.com/sebastienrousseau/scoop-bucket
scoop install askiso

# Debian, Ubuntu, Fedora, RHEL, Alpine, Arch — packages on the release page
```

Every release archive will carry all three binaries, an SBOM, and a Sigstore
signature over the checksums. The signing certificate records the workflow and
the commit that produced the artifact, so it can be verified without trusting a
key anyone had to store:

```bash
cosign verify-blob checksums.txt \
  --signature checksums.txt.sig \
  --certificate checksums.txt.pem \
  --certificate-identity-regexp 'https://github.com/sebastienrousseau/askiso/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

---

## Development

Building from source needs **Go 1.26.6 or newer**. That is a floor rather than a
preference: it is the release carrying the standard library fixes for the advisories
`govulncheck` reports against anything older, one of which
([GO-2026-6088](https://pkg.go.dev/vuln/GO-2026-6088)) guards `encoding/xml` against
unbounded decode recursion — directly on the path every `validate`, `lint` and `convert`
takes through untrusted input.

```bash
make build         # build the binary
make test          # unit tests
make cover         # tests with a coverage floor
make ci            # the full gate: fmt, vet, lint, test, cover, vuln, build,
                   # web-test, mcp-check, lsp-check
make conformance   # generate, convert and validate against your own catalogue
make differential  # agreement with libxml2 across the whole catalogue
make fuzz          # fuzz the parsers (FUZZTIME=5m for a longer run)
make web           # build the WebAssembly bundle for the website
make web-test      # smoke-test the Go/JS bridge
```

CI runs that same gate on Linux, macOS and Windows on every push, plus `govulncheck`
and CodeQL. Coverage is enforced at 98% on a runner with no catalogue installed, which
is a stricter measurement than a developer machine gives you: anything reachable only
when a catalogue happens to be present does not count towards it.

`make conformance` and `make differential` are not run in CI, because CI has no
catalogue. Run them before tagging a release. Conformance records known defects
explicitly, so fixing one turns the suite red until the expectation is removed.

See [CONTRIBUTING.md](CONTRIBUTING.md) and [SECURITY.md](SECURITY.md).

---

## License

Dual-licensed at your option:

- **Apache License 2.0** ([LICENSE-APACHE](LICENSE-APACHE))
- **MIT License** ([LICENSE-MIT](LICENSE-MIT))

ISO 20022 is a registered standard of the International Organization for Standardization.
AskISO bundles no ISO 20022 specification content — see [NOTICE](NOTICE).
