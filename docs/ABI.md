# AskISO JSON Contract (`askiso-abi/v1`)

`pkg/iso20022/ffi` provides a deterministic JSON-in / JSON-out dispatch layer
behind the HTTP daemon, `cmd/askiso-server` (`POST /v1/dispatch` and its
shortcut routes). No language packages are published; call the daemon over
HTTP from any language.

The website's WebAssembly build calls `pkg/iso20022` directly rather than this layer.

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
| `lookup` | `{"id":"pacs.008.001.10"}` | `MessageInfo` |
| `search` | `{"query":"credit transfer"}` | `[]MessageInfo` |
| `lint` | `{"xml":"...","filename":"","profile":"cbpr-2026"}` | `{"lint":LintResult,"profile"?:RuleResult}` |
| `validate` | `{"xml":"...","schema":""}` | `SchemaResult` |
| `translate_mt` | `{"mt":"..."}` | `Conversion` |
| `translate_mx` | `{"xml":"..."}` | `Conversion` |
| `classify_addresses` | `{"xml":"..."}` | `map[string]string` |

Every request reads the catalogue the operator chose with `-catalog`
(default: `$ASKISO_CATALOG`, then the standard locations). A `catalog_path`
field in a payload is ignored, so a network client cannot make the daemon
read a directory of its choosing. The daemon also keeps its
filesystem private: `schema_path`, `sample_path` and `report_paths` are
relative to the catalogue root (for example
`Payments Clearing and Settlement/Version 11.0/Schemas/pacs.008.001.10.xsd`),
errors never contain the root, and a missing catalogue is reported without
the directories searched. Go hosts get the same behaviour from
`ffi.DispatchPinned`.
