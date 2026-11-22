# Security Model

## Core Guarantees

1. **XXE & Entity Expansion Protection:** `encoding/xml` decoders run with `Strict = true` and `Entity = xml.HTMLEntity`, rejecting DTD entity expansion and never fetching external entities.
2. **Bounded I/O & HTTP Hardening:** `askiso-server` and `internal/mock` enforce `http.MaxBytesReader` (8 MiB default), `ReadHeaderTimeout` (5s), `ReadTimeout` (30s), `WriteTimeout` (30s), and `IdleTimeout` (60s). `internal/ai` bounds outbound LLM responses with `io.LimitReader`.
3. **Operator-Pinned Catalogue:** `askiso-server` reads only the catalogue set with `-catalog` (or the default search). A `catalog_path` in a request body is ignored, so clients cannot point the daemon at arbitrary directories. Responses give file paths relative to the catalogue root and errors omit host paths, so clients cannot map the server's filesystem.
4. **Least-Privilege Local Storage:** Private CBPR+ workspaces enforce `0700`/`0750` directories and `0600` files via `internal/atomicfile` and `internal/fsperm`.
5. **Continuous Gates:** CI enforces `govulncheck ./...`, `gosec` SARIF upload, CodeQL, OpenSSF Scorecard, `-race`, `-d=checkptr=2`, ASan, and MSan.
