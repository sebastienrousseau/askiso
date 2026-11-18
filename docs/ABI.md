# AskISO FFI & C-ABI Contract (`askiso-abi/v1`)

`pkg/iso20022/ffi` provides a deterministic JSON-in / JSON-out dispatch layer shared by:
- **C-ABI (`cmd/askiso-cabi`)** built via `go build -buildmode=c-shared` for Python (`askiso-py`), Java (`askiso-java`), and Rust SDKs.
- **WASM** builds for TypeScript/Node/Browser (`askiso-ts` / `@askiso/wasm`).
- **HTTP Daemon (`cmd/askiso-server`)** (`POST /v1/dispatch`).

## C-ABI Symbols

```c
char* askiso_abi_version(void);
char* askiso_call(const char* op, const char* json_payload);
void  askiso_free(char* ptr);
```

Every `char*` returned by `askiso_abi_version` or `askiso_call` must be freed by the caller via `askiso_free`.

## Response Envelope

```json
{
  "ok": true,
  "abi": "askiso-abi/v1",
  "data": { "...": "..." }
}
```

On error:

```json
{
  "ok": false,
  "abi": "askiso-abi/v1",
  "error": "human-readable message"
}
```

## Supported Operations

| Operation | Payload Fields | Output |
| :--- | :--- | :--- |
| `version` | `{}` | `{"abi":"askiso-abi/v1"}` |
| `lookup` | `{"id":"pacs.008.001.10","catalog_path":""}` | `MessageInfo` |
| `search` | `{"query":"credit transfer","catalog_path":""}` | `[]MessageInfo` |
| `lint` | `{"xml":"...","filename":"","profile":"cbpr-2026"}` | `{"lint":LintResult,"profile"?:RuleResult}` |
| `validate` | `{"xml":"...","schema":"","catalog_path":""}` | `SchemaResult` |
| `translate_mt` | `{"mt":"..."}` | `Conversion` |
| `translate_mx` | `{"xml":"..."}` | `Conversion` |
| `classify_addresses` | `{"xml":"..."}` | `map[string]string` |
