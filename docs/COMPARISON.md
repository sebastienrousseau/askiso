# Ecosystem Comparison

| Capability | AskISO | Prowide (OSS) | Moov `iso20022` | Pain001 | Volante / Form3 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **Core Runtime** | Go (`pkg/iso20022`) + C-ABI + WASM | Java (JVM) | Go | Python | Commercial SaaS / Enterprise |
| **Streaming XSD Validation (`camt.053`)** | Yes (`ValidateStream`, bounded memory) | DOM / JAXB | Struct-only | XSD gate (`pain.001`/`008`) | Yes (commercial) |
| **Dated Scheme Profiles (CBPR+ / SR2027)** | Yes (`cbpr-2026`, `RuleOverlay`) | Commercial tier | No | No | Commercial |
| **SR2027 Address Classification & Audit** | Yes (`audit`, `internal/remediate`) | No | No | No | Commercial |
| **MCP & LSP Protocol Servers** | Yes (`askiso-mcp`, `askiso-lsp`) | No | No | MCP only | No |
| **SARIF 2.1 CI Integration** | Yes (CLI + GitHub Action) | No | No | No | No |
| **Zero-Redistribution Air-Gapped Mode** | Yes | Yes | Yes | Yes | N/A |
