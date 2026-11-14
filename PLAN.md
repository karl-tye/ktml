# KTML Implementation Plan

## Goal

Replace templ in production Go web applications with KTML while making KTML a
faster UI development environment for Go applications using Chi, HTMX,
Alpine.js, and Tailwind.

KTML will provide:

- A small declarative component language with typed inputs.
- Simple conditions and keyed loops over prepared view models.
- A development interpreter with component hot module replacement (HMR).
- Direct, type-safe Go renderers for production, with no runtime template parsing.
- A federated LSP for KTML, Go, HTML, HTMX/Chi, Alpine.js, and Tailwind.
- A Component Explorer driven by colocated `.story.ktml` files.
- A templ migration command that generates KTML, Host Schemas, presenter
  scaffolds, and actionable diagnostics.

HTML is the first production target. XML templates (for example Hyperview
screens) stay on templ until an explicit XML output mode is designed and
implemented.

"The host application" below means a Go application that adopts KTML. KTML was
developed against a large production templ application, which converted all of
its HTML templates in place; the numbers in the design documents come from that
work.

## Product Decisions

The following decisions guide this plan:

- KTML will use a new Go rendering API rather than preserve `templ.Component`.
- A host application's production binaries must not depend on templ once they
  cut over.
- Templates may use composition, field access, simple conditions, and keyed
  loops. Business logic belongs in typed Go view models.
- Dedicated `.ktmlschema` Host Schemas will define the nominal records and closed
  Presentation States visible to templates. Generated Go packages are the only
  nominal values accepted by production renderers.
- Components may select one finite input through contiguous, order-independent
  Render Clauses. Bodyless signatures are exhaustive; residual rendering requires
  an explicit `default` body and reports fallthrough warnings.
- Conditions are limited to boolean composition and optional presence. Go derives
  comparisons, permissions, formatting, filtering, sorting, grouping, and state.
- Keyed Repetition is the only dynamic collection construct. It iterates a
  prepared collection path and requires a string or signed-integer item key.
- Development rendering may use an interpreter, but interpreter output must be
  tested for parity with production-generated output.
- Production rendering will be generated Go with no runtime template parsing.
- Compatible component changes use HMR. Incompatible API or backend changes
  rebuild the Go server and reload the browser.
- Optional or defaulted input additions are HMR-compatible. Required input
  additions, removals, renames, and type changes are incompatible.
- HMR uses stable development-only component markers and requires keys for
  dynamic loop items.
- Live component props remain in a bounded server-side render registry and are
  never embedded as browser-readable JSON.
- The compiler and tree-sitter implementations will conform to one formal
  language specification and test corpus.
- KTML exposes one federated LSP connection. Core semantic tooling is native;
  external language servers and optional plugins are adapters behind it.
- The HTMX plugin discovers standard Chi routes from a minimally configured Go
  source directory. Matcher overrides are only needed for custom router wrappers.
- Plugins are trusted and pinned initially, but obvious filesystem and resource
  hazards must be fixed before production use.
- Official HTMX, Alpine.js, and Tailwind integrations remain Library Plugins over
  generic schemas and tooling hooks. Library Plugins cannot add KTML syntax,
  types, control flow, render transforms, or output bytes.
- Host applications may commit the generated KTML Go, as they commit templ's
  output today, guarded by a staleness check (`ktml generate -check`) in CI:
  a clean checkout builds without running KTML, and the check fails when
  committed output is missing, changed, or stale. Generating during CI and
  Docker builds stays supported for projects that do not commit output.
- KTML is published as a public Go module, `github.com/karl-tye/ktml`, so
  host applications pin a version instead of a local `replace`.
- Production gates apply per binary: a binary may cut over to KTML once its
  own templates, tests, and production checks pass, while other binaries stay
  on templ.
- The first Component Explorer release requires typed controls. Interaction
  tests, visual snapshots, and accessibility automation follow later.
- Production cutover requires complete HTML migration of the binary, output
  parity, security review, IDE completeness, and removal of the templ runtime.

## Current State

KTML is early and unstable: the language, CLI, and Go API may change before
1.0. Phases 0 to 8 below are done in their first form, and a production templ
application has been converted end to end on a branch (every HTML template,
with the release gates passing). Production use by that application starts
binary by binary once KTML is tagged.

Real templ applications use embedded Go, conditions, loops, switches, dynamic
components, attribute spreads, safe URL/JSON helpers, scripts, and request
context. Migration is therefore a semantic refactor into KTML, Host Schemas,
and Go presenters, not a syntax-only rewrite.

## Target Architecture

```text
                            .ktml
                              |
                       canonical parser
                              |
                     typed semantic model
                   /          |           \
          dev interpreter   federated    production IR
                  |            LSP             |
         render registry       |         Go generator
                  |            |               |
             SSE DOM patch   editor       compiled binary

.templ -> templ parser -> migration IR -> .ktml + .ktmlschema + Go presenters
```

The syntax parser, semantic model, interpreter, production generator, formatter,
LSP, migration tool, and Component Explorer must share the same type and symbol
model. Backend-specific behavior must not be encoded in the syntax AST.

## Language and Compiler

### Supported language model

The accepted next contract is [ADR-0002](docs/adr/0002-typed-render-selection.md)
and [Host Schema, Render Clauses, and Keyed Repetition v1](docs/language/host-schema-render-clauses-keyed-repetition-v1.md).
It is a target contract, not a claim that the current implementation supports
these features.

KTML must support:

- Typed required, optional, and defaulted component inputs.
- Language-neutral nominal records and closed Presentation States generated into
  typed Go packages.
- Component composition and children/slots.
- Field access on prepared view-model values.
- Boolean/presence conditions with `else` branches.
- One public Component Signature with finite Render Clauses over one selected
  state, boolean, or optional input.
- Keyed iteration over prepared collections.
- Literal, typed dynamic, conditional boolean, and conditional valued
  attributes.
- Explicit attribute forwarding with deterministic collision rules.
- Raw-text `script` and `style` bodies.
- `*` in attribute names (htmx `response-targets`).
- Context-aware text, attribute, URL, CSS, JSON, and script values.
- HTML doctypes and comments.
- Cross-file package imports with aliases and a typed symbol table.

KTML must reject:

- Arbitrary Go statements or mutation.
- Database, service, or request calls from templates.
- Arbitrary method invocation.
- Scalar comparisons, formatting, filtering, sorting, grouping, or state
  derivation.
- Immediately invoked functions.
- Unsafe raw script interpolation.
- Unkeyed dynamic loops in HMR-capable components.
- Guards, wildcard/literal/multi-input Patterns, open states, recursive rendering,
  component-valued data inputs, loop indices/else/transforms, and plugin-defined
  language dialects.

### Host Schema and Business Derivation

`.ktmlschema` Documents are the source of truth for lowerCamel nominal record
fields, collections, optionals, contextual values, and closed Presentation
States. Schema packages generate exported Go record values plus sealed state
interfaces with private Variants and typed constructors/accessors. Generated Go
names uppercase only the first ASCII letter; `id` becomes `Id`, with no
initialism dictionary or target-specific annotation. Optional values remain
pointer-backed in generated Go. `integer` is a signed 64-bit scalar and the only
numeric Render Identity key type.

Handlers or page/fragment presenters perform Business Derivation. They map
domain and request values into generated presentation records, select states and
permissions, format domain values, prepare stable keys, and construct URL, JSON,
and trusted values. Literal copy and CSS classes stay in KTML. Request context is
never an implicit expression scope; request-derived values are explicit
Component Signature inputs.

The first proof is a checkout payment lifecycle with nullary and payload
Variants, distinct SSR shapes, and trusted URL boundaries. Before expanding Host
Schema use, compare its presenter glue and invalid-state elimination with the
equivalent templ/Go implementation. If the schema adds boilerplate without
removing meaningful invalid states or improving tooling, reconsider the source
of truth before wider rollout.

### Render Clauses

Render Clauses follow one contiguous, same-Document Component Signature:

```ktml
SearchResults [state: SearchState, query: string]

SearchResults [state = Loading] {
  SearchSkeleton
}

SearchResults [state = Loaded, items, total] {
  Results items=items total=total query=query
}
```

Every clause matches the same sole selector. Patterns are contextual or
qualified closed Variants, booleans, `Some`, or `None`; payload bindings are
partial, immutable, and typed from the Host Schema. Clauses cannot have guards,
overlap, depend on order, resume after another declaration, cross Documents, or
extend imports. All signature inputs remain in scope. Clause groups explicitly
declare children and named slots in their one shared signature.

A bodyless signature requires exhaustive clauses. A residual body is explicit:

```ktml
SearchResults [state: SearchState] default {
  GenericResults state=state
}
```

Variant, boolean, or option cases handled only by `default`, and currently
unreachable defaults, produce the same semantic warnings in CLI, LSP, CI, and
Explorer. Nil or invalid states return a typed error before that component
writes; they do not select `default` or render empty output.

### Keyed Repetition

Keyed Repetition retains one structural form:

```ktml
for item in group.items key item.id {
  Result item=item
}
```

The collection is a `[T]` path. The key is the string/integer scalar item or a
path rooted in it. Go prepares composite keys and performs collection filtering,
sorting, and grouping. There is no index, loop `else`, range, iterator protocol,
collection expression, or composite-key syntax. Bodies may contain arbitrary
declarative fragments, conditions, and independently keyed nested repetitions.

Every executed repetition preflights sibling key uniqueness with one set, then
streams a second pass without buffering body HTML. Empty collections render
nothing. Development uses fragment boundaries whose Render Identity contains
component identity, repetition source identity, the current key, and all
ancestor keys.
Production emits no markers or key attributes. Keyed insertion, removal, and
reordering must move surviving fragments and preserve browser state where safe.

### Compiler layers

Implement distinct layers:

1. Source text and lossless concrete syntax tree.
2. Syntax AST with complete source ranges.
3. Name resolution and dependency graph.
4. Typed semantic IR independent of rendering backend.
5. Development interpreter or production code generation.

Diagnostics from parsing or semantic analysis must block generation. The last
valid output may remain active in development, but invalid source must never
produce new partial output.

### Shared grammar contract

Create a versioned language specification containing:

- Lexical and concrete grammar.
- Canonical AST schema.
- Type and semantic rules.
- Valid and invalid fixture corpus.
- Parser recovery expectations.
- Formatting and round-trip expectations.

The Go compiler parser and tree-sitter parser may remain separate
implementations, but both must pass the same normalized conformance corpus.

### Dependencies and incremental builds

Build a workspace dependency graph for:

- Component imports.
- Component invocation.
- Story-to-component references.
- Route-contract references.
- Generated Go package dependencies.

Cache keys must include:

- Source and transitive dependency hashes.
- Compiler version.
- Language/AST schema version.
- Plugin content hash and ABI version.
- Plugin settings and target environment.
- Output path and output content integrity.

State and generated outputs must be written atomically.

## Production Go Renderer

### API prototypes

Prototype and benchmark two public APIs before freezing the production contract.

Prototype A uses bound components:

```go
type Component interface {
	Render(context.Context, io.Writer) error
}

func ProductCard(props ProductCardProps) Component
```

Prototype B uses direct render functions:

```go
func RenderProductCard(
	ctx context.Context,
	w io.Writer,
	props ProductCardProps,
) error
```

Evaluate both against a host application's full-page renderer, HTMX fragments, nested children,
feature components, SSE/OOB fragments, and error handling. The likely final
shape is a small KTML `Component` interface backed by generated structs and
direct internal render functions, but the benchmark and migration prototype
must decide this.

### Generator requirements

Generated production renderers must:

- Parse nothing at runtime.
- Write directly to `io.Writer`.
- Avoid reflection and untyped input maps in render paths.
- Minimize allocations and temporary strings.
- Apply context-specific escaping.
- Correctly render HTML void and boolean attributes.
- Implement deterministic attribute ordering and collision rules.
- Generate deterministic code and source maps.
- Return all render errors.
- Strip development markers, registries, and Explorer integration. (Line
  directives in generated Go stay in production builds: they are
  compile-time position information, not development markers; decided
  2026-10-14.)
- Have no dependency on templ.

The gohtml plugin remains an optional or experimental backend and is not the
recommended production backend.

## Development Runtime and HMR

### Semantic change classification

Compare old and new typed semantic models and classify changes as follows:

| Change | Development action |
| --- | --- |
| Styling, text, or element-body change | Interpret and patch live instances |
| Optional/defaulted input added | Interpret and patch with default value |
| Existing default changed | Interpret and patch live instances |
| Internal condition or loop changed | Patch if component schema is compatible |
| Render Clause Pattern or body changed | Interpret and patch if Component Signature and Host Schema are unchanged |
| Required input added | Generate, rebuild, restart, and reload |
| Input removed or renamed | Generate, rebuild, restart, and reload |
| Input type changed | Generate, rebuild, restart, and reload |
| Incompatible imported schema change | Rebuild transitive dependents and reload |
| Any Host Schema change | Generate schema/renderers, rebuild, restart, invalidate incompatible instances, and reload |
| Go/view-model/domain source changed | Rebuild server and reload |
| Interpreter diagnostic | Keep last valid page and show error overlay |
| Production generator diagnostic | Keep last working server and show error overlay |

Only successfully rendered patches may be broadcast. Any failed or ambiguous
patch falls back to a full browser reload after a successful rebuild.

### Server-side render registry

Replace the browser-visible JSON cache with a typed development-only registry:

```go
type RenderInstance struct {
	SessionID   string
	InstanceID  string
	ComponentID ComponentID
	Schema      SchemaVersion
	Props       TypedValue
	Children    []RenderInstance
	RenderedAt  time.Time
}
```

The registry must:

- Keep values server-side.
- Scope entries to browser sessions and requests.
- Enforce TTL, entry count, and memory limits.
- Never serialize sensitive props into injected HTML.
- Invalidate entries on incompatible schema changes.
- Rerender every live instance of a changed component.
- Track component dependencies so parent instances can be rerendered.
- Remove entries when sessions disconnect or expire.

### Component identity and DOM patching

- Add development-only stable component-instance markers.
- Include explicit loop keys in repeated instance identity.
- Include the current key, repetition source identity, and the full ancestor-key
  chain in nested Render Identity.
- Use attributes for safe single-root components and boundary markers for
  multi-root components.
- Send rendered HTML in patch events, not component names.
- Morph DOM rather than using unconditional `replaceWith`.
- Preserve focus, input values, and unchanged Alpine state.
- Dispose removed Alpine scopes and initialize newly inserted scopes.
- Call `htmx.process` for inserted HTMX content.
- Preserve HTMX history and active requests where possible.
- Fall back to full reload when identity or morphing is unsafe.

### Interpreter parity

Every language feature must render through both the interpreter and generated Go
using shared fixtures. Compare exact bytes for escaping-sensitive output and
normalized DOM where only formatting may differ. No interpreter-only semantics
are allowed.

## Federated LSP and IDE Support

KTML exposes one standard LSP connection:

```text
Editor
  |
ktml lsp
  |
  +-- KTML syntax and semantic analyzer
  +-- HTML and accessibility analyzer
  +-- HTMX analyzer
  +-- HTMX plugin with Chi route discovery
  +-- Alpine.js analyzer
  +-- Tailwind language-server adapter
  +-- gopls adapter
```

Neovim is the first packaged editor integration, but the server and protocols
must remain editor-independent.

### KTML language features

- Incremental diagnostics.
- Completion and signature help.
- Hover.
- Definitions, references, and rename.
- Document and workspace symbols.
- Semantic tokens.
- Folding ranges.
- Code lenses such as component reference counts.
- `prepareRename` validation.
- Linked editing for paired syntax.
- Full, range, and on-type formatting.
- Code actions.
- Cross-file component discovery and import completion.
- Generated Go source mapping.

This subsumes the previous standalone LSP backlog, including the existing gap
where completion misses discoverable cross-file components.

### Symbol index

Replace whole-workspace textual walks and whole-word matching with a persistent,
incrementally updated workspace index containing:

- Components and declarations.
- Inputs, slots, and local loop bindings.
- Imports and aliases.
- References and call graph edges.
- Story declarations.
- Routes and route references.
- Embedded Alpine scopes.
- Extracted Tailwind class tokens.

Rename and references must operate on semantic symbols, never textual word
matches.

### HTMX and Chi route discovery

Configure Chi route discovery in the HTMX plugin's settings. Standard Chi usage
requires only a source directory:

```json
{
  "plugin": {
    "htmx": {
      "path": "./plugins/htmx.wasm",
      "allow_read": ["./internal/web"],
      "settings": {
        "chi": {
          "dir": "./internal/web"
        }
      }
    }
  }
}
```

The plugin must recognize standard `Get`, `Post`, `Put`, `Patch`, `Delete`,
`Handle`, `Route`, and `Mount` registrations without additional configuration.
It must compose nested `Route` and `Mount` prefixes into effective route paths.

For example:

```go
r.Route("/orders", func(r chi.Router) {
	r.Post("/{id}/approve", orders.ApproveOrder)
})
```

must produce `POST /orders/{id}/approve` in the route index.

Projects with custom router wrappers may override or extend the defaults using
AST matcher patterns:

```json
{
  "chi": {
    "dir": "./internal/web",
    "patterns": {
      "scope": [
        "{router}.Route({prefix}, {routes})"
      ],
      "mount": [
        "{router}.Mount({prefix}, {routes})"
      ],
      "routes": {
        "GET": [
          "{router}.Get({route}, {handler})"
        ],
        "POST": [
          "{router}.Post({route}, {handler})"
        ]
      }
    }
  }
}
```

Matcher patterns operate on the typed Go AST, not source text or regular
expressions. Named captures are required so the plugin can distinguish router,
route, handler, prefix, and nested route-function positions. Receiver names such
as `r` are not part of the contract.

The resulting route index contains method, effective path, path parameters,
handler symbol, and declaration location. The HTMX plugin uses it to provide:

- `hx-get`, `hx-post`, `hx-put`, `hx-patch`, and `hx-delete` completion.
- Method and route compatibility diagnostics.
- Missing, extra, or mismatched path-parameter diagnostics.
- Definition navigation from HTMX attributes to Chi registrations.
- References from Chi registrations to KTML consumers.
- Semantic route rename where both sides are statically represented.
- Explicit diagnostics when a dynamic route cannot be analyzed safely.

The minimal configuration is the primary API. Custom patterns are an escape
hatch and must not be required for ordinary Chi applications.

Implemented (item 12). Routers are recognised by type, through a Chi stub,
not by receiver name. The matcher schema is:

- `patterns.scope` and `patterns.mount` take `{router}`, `{prefix}`, and
  `{routes}`; mount also accepts `{handler}` in place of `{routes}`.
- `patterns.routes.<METHOD>` takes `{router}`, `{route}`, and `{handler}`.
  The key is an upper-case method or `*`.
- `{_}` matches any one expression, and `{...}` matches the remaining
  arguments.
- `patterns.defaults` (default `true`) keeps the built-in matchers. `false`
  replaces them.

Every violation is reported with its JSON path, and pattern errors also give
the column. `ktml lsp` publishes these errors, and core configuration
errors, as diagnostics on `ktml.config.json` at the offending value.

Decision (2026-10-13): the route index indexes every registration under
`chi.dir`. There is no exclude setting.

### Alpine.js

- Parse Alpine expressions rather than treating them as opaque strings.
- Model variables introduced by `x-data`, loops, and local scopes.
- Support completion, hover, definitions, references, and scoped rename.
- Complete directives, events, and modifiers.
- Diagnose invalid scope references and directive combinations.
- Model common literal object, array, boolean, and component-input types.
- Understand Alpine lifecycle boundaries created by HTMX swaps and KTML HMR.

Complex JavaScript should live in imported modules; complete TypeScript inference
inside inline Alpine expressions is not an initial requirement.

### Tailwind

- Adapt the Tailwind language server through virtual KTML documents.
- Extract static and supported conditional class tokens.
- Provide completion, hover, diagnostics, and theme definition navigation.
- Warn about class construction that cannot be statically discovered.
- Share class extraction between LSP, production builds, and Explorer.

## Plugin Platform

Core compiler, KTML semantics, HMR, and LSP federation should be native Go.
Framework integrations such as HTMX may be first-party plugins. External
language servers run behind native adapters. WASM remains suitable for pinned
generators, formatters, and analyzers.

Before production use:

- Add a versioned manifest and ABI.
- Enforce declared capabilities.
- Declare permitted source mounts and output roots.
- Reject absolute and escaping output paths.
- Validate paths after symlink evaluation.
- Restrict stale-output cleanup to generated roots.
- Mount source directories read-only.
- Add execution deadlines and cancellation.
- Add memory and output-size limits.
- Include plugin hashes and settings in build cache keys.
- Remove filesystem mutation from package initialization.
- Make all generated writes and state updates atomic.

Status (2026-10-12). Done and enforced by the host:

- An abi-1 manifest with declared capabilities.
- Read-only `allow_read` mounts.
- A deadline and the caller's cancellation on every guest call; a stopped
  instance restarts.
- Memory, result-size, and auxiliary-output limits, configurable in
  `plugin.<name>.limits`.
- Plugin hashes and settings in generation cache keys.
- No filesystem mutation in package `init`, guarded by a test.

Output roots, path containment after symlink evaluation, and atomic writes
were already handled in `buildstate`; they were not re-audited.

Status (2026-10-15): output roots are declared and enforced (decided:
declared prefixes plus a host deny-list).
- **Declared outputs.** Each plugin declares `plugin.<name>.outputs`:
  `{dir, suffixes}` entries, matched by whole path segments on the canonical
  path after symlink evaluation. Writes and stale cleanup outside them are
  refused, and so is any write by a plugin that declares none.
- **Deny-list.** Whatever a plugin declares, the host never writes or deletes
  `.git/`, `.ktml/`, `ktml.config.json` and the Go/git config files, KTML
  sources, or handwritten `.go` files.
- **Cleanup.** Build state records each entry's writer, and stale cleanup is
  confined to it.
- **Diagnostics** name the plugin and the path (cli-spec §2.4).

Decision (2026-10-12): deadlines stay on for every plugin, with no opt-out
and no LSP exemption, although wazero's termination checks cost about 5x
guest CPU. The cost is hidden instead:

- Compiled plugins persist in the user cache.
- Long plugin work runs as host background jobs on a worker instance, so
  requests never wait for it.
- The HTMX route index refreshes only the Go files the editor reports
  changed.

Initial capability vocabulary:

```text
generate
format
completion
semantic-tokens
embedded-language
route-provider
class-provider
story-control
```

## Component Explorer

The first expanded Explorer release is intentionally focused on typed controls.

### Version one

- Discover colocated `.story.ktml` files.
- Type-check stories against component schemas.
- Derive controls for required, optional, defaulted, enum, boolean, number,
  string, object, and collection inputs.
- Derive Variant-aware controls for Presentation States: select a Variant, then
  edit its typed payload fields. Optionals expose `None`/`Some`; trusted values
  require explicit fixtures and are never unrestricted text controls.
- Render through the same development interpreter.
- Apply story edits using the same HMR path.
- Isolate previews in an iframe.
- Bundle Alpine, HTMX, and styling assets locally rather than using CDNs.
- Display syntax, type, and render diagnostics inline.
- Bind to loopback and run only in development.

Later releases may add interaction tests, visual snapshots, accessibility checks,
viewport/theme controls, documentation pages, and HTMX route mocks.

## Templ Migration Tool

### Command

```bash
ktml migrate templ ./internal/... \
  --report .ktml/migration.json \
  --scaffold-presenters
```

Use the version-pinned templ `parser/v2` AST as source. Generated `_templ.go`
files are behavioral references, not the primary parser input.

### Outputs

For each input file, produce:

- Editable `.ktml` source.
- Editable `.ktmlschema` Host Schema declarations where nominal records or
  Presentation States are needed.
- Page/fragment presenter scaffolds that map existing Go values into generated
  Host Schema packages.
- Source maps.
- A machine-readable migration report.
- Source-ranged diagnostics.
- Suggested Go call-site edits.
- Explicit security review markers for trusted URL, JSON, and script behavior.

Every templ construct receives one status:

```text
converted
presentation_scaffolded
review_required
blocked
```

`presentation_scaffolded` means the construct required Host Schema declarations,
presenter code, or both. With `--scaffold-presenters`, the converter emits every
required Host Schema alongside its presenter scaffold.

The converter must return a nonzero status if any source node is dropped,
partially parsed, or blocked.

### Conversion policy

Convert mechanically:

- Static elements, text, comments, and doctypes.
- Static and simple typed attributes.
- Component declarations and composition.
- Simple field interpolation.
- Simple conditions.
- Loops when a stable key can be identified.
- Inline `<script>`/`<style>` bodies, as raw-text literals
  (`` ``` … ``` ``).
- Conditional valued attributes (`if c { name="v" }`), as conditional
  attribute groups in a parenthesized list.
- `templ.Attributes` inputs and their `{ attrs... }` spreads, as
  `Attributes` inputs forwarded with `...attrs`. KTML drops a forwarded
  name the element writes itself; with templ's spread last, browsers did
  the same.
- `templ.JSFuncCall` with literal arguments, as a static script.

See the [migration-language contract](docs/language/migration-language-v1.md#conversion-rules).

Scaffold Host Schema fields and presenter derivation for:

- Method calls and formatting.
- URL construction.
- Filtering, sorting, and local initialization.
- Computed classes.
- Conditional component selection.
- Request-context-derived presentation values.
- Mutually exclusive rendering shapes that require a closed Presentation State
  and Variant payloads.
  A `switch` over a closed set (a named-type tag, every case a constant of
  that type) becomes a clause group over a presenter-selected Presentation
  State, with `Other` for the default or unmatched values; other switches
  stay presenter booleans with a review marker (done 2026-10-20).
- JavaScript built in templ script attributes (`fmt.Sprintf` or concatenation
  in `@click`, `:disabled`, `x-data`, `onclick`, `hx-trigger`, …). Script
  attributes accept only presenter-built `JSONValue` (`ktml.JSON`),
  `TrustedScript` (`ktml.TrustScript`), or booleans (ADR-0001 amendment of
  2026-10-12), in expression positions (amendment of 2026-10-13).
- Go values in inline scripts, `templ.JSONScript`, and `templ.JSFuncCall`
  with data arguments: presenter-built `JSONValue` values, in a JSON data
  element or as arguments in a static script.
- `style={ … }` and `templ.KV` styles: a presenter-built `CSSValue`
  (`ktml.CSS`), or a conditional literal `style`.
- Spreads of Go-built `templ.Attributes`: a presenter-built
  `ktml.Attributes`, from the context-checked constructors.
- Conditions of conditional attributes that Go derives: a presenter boolean.
- Clock reads in templates (`time.Now()`): presenter-provided values, like
  request-context values.
- Loop keys the converter cannot identify: presenter-built keyed collections
  with presenter-chosen keys (the common case).

Require review for:

- `templ.SafeURL`, `templ.SafeCSS`, `templ.JSUnsafeFuncCall`, and other
  trust boundaries.
- Inline scripts that contain three backticks (they move to static files).
- `templ.NewOnceHandle`. It is not a language feature: move the guarded
  script into the static bundle.
- Dynamic `templ.Component` values.
- Functions returning components.
- Immediately invoked Go functions.
- Unkeyed loops without stable identity.
- Hyperview/XML templates.

### Go call-site codemod

Add a Go AST codemod for:

- Component constructor calls.
- `.Render(ctx, w)` calls.
- `templ.WithChildren`.
- `templ.Component` fields and parameters.
- Variadic component collections.
- Imports and renderer abstractions.

Dynamic component storage or construction must emit diagnostics instead of being
rewritten speculatively.

## Host Application Integration

### Renderer boundary

The host application's page renderer (for example a `pages.go` in its web
package) is refactored around the KTML production API. It must support:

- Full pages and layouts.
- HTMX fragments.
- Nested children and feature components.
- SSE and out-of-band fragments.
- Request context propagation.
- Heterogeneous component composition.
- Complete error reporting.

Keep HTTP behavior in the web package and domain-specific components in their
existing domain packages.

### View models

Declare presentation records and states in Host Schema packages near their
domain components. Generate their Go representation, then use handler-level or
fragment-level presenters to map domain models into those values before
rendering. Domain entities must not implement KTML-specific interfaces merely
for data passing, and runtime reflection/structural mapping is not accepted.

### Build and development workflow

Two workflows are supported.

Committed output: the application commits the generated KTML Go and CI runs
`go tool ktml generate -check` over the converted packages; Docker builds the
committed Go.

Generated in the build: production builds run pinned KTML generation before Go
compilation, and Docker and CI generate from a clean checkout:

```make
generate-ui:
	ktml generate ./internal/...

build-assets:
	npm run build

build: generate-ui build-assets
	go build ./cmd/server
```

`ktml dev` orchestrates:

- KTML watching and interpretation.
- Go generation and incremental rebuilding.
- Chi server process restart.
- Tailwind watching.
- esbuild watching.
- Development reverse proxy and SSE.
- Error overlay.
- Component Explorer.

## Testing Strategy

### Compiler and language

- Shared parser/tree-sitter conformance corpus.
- Invalid syntax and recovery tests.
- Formatter idempotence and parse/format/parse equivalence.
- Host Schema, Render Clause, typed path, payload binding, exhaustiveness,
  overlap, import, and dependency graph tests.
- Deterministic generation and invalidation tests.

### Rendering

- Interpreter/generated-Go differential fixtures for every language feature.
- Exact output tests for scripts, JSON, URLs, attributes, whitespace-sensitive
  content, and escaping.
- DOM-normalized comparisons for formatting-insensitive HTML.
- Attribute collision and boolean-attribute matrices.
- Adversarial injection cases for every output context.
- Benchmarks for static, nested, list-heavy, form-heavy, and script-heavy UI.
- Keyed Repetition benchmarks against equivalent handwritten Go and templ,
  including allocation counts and generated-source inspection for one preflight
  set and no body buffering.

### HMR

- Semantic compatibility classification tests.
- Multiple live instances of one component.
- Parent/dependency rerendering.
- Keyed insertion, deletion, and reordering.
- Focus and form-value preservation.
- Alpine state preservation and disposal.
- HTMX processing after patches.
- Registry expiry and memory bounds.
- Failed-render fallback behavior.

### LSP

- Golden diagnostics, completion, hover, definition, reference, and rename tests.
- Cross-language Host Schema/KTML symbol tests and generated-Go source maps
  (gopls adapter tests pin gopls v0.23.0 and skip without it).
- Incremental workspace-index updates.
- Cross-file import and component resolution.
- Alpine scope and Tailwind virtual-document tests.
- HTMX-to-Chi route discovery, validation, references, and navigation.
- Standard LSP protocol/concurrency tests.

### Migration

- Assert every templ AST node is classified.
- Compile generated KTML, Host Schema packages, and presenter scaffolds.
- Compare old templ and new KTML output using deterministic fixtures.
- Run the host application's affected package and handler tests after each
  migration batch.
- Run browser tests for HTMX swaps, OOB fragments, SSE, Alpine behavior, and
  layouts before cutover.


## Delivery Phases

| Phase | Scope | Status |
| --- | --- | --- |
| 0 | Contracts and baselines: ADRs, conformance corpus, templ output fixtures and benchmarks | Done |
| 1 | Compiler foundation: lossless syntax, typed semantics, diagnostics, formatter, tree-sitter alignment | Done |
| 2 | IDE-first milestone: federated LSP, HTML/Alpine analysis, HTMX/Chi routes, Tailwind, Neovim | Done |
| 3 | Development interpreter and HMR: semantic change classification, render registry, DOM patching | Done |
| 4 | Production generator: direct Go renderers, benchmarks against templ and handwritten Go | Done |
| 5 | Component Explorer: `.story.ktml`, typed controls, `ktml stories generate` | Done |
| 6 | Templ converter: inventory, KTML/Host Schema/presenter emission, in-place mode, call-site codemod | Done |
| 7 | Host HTML migration: convert a production application's HTML templates in dependency waves | Done (branch) |
| 8 | Production cutover and hardening: release gates, source maps, plugin confinement, performance | Done for the first host; per-binary rollout next |

Each phase's exit gate is a test in this repository (for example the LSP,
Explorer, HMR, and release-gate tests in `e2e-test/`).

## Mandatory Release Gates

The gates apply per production binary: a binary cuts over when its gates
pass. For committed output, "generate and compile" reads as "the committed
output passes `ktml generate -check` and compiles".

- All of the binary's HTML templ components are migrated.
- The binary has no templ runtime dependency.
- Representative templ and KTML renders pass differential output tests.
- Text, attribute, URL, CSS, JSON, and script escaping pass adversarial tests.
- Plugin writes and stale cleanup cannot escape declared generated roots.
- Development props are not serialized into browser-visible cache nodes.
- Required semantic LSP workflows are stable.
- Every new syntax feature is complete in the canonical contract, Go parser,
  Tree-sitter corpus, formatter, semantic model, interpreter, direct Go
  generator, CLI, LSP, Explorer when applicable, HMR classification, source
  maps, behavioral tests, and generated-artifact inspection. Parser-only and
  backend-only features are not releasable.
- The payment proof justifies Host Schema presenter glue with concrete
  invalid-state and tooling benefits.
- Keyed Repetition meets its allocation/throughput benchmark and generated-source
  inspection gate.
- Production binaries contain no interpreter or Component Explorer routes.
- Clean CI and Docker builds generate (or check) and compile all KTML output.
- The host application's existing unit, short integration, handler, HTMX,
  SSE, and browser tests pass.

## Next

1. Tag the first release (`v0.1.0`). Then make `migrate/go.mod` require the
   tagged module and drop its `replace github.com/karl-tye/ktml => ../`, so
   `go install github.com/karl-tye/ktml/migrate/cmd/ktml-migrate@latest`
   works; use a `go.work` for local development.
2. Run the go-rod browser tests with a throwaway Chromium profile, so they
   pass while a desktop Chromium is open.
3. Make an e2e run clean up its generated files (`e2e-test/*.ktml.go`,
   `*.html`, `.ktml/`), which break a second run in the same checkout.
4. Decide whether `ktml generate` keeps printing one `Generation complete`
   line per file.
5. Converter: a derived boolean or loop record that the KTML no longer reads
   stays in the schema and presenter; inline it in a later cleanup.
6. XML output mode (for Hyperview-style templates).
