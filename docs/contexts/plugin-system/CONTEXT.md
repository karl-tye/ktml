# Plugin System

The WASM-based extension mechanism that currently enables language-specific code generation, formatting, and LSP intelligence. ADR-0002 additionally defines a target Library Plugin boundary that is not yet implemented.

## Language

**Plugin**:
A compiled WebAssembly (`.wasm`) module that implements the KTML Wasm ABI. Each plugin declares its capabilities (generate, format, lsp) via a Manifest. Plugins are sandboxed — no network access, restricted read-only filesystem access via WASI.
_Avoid_: Module, extension, addon, compiler

**Library Plugin**:
The target form of opt-in Plugin that describes a web library's attributes, output contexts, routes, or embedded-language tooling through generic Host contracts without adding KTML syntax or changing rendered output. Official HTMX, Alpine.js, and Tailwind integrations will be Library Plugins, not privileged Core Language features.
_Avoid_: Language extension, framework dialect, compiler special case

**Generator Plugin**:
The target form of Plugin that converts the canonical semantic model into a target artifact. Current prototype generators consume serialized syntax AST instead. A Generator Plugin may reject an unsupported target but cannot reinterpret or enlarge the Core Language.
_Avoid_: Language backend dialect, syntax plugin

**Host**:
The KTML CLI process that loads, sandboxes, and executes Plugins. The Host parses `.ktml` files to AST, serializes them to JSON, writes the payload into WASM memory, and calls plugin exports. It also provides Host Functions for controlled side effects.
_Avoid_: Runtime, orchestrator, main process

**Wasm ABI**:
The contract between Host and Plugin. Plugins must export: `ktml_manifest`, `ktml_generate`, `ktml_format`, `ktml_lsp_handle`. All communication uses a packed return convention: a single 64-bit value encoding `(pointer << 32) | length` into WASM linear memory. Payloads are JSON.
_Avoid_: API, interface, protocol

**Plugin Manifest**:
A JSON object returned by `ktml_manifest()` declaring the plugin's name, version, and supported capabilities (`generate`, `format`, `lsp`). The target Host enforces it before routing; current prototype enforcement is incomplete.
_Avoid_: Plugin metadata, capability declaration

**Host Function**:
A function exported by the Host into the WASM module's `env` namespace that plugins can call for controlled side effects. Current host functions:
- `ktml_host_write_file` — requests the Host to write an Auxiliary Output, confined to the plugin's declared Output Roots and the Host deny-list (cli-spec §2.4)
- `ktml_host_proxy_lsp` — proxies an LSP request to an External LSP Server
_Avoid_: Host export, callback, import

**Plugin Capability**:
One of the operations a Plugin can declare support for, currently `generate`, `format`, or `lsp`. The target Host routes only capabilities advertised by the Plugin Manifest; current enforcement is incomplete. The gohtml plugin advertises all three.
_Avoid_: Feature, mode, operation

**Generation Config Payload**:
A JSON object passed by the Host to `ktml_generate` alongside the AST. Contains: `environment` ("development" or "production"), `file_path` (absolute path to the source `.ktml` file), and `settings` (arbitrary plugin-specific key-value pairs from Configuration).
_Avoid_: Build context, generation options

**Environment**:
The execution context for Generation, either `"development"` or `"production"`. Development mode enables dev-specific output like `data-ktml-id` attributes for Hot Reload targeting. Set via the `environment` field in the Generation Config Payload.
_Avoid_: Mode, build flavor

**Sandboxing**:
Plugins execute in a restricted WASM sandbox with no network access and configured read-only WASI mounts. File writes go through `ktml_host_write_file`. The Host confines every write and stale-output deletion to the plugin's declared Output Roots, after symlink evaluation, and never touches `.git`, `.ktml`, configuration, KTML sources, or handwritten Go.
_Avoid_: Isolation, jail, security boundary

**Plugin Configuration**:
Per-plugin settings in `ktml.config.json`: `path` (location of the `.wasm` file), `allow_read` (WASI-accessible directories), `outputs` (Output Roots: the directories, optionally restricted to file suffixes, where the plugin may generate files), and `settings` (arbitrary key-value pairs passed through to the Generation Config Payload).
_Avoid_: Plugin options, plugin setup

**WASI Permissions**:
The read-only filesystem directories a Plugin can access, configured via `allow_read` in Plugin Configuration. Used by plugins to scan project directories for imports, type definitions, or related files without being able to modify them.
_Avoid_: Filesystem access, read paths
