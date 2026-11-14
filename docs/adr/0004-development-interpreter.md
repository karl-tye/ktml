# ADR-0004: Development Interpreter, Render Identity, and Render Registry

- Status: Accepted (2026-10-12: the user confirmed decision 1, interpretation
  in the app process through the `ktmldev`-linked `ktml/dev/interp` with the
  registry in `ktml/dev/hmr`; decisions 2, 3, and 5 stand as written)
- Date: 2026-10-12
- Deciders: KTML maintainers
- Scope: Phase 3 — the interpreter, its process, typed props access, component
  instance markers, the server-side render registry, the `ktml dev` ↔ app
  control channel, and the release gate
- Builds on: [ADR-0002](0002-typed-render-selection.md),
  [ADR-0003](0003-rendering-api.md) (which defers marker format, registry
  ownership, and interpreter placement to this ADR),
  [Keyed Repetition and Render Identity v1](../language/keyed-repetition-v1.md)

## Context

PLAN's Development Runtime requires that compatible component changes patch
live pages from server-side typed props, with no browser-visible prop cache
and no interpreter-only semantics. Today:

- The livereload proxy scrapes `<template class="__ktml_cache">` JSON out of
  responses into `map[string]map[string]map[string]any` and rerenders through
  gohtml `html/template`. gohtml rejects records, states, repetitions, content,
  and conditions, so no current KTML feature can be patched.
- Generated renderers take Hybrid H props structs (ADR-0003). Content inputs
  are `ktml.Component` values, which in practice are Go closures and generated
  structs of other packages.
- The `ktmldev` build constraint already selects `*_ktml_dev.go` variants that
  import `ktml/dev` and write keyed Render Identity boundaries.

Any design must answer where the interpreter runs, how it reads typed props,
how live instances are identified in the DOM, who owns the registry, and how
the interpreter stays out of production binaries.

## Decision

### 1. The app process interprets

The interpreter runs **inside the application process**, linked only by
`ktmldev` builds, as `github.com/karl-tye/ktml/dev/interp`. `ktml dev` stays
the watcher, semantic change classifier, reverse proxy, SSE hub, and error
overlay; it never holds props.

- On a compatible change, `ktml dev` sends the changed sources to the app over
  the dev control channel (decision 6). The app swaps its interpreter program,
  rerenders the affected live roots from its registry, and returns HTML. Only
  rendered HTML crosses the process boundary, to the browser session that
  already received the original page.
- After the first swap, **every** generated component render in the app goes
  through the current program (decision 3), so page loads and HTMX requests
  made after an edit also show the edit without a rebuild.

Rejected: interpreting in the `ktml dev` process. Props would have to be
serialized out of the app. Content inputs are Go closures and Go-built
`ktml.Component` values, which cannot be serialized, and serializing the rest
recreates the browser-visible cache one hop later. A split (registry in the
app, interpreter in `ktml dev`) has the same serialization problem.

### 2. Typed props reach the interpreter as the generated values

The registry stores the generated Hybrid H value itself: it is the
`ktml.Component`. The interpreter reads it with **development-only
reflection** whose rules are fixed by the typed semantic model, not by the
value:

| Semantic type | Read |
| --- | --- |
| component input | the exported field named by `compiler.GeneratedGoField`; optional and defaulted inputs are pointers |
| record field | the exported field named by the Host Schema `Field.GoName` |
| collection | a slice; nil and empty are both empty |
| state | `Variant()` and the Variant type's `String()`; the payload is the unexported `payload` field of the sealed variant, read from a copy |
| contextual value | type assertion to `ktml.URL`, `ktml.TrustedURL`, `ktml.JSONValue`, `ktml.TrustedScript` |
| content | `ktml.Component`; nil is absent |

- No props are converted to maps, serialized, or stored outside the
  generated values. Interpreter frames hold `reflect.Value`s of the original
  values, and literal call inputs as typed scalars.
- A props struct compiled before an optional or defaulted input was added has
  no field for it. The interpreter reads it as absent, which selects the
  default: exactly PLAN's HMR-compatible case. A missing *required* field is an
  error before any write and means "rebuild".
- Reading the sealed payload with `reflect.NewAt` on a copy uses `unsafe`. It
  is acceptable because it is confined to a package that production builds
  cannot link (decision 7), it depends only on generator output in this
  repository, and the parity harness fails if the generator changes the shape.
- Rejected for now: generated dev accessors (a `*_ktmlschema_dev.go` twin
  exposing payloads). They add a generated file and API to every schema
  package for the one value reflection cannot read. Revisit if the reflection
  cost shows in HMR latency or the coupling causes a parity failure.

PLAN forbids untyped maps in *production* render paths. This decision keeps
that: production renderers are untouched, and the development path is typed by
the semantic model and checked byte for byte against the generator.

### 3. The interpreter shares the generator's semantics

`interp` compiles each component Document once into render operations and
renders with the same `rt` primitives (escapers, `rt.Writer`, key sets, error
constructors) and `ktml/dev` marker helpers as the generated code. It
reproduces:

- the generator's static-write coalescing, so writes match one for one;
- the cancellation, `ErrMissingContent`, default, dispatch, and
  `ErrInvalidState` order of every component entry;
- one-set keyed preflight before any body writes, and per-item cancellation;
- fill closures over the caller's scope and identity, as generated closures
  are;
- `RenderError` kinds, components, inputs, keys, and `Partial`.

The differential harness (`dev/interp/parity_test.go`) is part of
`go test ./...` and proves this on every fixture (see
[Parity](#parity-evidence)).

**Hot swap.** A `Program` is immutable and safe for concurrent use.
`Program.Swap(snapshot, changed)` recompiles only the changed Documents and
reuses the rest. Calls find their callee when they run, so an unchanged caller
uses a swapped callee. In-flight renders finish on the program they started
with.

**Delegation.** The `ktmldev` variant of each generated public `Render`
first calls a `ktml/dev` hook. When the app has installed an interpreter and
it knows the component's Go type, the hook renders through it and the
generated body is skipped. This is what makes Go-built content correct: a page
body passed as a layout's `Children` is a generated value whose `Render` runs
from inside the interpreted layout, so it must not run stale compiled code.
Same-package static calls never reach the hook; the interpreter performs those
calls itself.

### 4. Render Identity and instance markers

Keyed item boundaries are unchanged (`<!--ktml:item IDENTITY-->`). Component
instances extend the same identity grammar:

```ebnf
identity  = root, { " ", frame } ;
root      = "@", instance_id ;                 (* registry root instance *)
frame     = item_frame | call_frame ;
item_frame = component_id, "#", source_id, "=", canonical_key ;   (* v1 *)
call_frame = component_id, "#", call_id ;      (* call_id = "c<n>" *)
```

- `call_id` numbers a component's component calls in source pre-order across
  its clause and default bodies, like `r<n>`. An instance's identity is
  therefore determined by its position: the root instance, then every
  enclosing call site and repetition item. It is stable across rerenders while
  that position exists.
- `instance_id` is assigned by the registry for each tracked root render
  (decision 5); without a registry (tests, plain `ktmldev` builds) the root is
  omitted and identities start at the first frame, as in v1.
- **Single-root components** (every clause and default body is exactly one
  element) carry the identity as an attribute on that element:
  `data-ktml-i="IDENTITY"`, escaped by `rt.Attr`.
- **Multi-root components** (anything else) are surrounded by
  `<!--ktml:c IDENTITY-->…<!--/ktml:c IDENTITY-->`, percent-escaped like item
  boundaries.
- **Document components** (a body that starts with `doctype`) get neither:
  nothing may precede the doctype. They are patched by morphing
  `document.documentElement`.
- The client patches by identity, reuses the existing keyed morph, and falls
  back to a full reload on unbalanced, duplicate, or missing identities.
- Generated `ktmldev` code and the interpreter emit identical markers; the
  parity harness compares them. Production emits none.

### 5. The registry lives in the app process

A new development package, `github.com/karl-tye/ktml/dev/hmr`, owns the
registry. It is linked only from `ktmldev` files.

```go
type RenderInstance struct {
	SessionID   string
	InstanceID  string
	ComponentID string             // canonical "<import path>.<Name>"
	Props       ktml.Component     // the generated value itself
	Generation  uint64             // interpreter program generation at render
	Depends     []string           // transitive component IDs, from the program
	RenderedAt  time.Time
	Size        int                // estimated retained bytes
}
```

- **What is tracked.** Only *roots*: a generated component rendered by
  application code (a handler, `ktml.Render`, `SSERender`) with no enclosing
  KTML instance in `ctx`. Nested instances are recomputed by rerendering their
  root, so their props never need storing. The delegation hook (decision 3)
  is the tracking point.
- **Sessions without cookies.** A page render stores its roots under a fresh
  random request token. The injected client script carries only that opaque
  token and opens its SSE stream with it, which adopts the roots into the
  stream's session. HTMX requests add an `X-KTML-Session` header (set by the
  client in `htmx:configRequest`) so fragment roots join the session directly.
- **Bounds** (defaults, configurable): 256 roots per session, 4,096 roots and
  64 MiB estimated retained size overall, evicting least recently rendered
  first; 30 minutes idle per session; 30 seconds for unadopted request tokens;
  10 seconds after an SSE disconnect (to survive a reload).
- **Invalidation.** Host Schema, Go, and incompatible signature changes rebuild
  and restart the app, which empties the registry. Compatible swaps keep
  entries; a rerender that fails (for example, a now-missing required field)
  drops that root and requests a full reload of its session.
- **Patching.** For a changed component, every root whose `Depends` contains
  it is rerendered through the new program; the patch for that root targets
  its identity. Several live instances of one component, and parents of a
  changed child, are covered because the root render contains them all.

### 6. Control channel and HMR flow

- The runner starts the app with `KTML_DEV_TOKEN` (random per `ktml dev`
  run) and `KTML_DEV_ROOT`. In `ktmldev` builds, `hmr.Install(handler)`
  wraps the app's handler and serves `/__ktml/dev/*` only to requests that
  carry the token; the routes do not exist in production builds.
- `POST /__ktml/dev/swap {generation, files: [{path, source}]}`: the app
  applies the sources to its workspace (the bytes `ktml dev` validated, so
  there is no filesystem race), swaps the program, rerenders affected roots,
  and responds `{patches: [{session, identity, html}], reload: [session…]}`
  or a diagnostic. `ktml dev` broadcasts each patch to its session's SSE
  stream; any error falls back to rebuild and reload, and diagnostics drive
  the error overlay while the last valid program stays active.
- The livereload proxy stops scraping `__ktml_cache` templates; that path and
  `/__ktml/cache` are removed.

### 7. Release gate

- `ktml/dev`, `ktml/dev/interp`, `ktml/dev/hmr`, and `ktml/internal/compiler`
  are imported only by files built with `ktmldev`: generated
  `*_ktml_dev.go` files and one application wiring file.
- A gate test asserts that `go list -deps` and `go tool nm` on the production
  binary contain none of those packages. The parity harness already asserts it
  for every fixture module. A host application's release gates can add the
  interpreter, registry, and compiler packages to their own `ktml/dev` check.
- Dev endpoints require the per-run token and exist only in `ktmldev` builds.
- Because the interpreter, registry, and delegation hook exist only in
  `ktmldev` builds, the app's `ktml dev` build command must pass the tag: the
  `dev.build` command in `ktml.config.json` includes `-tags ktmldev` (for
  example `["go", "build", "-tags", "ktmldev", "-o", "{binary}", "./cmd/app"]`).
  Without it the app runs the production variants and every change falls back
  to rebuild and reload.

## Parity evidence

`TestParity_GIVENEveryLanguageFeatureFixture_WHENRenderedByBothBackends_THENBytesWritesAndErrorsMatch`
generates the gogen fixtures (`apis`, `board`, `gaps`, `hostschema`,
`payment`), every conformance Document that is valid on its own, and the
`dev/interp/testdata/parity/features` fixture into one module. For each
component it generates scenarios: zero props, filled props cycling through
every Variant, both boolean values, and present/absent optionals, plus empty,
duplicate, and nested-duplicate collections. Values are adversarial for their
context (markup and quotes in strings, `javascript:` and mixed-case schemes in
URLs, `</script>`, `<!--`, U+2028 in JSON). Each scenario is rendered by the
generated Go and the interpreter in the production and `ktmldev` variants,
comparing output bytes, write count, and the error:

- once normally and once with a canceled context;
- once with the writer failing at each write;
- once with the context canceled after each write.

## Consequences

- The interpreter must change with the generator. Any generator change
  without the matching interpreter change fails the parity test.
- Reflection makes interpreted rendering slower than generated code. That is
  acceptable for development; production does not use it.
- `ktmldev` builds link the compiler. The app's dev binary gets larger.
- Decisions 3 (delegation), 4 (instance markers), 5, 6, and 7 are built; see
  the [implementation notes](#implementation-notes-2026-10-12).

## Implementation notes (2026-10-12)

Decisions 3–7 are built as written, with these refinements found while
building them:

- **Outlet frames.** Content rendered at an outlet (Go-built content such as a
  page body passed as a layout's `Children`, or a fill closure) needs its own
  position, or it would share the identity of the instance that renders it.
  Outlets extend the identity with `component_id#o<n>`, numbered like `c<n>`.
  The unified grammar is in the
  [keyed contract](../language/keyed-repetition-v1.md#render-identity).
- **Marker shape per body.** The shape is decided for each default or clause
  body, so a signature whose clauses differ marks each rendered body by its
  own shape. A body that is exactly one call of a document component is also
  a document body: a wrapper such as `Page [] { Layout { … } }` must not write
  a boundary before the layout's doctype.
- **Delegation.** The `ktmldev` public `Render` is
  `dev.Render(ctx, w, component, component.ktmlGenerated)`. The registry
  tracks roots from the first request, but renders through the interpreter
  only after the first swap.
- **Sessions.** `hmr.Install` gives every request without `X-KTML-Session` a
  fresh token in the `X-KTML-Request` response header. The proxy removes the
  header and injects the token as `window.__ktmlSession`; the client opens
  `/__ktml/livereload?session=…`, and `ktml dev` reports the stream's
  connection and disconnection with the token-guarded
  `POST /__ktml/dev/session`.
- **Failed patches.** A root that fails to rerender is dropped. Because the
  rebuild restarts the app and empties the registry, `ktml dev` rebuilds and
  reloads every page, not only the failing session.
- **Client.** Patches wait for in-flight HTMX requests. Content of other
  registry roots (HTMX fragments swapped into the page) is kept when a page
  root is patched. An instance whose `c<n>` frame was renumbered by an added
  or removed call is matched, in order, with its replacement of the same
  shape. An Alpine scope whose bound markup changed is reinitialized and its
  state restored when its `x-data` is unchanged, so `init()` runs again;
  `x-cloak` is never re-added. The overlay events are `diagnostic` and
  `clear`.
- **Known limits.** A root rerenders from the props of its original render: a
  page whose HTMX actions replaced one of its own instances (an `outerHTML`
  swap) gets that instance's old markup back on the next page patch. A patch
  for an instance no longer on the page is skipped. Diagnostics from `ktml dev`
  validation never reach the app; the interpreter's own diagnostics (none are
  expected, by parity) show in the overlay and fall back to a rebuild.

## Rejected alternatives

- **Interpreting in `ktml dev`.** It requires serializing props, and content
  closures cannot be serialized (decision 1).
- **Generated dev accessors for all values.** Reflection reads every value
  except sealed payloads; see decision 2.
- **Registering every nested instance.** This duplicates what a root rerender
  recomputes, and multiplies registry size by tree size.
- **Cookies for dev sessions.** Request tokens adopted by the SSE stream need
  no cookie and no change to app middleware or cookie policy.
