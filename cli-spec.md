# KTML CLI & Plugin Architecture Specification

## 1. Overview
The KTML CLI is a modular, high-performance toolchain written in Go. It acts as an orchestrator, parser, and LSP multiplexer. Language and framework-specific behaviors (generation, formatting, intellisense) are delegated to WebAssembly (Wasm) plugins.

The CLI serves three primary commands:
- `ktml generate [-check] [-v] <file|dir|dir/...>...`: Compiles `.ktml` and `.ktmlschema` files. A directory selects its own sources; `dir/...` selects every source below it, skipping hidden, `vendor`, and `node_modules` directories. Flags may come before or after the paths. `-check` generates in memory, ignoring the build cache, and writes nothing (no outputs, no `.ktml/state.json`): it prints one line per output that would differ, `missing: <path>`, `changed: <path>`, or `stale: <path>`, and exits 1 if there is any. A development output (`*_ktml_dev.go`) is optional: a missing one is not reported, so a repository can commit production output and ignore development output; one that exists is compared. A stale output is a native generator output (`*_ktml.go`, `*_ktml_dev.go`, `*_ktmlschema.go` with a `// Code generated ... DO NOT EDIT.` header) in a directory the arguments select that no selected source generates any more, or a recorded build-state output of a selected source that generation no longer produces. It is for repositories that commit generated output, as a staleness gate in CI or a pre-commit hook. `-v` (or `-verbose`) logs at debug level.
- `ktml fmt`: Formats `.ktml` files based on plugin rules.
- `ktml vet [patterns...]`: Reports generated-props composite literals in Go code that omit a required KTML input (`[missing-required-input]`, ADR-0003 Q2). Patterns are directories or `dir/...` (default `./...`). It exits 1 when there are findings. Findings are not yet published through the LSP: the LSP relays plugin diagnostics per document, so host-side Go diagnostics need a merge path first.
- `ktml migrate templ <file|dir|dir/...>... --report <file.json> (--dry-run | --out <dir> [--scaffold-presenters] | --in-place [--scaffold-presenters] [--force] [--bridges auto|all|none] [--remove-templ [--hand-finished]]) [--summary <file.md>] [--categories <file.json>]`: Runs the separate `ktml-migrate` tool (found next to `ktml`, then on `PATH`; install it with `go install github.com/karl-tye/ktml/migrate/cmd/ktml-migrate`) with the same arguments, and returns its exit status. The tool is its own Go module, `migrate/`, so that the templ parser it needs stays out of KTML's dependency graph. It reads templ files with the pinned templ `parser/v2` AST and classifies every node as `converted`, `presentation_scaffolded`, `review_required`, or `blocked`. `--dry-run` writes only that inventory: a JSON report, and optionally a Markdown summary between generated markers. `--out <dir>` converts: for each templ file it writes `.ktml`, a `.ktmlschema` of view records, a `_present.go` presenter scaffold, and a `.ktmlmap.json` source map under the same relative directory of `<dir>`, which must sit inside a Go module; the `.templ` files are never written. Templates that need a presenter are partially converted unless `--scaffold-presenters` is given. The report then also lists the outputs, source-ranged diagnostics, security-review markers, and presenter field counts, and the emitted KTML is validated with the compiler and the Go generator. The command exits 1 when any node is blocked, dropped, or partially converted, any file fails to parse, or the emitted KTML fails validation, and 2 on usage errors. `--in-place` converts a batch (templ files or packages) into the source packages themselves (Phase 7): the `.ktml`, the package's `.ktmlschema`, `_present.go` presenters, `migrate_helpers.go`, and `_bridge.go` templ bridges go next to the templ files, and source maps go under `<root>/.ktml/migrate`. Presenters reach the package's unexported helpers. Existing outputs are kept, since presenters are finished by hand, unless `--force`. A bridge `XTempl` with the templ signature is emitted for each exported template that an unconverted `.templ` file calls (`--bridges`). `--remove-templ` deletes the batch's `.templ` and `_templ.go` files only when the batch is complete: no partial or blocked node (unless `--hand-finished`), valid KTML, and no Go or templ code in the module still using its templates. The report's `conversion.calls` maps every converted template to its Hybrid H spelling. Presenters build JSON values with `ktml.MustJSON`, except `templ.JSONScript` data, which keeps templ's trailing newline (`migrateJSONScript`). See [docs/migration/in-place-conversion.md](docs/migration/in-place-conversion.md) and [docs/language/migration-language-v1.md](docs/language/migration-language-v1.md).
- `ktml migrate codemod <package pattern>... --map <conversion.json> --report <file.json> [--write] [--fragment-render <import path>.<Func>] [--root <dir>] [--tags <tag,...>]`: Runs `ktml-migrate codemod`. It rewrites the Go callers of the templates a conversion report maps to the Hybrid H API (`pkg.X(a)` becomes `pkg.X{View: pkg.NewXView(a)}`), and renames their calls in unconverted `.templ` files to the templ bridges, regenerating those files' Go. A direct `.Render(ctx, w)` into an `http.ResponseWriter` is routed through `--fragment-render`, a buffered renderer (ADR-0003). Every other use is a `manual` site with its source range and reason, and is never edited. `--tags` loads the packages with build tags, so files built only with them (such as `e2e` tests) are rewritten too. A nil `templ.Attributes` argument becomes `ktml.Attributes{}`. Without `--write` it only reports. The output is gofmt'd, and a second run changes nothing. It exits 1 when a site needs a hand edit.
- `ktml lsp`: Runs a Language Server Protocol server for IDE integration.
- `ktml explore [-addr host:port] [root]`: Runs the Component Explorer over the `.story.ktml` stories under `root` (default `.`), on a loopback address only (default `127.0.0.1:7332`; any other address exits 1). It reads the `explorer` section of `root/ktml.config.json` (`static` `{dir, url}`, `stylesheets`, `scripts`, `body_class`; local URL paths only). See [docs/explorer/explorer-v1.md](docs/explorer/explorer-v1.md). `ktml generate` skips story documents.
- `ktml stories generate [-chi dir] [-all] [-unreachable] [-check] [-json] [paths...]`: Writes stories for the components the application renders. Reachability follows the HTMX route index (`-chi`, else `plugin.htmx.settings.chi`, else the whole module) from each route's handler expression into the module's Go code, type-checked from source, to the generated components it constructs (pages), then the KTML calls below them; `-unreachable` lists the components no route reaches, and `-all` writes stories for every component. `paths` (files, directories, or `dir/...`; default `./...`) select the component Documents. Each Document `x.ktml` gets stories in `x.story.ktml` beside it: one per Variant of each state input the component selects Render Clauses on (else `Default`), then, except for pages, `Empty`, `NoOptionals`, and `LongText` when they differ, with values synthesized from field names and types under a fixed seed, placeholder fixtures for trusted values, and `[]` for attribute bags. Every story sits under a `// ktml:generated <hash>` line: a rerun rewrites intact stories, removes stale ones, adds new ones, and keeps any story edited or written by hand (reported). Nothing is written unless every generated story type-checks with no diagnostic and renders. `-check` writes nothing and exits 1 when a Document would change; `-json` prints the reachability and the outcome. See [Stories and Fixtures v1](docs/language/stories-v1.md#generated-and-captured-stories).
- `ktml dev`: Runs the development loop configured by the `dev` section of `ktml.config.json`. Its proxy listens on `127.0.0.1:<dev.port>` and answers only requests for a loopback host (`localhost`, `127.0.0.1`, `::1`); any other `Host` gets 403, against DNS rebinding. If that port is in use, ktml dev exits 1 before starting anything; request headers must arrive within 10 seconds. With `dev.explorer` set to `{"addr": "host:port"}`, it also serves the Component Explorer over the project root on that address, beside the dev loop, and stops it with dev; it logs `KTML Explorer on http://host:port/`. `addr` defaults to `127.0.0.1:7332` and follows the `ktml explore -addr` rules: it must be loopback (`127.0.0.1`, `::1`, or `localhost`; port 0 picks a free port), and any other address is a configuration error at `dev.explorer.addr` (ktml dev exits 1, and the LSP reports it on the value). Unknown keys are ignored, as elsewhere in `dev`. `dev.ready_timeout_ms` bounds how long a rebuilt backend may take to answer before ktml dev restores the previous one (default 5000; a backend that seeds fixtures at startup, needs longer). `dev.proxy_target` must be an `http` or `https` URL with a host and no credentials, and loopback (`localhost`, `127.0.0.1`, `::1`): ktml dev sends the app's development control token to it, and never follows a redirect with it. A target outside loopback is a configuration error at `dev.proxy_target` unless `dev.allow_remote_proxy_target` is `true`, and even then it must be `https`; ktml dev then prints a warning at startup. The Explorer runs in-process with its own server, livereload hub, and watcher, and reads the top-level `explorer` section. Without `dev.explorer`, no Explorer starts. The dev generation loop ignores story documents either way. `ktml dev [--capture-stories <dir>] [--capture-raw]`: `--capture-stories` asks the application to write a story for each new shape of props it renders as a page, into `dir` (inside the project, not `.git` or `.ktml`), mirroring the component tree. It needs the project's consent, `"dev": {"capture": {"allow": true}}`, and a loopback `dev.proxy_target`, or ktml dev exits 1; and the application's, `hmr.Install(handler, hmr.LocalData(true))` in its ktmldev wiring, or the app refuses and ktml dev prints why. Values are redacted with stable fakes unless `--capture-raw`, which prints a warning. ktml dev prints the capture status once the app answers.

`ktml dev --profile <name>` applies `dev.profiles.<name>`, which overrides `command`, `build`, `env_file`, `env` (merged key by key), `port`, `proxy_target`, `explorer`, and `ready_timeout_ms` of `dev`; any other key, and an invalid result, is a configuration error at the profile's key. `dev.env_file` names an env file, relative to the project root, that ktml dev reads itself (sh dotenv syntax as `set -a; . ./file` sets it: `KEY=value`, `export`, quotes, `$NAME`/`${NAME}`/`${NAME:-default}`; command substitution is refused) and sets for the application, under `dev.env` and then ktml dev's own `KTML_DEV_*` control variables, which neither may set. Errors name the line, never a value. A run that loads an env file prints a warning that browser actions may change production. `ktml dev --dry-run` prints the resolved profile, proxy and target, build, command, Explorer, picker hotkey, ready timeout, env file path, and the names and sources (never the values) of the application's variables, then exits 0 without building, starting, or connecting anything.

The watcher classifies each changed file by its name against the `dev` globs (matched against the file name, as `filepath.Match` does). `watch` matches (`.ktml` and `.ktmlschema` sources; other files are ignored) are validated, generated with their transitive dependents, and patched in place or rebuilt and reloaded. `restart_watch` matches (such as `*.go`) rebuild and restart the backend and reload every page; `.ktml` and `.ktmlschema` sources changed in the same 50 ms batch (an editor's save-all, `git checkout`, `git stash`) are generated first, so the rebuild compiles their fresh output, and the batch reloads pages once. A source that fails to validate or generate keeps its error overlay and is retried with the next change, while the backend still restarts. `reload_watch` matches (static assets such as `*.css` or `*.svg`) need no generation or rebuild: every page swaps a changed stylesheet in place (each same-origin `<link rel="stylesheet">` with the same file name, or every same-origin one when none matches, with a cache-busting query, keeping page state) and reloads for any other asset. A file matching `reload_watch` is never a `restart_watch` change, so moving `*.css` from `restart_watch` to `reload_watch` replaces a rebuild with a stylesheet swap; an asset the backend embeds at build time belongs in `restart_watch` instead. A `reload_watch` entry that is not a valid pattern is a configuration error at that entry.

The proxy injects its client as inline `<script data-ktml-client>` tags into HTML pages that have a `</head>`. When the page's `Content-Security-Policy` or `Content-Security-Policy-Report-Only` header restricts script elements, ktml dev generates a nonce for that response, puts it on every injected script, and adds `'nonce-…'` to the directive that governs script elements (`script-src-elem`, else `script-src`, else `default-src`) of each policy, in every header and every comma-separated policy; it drops a `'none'` it joins, and leaves every other directive as it is. A directive that already allows inline scripts (`'unsafe-inline'` without a nonce, hash, or `'strict-dynamic'`) is left unchanged, since a nonce would switch `'unsafe-inline'` off for the page's own scripts. A policy set in a `<meta http-equiv="Content-Security-Policy">` tag cannot be amended: ktml dev logs one warning, and if it restricts scripts the browser blocks live reload, patching, and the picker. The client styles its overlay and picker through the CSSOM, which `style-src` does not restrict.

`ktml dev [--port <port>] [--proxy-target <url>] [--explorer-addr <host:port>]` overrides `dev.port`, `dev.proxy_target`, and `dev.explorer.addr` over the chosen profile, so a Makefile's port variables can drive ktml dev. Each is validated as its key is (`--port` 1–65535; `--proxy-target` loopback unless `dev.allow_remote_proxy_target`, and then https; `--explorer-addr` a loopback `host:port`, port 0 picking a free one), and an invalid value exits 1 with an error naming the flag. `--explorer-addr` serves the Explorer even without `dev.explorer`. `{target_port}` in `dev.command`, `dev.build`, or a `dev.env` value (and in a profile's) is replaced by the port of the resolved `dev.proxy_target` (its scheme's default when it names none), so the application listens where the proxy forwards: `"command": ["{binary}", "-addr", "127.0.0.1:{target_port}"]`. `--dry-run` prints the result. `ktml explore -addr` sets the standalone Explorer's address.

With `dev.explorer` set, pages ktml dev proxies also get the component picker, unless `dev.picker.disabled` is `true`. `dev.picker.hotkey` (default `Alt+Shift+K`: Ctrl, Alt, or Meta, optionally Shift, and a letter, digit, or F1–F12) toggles pick mode; Esc leaves it. Hovering highlights the innermost component instance (its `data-ktml-i` element or `ktml:c` range) and labels its component chain, `Layout › Page › **Panel**`; Shift+wheel up selects the enclosing instance and down goes back toward the innermost, one level per gesture, without scrolling the page (Alt+Shift+Up/Down do the same); moving to another instance resets the level. A click, which never reaches the page, opens the selected component in one reused Explorer tab (`ktml-explorer`) at `/_explorer/open?component=<import path>.<Name>`, which redirects to its story. A component without a story first gets one written into its `.story.ktml` from its input types only (the `ktml stories generate` synthesis, under `// ktml:generated`), never from the page's values. The page's Render Identities go to ktml dev's `POST /__ktml/picker/resolve`, which names their components from the Explorer's program and, for a root with no frames, the app's token-guarded `POST /__ktml/dev/components` (component IDs only, no props); `POST /__ktml/picker/open` asks the Explorer for the story. Both accept only a same-origin `application/json` POST to a loopback Host.

Every command logs to stderr at warning level and above. `KTML_LOG_LEVEL` (`debug`, `info`, `warn`, or `error`) sets the level for any command, such as `ktml dev` or `ktml lsp`; `ktml generate -v` raises it to debug. Command output (such as `Generation complete` or the `-check` listing) goes to stdout, and errors to stderr.

### 1.1. Generate backends
`resolution.generate` selects the backend for `.ktml` component documents:

- **`go` (the default).** The native production Go generator (`gogen`, ADR-0003). It is used when `resolution.generate` is absent or empty. `foo.ktml` becomes `foo_ktml.go` (`//go:build !ktmldev`) and `foo_ktml_dev.go` (`//go:build ktmldev`) in the Go package of the source directory, with the frozen ADR-0003 Hybrid H API: each component is a struct `X` whose value is its `ktml.Component`. The package name comes from the directory's handwritten Go files (or the directory name), and the import path from the nearest `go.mod`. Generation fails, writing nothing, if a generated name collides with a handwritten declaration in that package. The name `go` is reserved: a plugin cannot use it.
- **A named plugin**, such as the experimental `gohtml` backend. It must be named explicitly. A lone configured plugin is no longer selected implicitly, because a project may configure plugins only for formatting or LSP features.

`.ktmlschema` documents always use the native generator. Until the HMR registry work lands (PLAN items 10 and 11), `ktml dev` can patch components in place only with the `gohtml` backend; with `go`, a component change regenerates, rebuilds, and reloads.

## 2. Plugin Architecture (Wasm + WASI)
Plugins are compiled to WebAssembly (`.wasm`) and executed using a runtime like `wazero` within the Go host. 

### 2.1. Sandboxing and WASI
Plugins are sandboxed by default. They do not have network access. They are granted restricted, read-only filesystem access via WASI based on the user's `ktml.config.json`. This allows plugins to scan project directories without compromising security. A plugin's stdout and stderr never reach the terminal or the LSP stream: the host logs them a line at a time at debug level, prefixed with the plugin and stream, with control characters and invalid UTF-8 escaped, lines bounded to 4 KiB, and at most 100 lines a second per stream (the rest are counted as dropped).

### 2.2. The Wasm ABI (Application Binary Interface)
Every plugin must export specific Wasm functions. The host communicates with plugins by writing JSON or Protobuf payloads into Wasm memory and calling these exports:

- `ktml_manifest() -> ptr`: Returns a JSON manifest defining the plugin's name, version, and supported capabilities (`generate`, `format`, `lsp`).
- `ktml_generate(ast_ptr, ast_len, config_ptr, config_len) -> ptr`: Accepts the KTML AST and a configuration payload (for environment flags like dev vs prod) and returns the generated primary target code string.
- `ktml_format(ast_ptr, ast_len) -> ptr`: Accepts the AST/source and returns the formatted KTML string.
- `ktml_lsp_handle(method_ptr, payload_ptr) -> ptr`: Handles specific LSP methods (e.g., `textDocument/completion`) and returns LSP-compliant JSON responses.

The manifest's `abi` names the ABI version. The host loads ABI 1 and ABI 2 plugins; ABI 2 adds, without changing ABI 1's exports:
- **The `symbol-index` capability** (ABI 2 only). The host sends `$/ktml/index` requests through `ktml_lsp_handle`, and only to plugins that declare it. A plugin answers with the documents it indexed, and the host merges them into the language server's workspace symbol index ([Symbol index](docs/lsp/symbol-index.md)).
- **A guest working directory.** `PWD` is the project root, so relative paths and `os.Getwd` name the host's absolute project paths. It applies to ABI 1 plugins too.

### 2.3. Host Functions and Side Effects
While plugins are restricted to read-only WASI access for security and determinism, they often need to generate auxiliary files (e.g., CSS, JS, or framework-specific routing files). To achieve this, plugins must import and call Host Functions provided by the KTML CLI.

- `ktml_host_write_file(path_ptr, path_len, content_ptr, content_len)`: Requests the Host to write a file to the filesystem.

### 2.4. Host's Responsibilities for File Generation
The KTML Host CLI intercepts all `ktml_host_write_file` calls to ensure a secure, robust developer experience. The Host is responsible for:
1. **Security Enforcement:** Every primary output, auxiliary file, and stale-output deletion is confined, and refused before anything is written otherwise. The error names the plugin and the project-relative path, such as `plugin "gohtml" may not write .git/hooks/pre-commit: version control (.git) is never generated`.
   - **Declared outputs.** A plugin writes only in the directories its `plugin.<name>.outputs` declares (§4). Each entry is `{"dir": "<project-relative dir>", "suffixes": [...]}`. `dir` matches whole path segments: `gen` admits `gen/a.go` but not `generated/a.go` or `gen.go`. `"."` is the whole project, and `suffixes`, when given, restricts the entry to file names ending in one of them. A plugin without `outputs` may not write. The native Go generator is confined the same way, to `*_ktml.go`, `*_ktml_dev.go`, and `*_ktmlschema.go`.
   - **Path checks.** Paths are checked after `..` cleaning and symlink evaluation, against the canonical project-relative path. Absolute paths, paths that escape the project, and symlinked output files are rejected.
   - **Deny-list.** Whatever a plugin declares, the host never writes or deletes:
     - anything in a `.git` or `.ktml` directory (the host saves its own `.ktml/state.json` directly);
     - `ktml.config.json`, `go.mod`, `go.sum`, `go.work`, `go.work.sum`, `.gitignore`, `.gitattributes`, and `.gitmodules`;
     - KTML sources (`.ktml`, `.story.ktml`, `.ktmlschema`);
     - an existing `.go` file without the `// Code generated ... DO NOT EDIT.` header.

     Names compare case-insensitively.
   - **Config validation.** An `outputs` dir outside the project or inside `.git` or `.ktml`, or a suffix that names a KTML source or contains a separator, is a configuration error at its value.
2. **Build Caching:** Tracking generated auxiliary files so they can be cached and incrementally rebuilt only when necessary.
3. **Cleanup:** Automatically cleaning up orphaned or outdated auxiliary files during `ktml dev` when the original `.ktml` files are modified or deleted. Build state records each entry's writer (the plugin name, or `go` for the native generator). A stale output is deleted only if all of these hold:
   - its recorded hash still matches;
   - it lies inside that writer's declared outputs and off the deny-list;
   - the writer is still configured.

   Otherwise the batch fails and rolls back, naming the writer and the path.

### 2.5. The Generation Configuration Payload
To generate code effectively, particularly when managing auxiliary files, plugins need contextual information about the run state and user preferences. The Host serializes this information into a JSON payload, writes it to Wasm memory, and passes its pointer and length (`config_ptr`, `config_len`) to `ktml_generate`.

The configuration payload has the following structure:

```json
{
  "environment": "development", 
  "file_path": "/absolute/path/to/project/src/components/button.ktml",
  "settings": {
    "framework": "tailwind",
    "custom_struct_tags": ["json", "db"]
  }
}
```

- **`environment`**: Indicates the execution context (e.g., `"development"` for hot-reloading `ktml dev` sessions, or `"production"` for an optimized `ktml build` run).
- **`file_path`**: The absolute path to the specific `.ktml` file currently being parsed. This is essential for plugins to namespace generated types or derive predictable auxiliary file names.
- **`settings`**: An arbitrary key-value object containing the user preferences defined specifically for this plugin in `ktml.config.json`. This allows the plugin to be highly customizable without requiring hardcoded CLI flags.

## 3. LSP Multiplexing and Proxying
To prevent plugins from reinventing the wheel, the KTML CLI features an **LSP Proxy Engine**.

### 3.1. Downstream (IDE to Plugins)
When the IDE sends an LSP request (e.g., Go-to-Definition) to the KTML CLI:
1. The CLI broadcasts the request to all Wasm plugins that registered the `lsp` capability.
2. The plugins process the request (using their WASI file access or AST analysis).
3. The CLI merges the responses from all plugins and returns a unified response to the IDE.

### 3.2. Upstream (Plugins to External LSPs)
Plugins can ask the Host CLI to query external language servers. 
1. The plugin yields execution back to the Host with a special payload: `{"action": "proxy_lsp", "target": "gopls", "payload": {...}}`.
2. The Go Host sends the request to the running lsp instance on the user's machine.
3. The Host receives the response from the lsp and passes it back into the Wasm plugin's memory.
4. The plugin formats the lsp data and returns it to the Host.

## 4. Configuration (`ktml.config.json`)
The CLI is configured per-workspace. The configuration dictates which plugins are loaded, their WASI permissions, and how conflicts are resolved.

```json
{
  "package_dir": "./ktml",
  "lsp": {
    "gopls": { "command": ["gopls"] }
  },
  "plugin": {
    "go-plugin": {
      "path": "./plugins/ktml-go.wasm",
      "outputs": [{"dir": ".", "suffixes": [".html", ".ktml.go"]}]
    },
    "chi": {
      "path": "./plugins/ktml-chi.wasm",
      "allow_read": ["./internal/api", "./cmd"]
    },
    "htmx": {
      "path": "./plugins/ktml-htmx.wasm"
    }
  },
  "resolution": {
    "generate": "go-plugin",
    "format": ["go-plugin"],
    "lsp": ["*"]
  }
}
```

`outputs` declares where a plugin may write generated files (§2.4). Plugins used only for formatting or LSP features declare none.

A `tailwind` section enables the Tailwind CSS adapter of `ktml lsp`; see
[docs/lsp/tailwind.md](docs/lsp/tailwind.md):

```json
{
  "tailwind": {
    "command": ["tailwindcss-language-server", "--stdio"],
    "settings": {}
  }
}
```

The `gopls` section configures the gopls adapter of `ktml lsp`, which is
on by default in a Go module; see [docs/lsp/gopls.md](docs/lsp/gopls.md).
`remote` names the shared gopls daemon: `"auto"` (the default),
`"unix;/path"`, `"host:port"`, or `"off"` for a private gopls. An
unreachable daemon falls back to a private gopls.

```json
{
  "gopls": {
    "command": ["gopls"],
    "remote": "auto",
    "disabled": false
  }
}
```

The `html` section configures the HTML analyzer of `ktml lsp`; see
[docs/lsp/html-and-alpine.md](docs/lsp/html-and-alpine.md).
`extension_attributes` declares the `hx-*` attributes of the project's own
HTMX extensions, so the analyzer does not report them as
`html-unknown-attribute`. Each entry is a lower-case `hx-*` name without
`data-` or a `:modifier`; anything else is a configuration error at its
index. The host passes the list to every plugin as
`settings._ktml.html.extension_attributes`, beside `_ktml.path_map`.

```json
{
  "html": {
    "extension_attributes": ["hx-require"]
  }
}
```

`ktml classes [-o file] [-json] [-check] [paths...]` lists the Tailwind
class tokens of KTML sources, with the language server's extractor, for
Tailwind's `@source`.

## 5. The Generation Pipeline
To manage the complexity of auxiliary files and provide fast incremental builds, the Host CLI maintains a Build State manifest (e.g., `.ktml/state.json`). This state maps source `.ktml` files to their content hashes, primary outputs, and auxiliary outputs.

1. **File Watcher/Traversal:** The Go Host detects a modified or new `.ktml` file (or a deletion). If a deletion is detected, the Host reads the build state and immediately deletes all previously generated primary and auxiliary files associated with that source file.
2. **Cache Check:** For modifications, the Host calculates the hash of the `.ktml` file and compares it against `.ktml/state.json`. If the hash is unchanged, generation is skipped.
3. **Parsing & Routing:** The Host parses the file into a standardized KTML Abstract Syntax Tree (AST) and checks `resolution.generate` to find the target compiler plugin.
4. **Execution & Interception:** The Host calls `ktml_generate` on the target Wasm plugin, passing the AST and the configuration payload. During execution, the Host intercepts any `ktml_host_write_file` calls, validating them and temporarily recording the requested auxiliary files in memory.
5. **Output & State Update:** The Host writes the returned primary target code (and intercepted auxiliary files) to disk. It then compares this new set of generated files against the old outputs recorded in the build state, deleting any old auxiliary files that the plugin no longer requires. Finally, it updates `.ktml/state.json` with the new hash and the current list of generated files.
