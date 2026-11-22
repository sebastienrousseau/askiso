# AskISO FFI & C-ABI Contract (`askiso-abi/v1`)

`pkg/iso20022/ffi` provides a deterministic JSON-in / JSON-out dispatch layer shared by:

- **C-ABI (`cmd/askiso-cabi`)**, built with `go build -buildmode=c-shared`. No language packages are published yet; bind the library with your language's C FFI.
- **HTTP Daemon (`cmd/askiso-server`)** (`POST /v1/dispatch`).

The website's WebAssembly build calls `pkg/iso20022` directly rather than this layer.

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

`catalog_path` is honoured only by the C-ABI library, whose caller already
owns the filesystem. `askiso-server` ignores it: every
request reads the catalogue the operator chose with `-catalog` (default:
`$ASKISO_CATALOG`, then the standard locations), so a network client cannot
make the daemon read a directory of its choosing. The daemon also keeps its
filesystem private: `schema_path`, `sample_path` and `report_paths` are
relative to the catalogue root (for example
`Payments Clearing and Settlement/Version 11.0/Schemas/pacs.008.001.10.xsd`),
errors never contain the root, and a missing catalogue is reported without
the directories searched. The C-ABI library keeps absolute paths, which its
callers use to open the files. Go hosts get the daemon's behaviour from
`ffi.DispatchPinned`.
