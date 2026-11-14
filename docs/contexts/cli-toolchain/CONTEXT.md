# CLI Toolchain

The command-line tool that processes `.ktml` source files through a pipeline of parsing, generation, formatting, and LSP. It acts as an orchestrator — delegating language-specific code generation to WASM plugins.

## Language

**Generation**:
The process of compiling a `.ktml` Document into target-language output (HTML, Go structs, etc.) via a WASM plugin. Triggered by `ktml generate <file>`. In Dev Mode, re-triggered on file changes. Incremental via content hashing.
_Avoid_: Compilation, build, transpilation

**Formatting**:
The process of pretty-printing a `.ktml` Document back to KTML source code with consistent indentation, spacing, and attribute ordering. Triggered by `ktml fmt`. Can use either the built-in Go formatter or a WASM plugin.
_Avoid_: Linting, prettifying

**Primary Output**:
The main generated file from a Generation — the content returned directly by the plugin's `ktml_generate` export. For the gohtml plugin, this is an HTML/template file (e.g., `index.ktml.go` producing Go `html/template` code).
_Avoid_: Main output, build artifact

**Auxiliary Output**:
Additional files generated during Generation via the plugin calling `ktml_host_write_file`. Examples: Go struct definitions for type-safe template data, CSS files, routing manifests. Tracked in the Build State for cleanup when the source changes.
_Avoid_: Side output, extra file, secondary artifact

**Build State**:
A manifest file (`.ktml/state.json`) that maps each source `.ktml` file to its content hash, Primary Output path, and Auxiliary Output paths. Enables incremental builds (skip if hash unchanged) and stale output cleanup (delete outputs for removed source files).
_Avoid_: Cache manifest, build cache

**Content Hash**:
A SHA-256 hash of a `.ktml` source file's contents, stored in the Build State. Used to detect whether a file has changed since the last Generation and avoid redundant work.
_Avoid_: Checksum, fingerprint, digest

**Configuration**:
The `ktml.config.json` file at the project root. Controls which plugins are loaded, their WASI permissions, per-pipeline resolution rules, the Package Directory, external LSP servers, and Dev Server settings.
_Avoid_: Settings, options, preferences

**Package Directory**:
The root directory that contains `.ktml` source files and serves as the base for Package resolution. Configured via `package_dir` in Configuration. Allows imports like `from "ui"` instead of relative paths.
_Avoid_: Source root, components directory

**Resolution Rules**:
The per-pipeline plugin selection in Configuration. `resolution.generate` picks which plugin handles Generation; `resolution.format` and `resolution.lsp` can specify multiple plugins (with `"*"` meaning all). If only one plugin is configured and no explicit rule is set, it's auto-assigned as the generate plugin.
_Avoid_: Plugin routing, dispatch rules

**External LSP Server**:
A language server (e.g., `gopls`) configured in `ktml.config.json` under `lsp`. The CLI manages these as child processes. Plugins can proxy LSP requests to them via `ktml_host_proxy_lsp` to reuse existing language intelligence without reinventing it.
_Avoid_: Upstream LSP, delegated LSP

**LSP Multiplexing**:
When the IDE sends an LSP request, the CLI broadcasts it to all plugins that registered the `lsp` capability. Responses from multiple plugins are merged into a unified response. Allows each plugin to contribute its own intelligence (completions, diagnostics, go-to-definition) without conflicts.
_Avoid_: LSP fanout, request broadcasting
