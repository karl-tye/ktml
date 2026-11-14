# Context Map

## Contexts

- [Core Language](./docs/contexts/core-language/CONTEXT.md) — the KTML templating language: syntax, semantics, and the AST model
- [CLI Toolchain](./docs/contexts/cli-toolchain/CONTEXT.md) — the command-line tool that processes `.ktml` files through generation, formatting, and LSP
- [Plugin System](./docs/contexts/plugin-system/CONTEXT.md) — the WASM-based extension mechanism: ABIs, host functions, sandboxing, and plugin resolution
- [Dev Server](./docs/contexts/dev-server/CONTEXT.md) — the development experience: file watching, livereload, hot reload, and reverse proxying

## Relationships

- **Core Language → CLI Toolchain**: The CLI reads Core Language Documents, parses them to AST, and feeds them into the generation/formatting pipeline
- **Core Language → Plugin System**: Target Generator Plugins will consume the canonical semantic contract. Target Library Plugins will add framework schemas and tooling through generic hooks without extending Core Language syntax or rendering semantics. Current prototype plugins still consume serialized syntax AST.
- **CLI Toolchain → Plugin System**: The CLI hosts WASM plugins, routing generation, formatting, and LSP requests to the appropriate plugin via the configured resolution rules
- **CLI Toolchain → Dev Server**: The Dev Server wraps the CLI's `generate` pipeline and extends it with file watching, an HTTP reverse proxy, and live-reload SSE injection
- **Plugin System → Dev Server**: The Dev Server calls plugin generation on file changes to support hot component reload
