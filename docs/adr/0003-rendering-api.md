# ADR-0003: Production Go Rendering API

- Status: Accepted (2026-10-12: the user confirmed Hybrid H and the Q1 page
  policy after the item-9 prototypes); amended 2026-10-13 (contextual
  constructors and forwarded attributes, under
  [Escaping primitive boundary](#escaping-primitive-boundary))
- Date: 2026-10-12
- Deciders: KTML maintainers
- Scope: Phase 0 rendering-API contract and the Phase 4 / Immediate Backlog
  item 9 prototype protocol
- Builds on: [ADR-0001](0001-language-boundary.md),
  [ADR-0002](0002-typed-render-selection.md)

The item-9 prototypes chose between the remaining candidates using the
[evaluation protocol](#evaluation-protocol). **Hybrid H is the frozen API**;
the measured results are in [Item-9 results](#item-9-results).

## Context

KTML replaces `templ.Component` in the HTML binary of a production host
application (Product Decisions: a new Go rendering API, no templ dependency in
the final binary). The generated API is what every handler, layout, SSE stream,
test, and codemod touches, so it must be fixed before Phase 4 generation and
Phase 6 codemods are built.

### The host application's rendering call sites

The host application renders with templ `v0.3.1020`. Its call sites are
evidence for the requirements, not a design to copy.

- **The renderer boundary.** A small renderer file defines
  `Renderer.Component(c templ.Component, w, req)`, which sets `Content-Type`,
  adds an impersonation value to the request context, renders, and only logs
  errors. `Renderer.Page(p Page, w, req, features ...templ.Component)` builds
  `Layout(p.Title, req.URL.Path, staff, features...)` and passes the page body
  through `templ.WithChildren(ctx, p.Content)`. This is the only
  `templ.WithChildren` in handwritten Go. `Page.Content` is a
  `templ.Component` field. Handlers call `.Render.Component(` a few hundred
  times and `.Render.Page(` a few dozen times; a handful of the `Page` calls
  pass `features`.
- **Full page vs. HTMX fragment.** Handlers decide this, not the renderer:
  dozens of handlers check `HX-Request`. A typical error handler first picks a
  component type per region, then sets `HX-Retarget`/`HX-Reswap`. It ends with
  either `Render.Component(component, …)` or
  `Render.Page(Page{Content: component}, …)`. Its local
  `component := templ.Component(...)` holds a value whose concrete type varies
  by branch.
- **Layout composition.** `Layout(title, path, staff, features
  ...templ.Component)` renders the page body through `{ children... }`, and
  the variadic features are rendered by `TopBar` with
  `for _, f := range features { @f }`.
- **Request context inside templates.** Only the layout reads `ctx`: an
  impersonation value and an `IsStaging(ctx)` check.
- **Heterogeneous component values in Go.** About twenty non-test Go uses of
  `templ.Component` cover every storage form:
  - struct fields (including `Page.Content`)
  - a map, `map[string]templ.Component`, behind a tabular-data interface
  - a function type, `type Component func(Item) templ.Component`
  - fallible builders returning `(templ.Component, error)`
  - branch-selected locals

  Inside templates there are about as many `templ.Component` parameters (for
  example `SectionHeader(title, children ...templ.Component)`) and a few dozen
  `{ children... }` outlets.
- **Direct renders.** A few dozen non-test `.Render(r.Context(), w)` calls
  bypass the renderer, and nearly all of them ignore the error. Tests contain a
  few hundred `.Render(` calls, mostly
  `require.NoError(t, X.Render(ctx, &buf))`.
- **SSE.** `SSERender(ctx, w, comp templ.Component, id)` renders into a
  `bytes.Buffer` after an `id:`/`data:` prefix, collapses whitespace onto one
  line, and writes the event. It has about a dozen call sites (message bubbles,
  thread items, live maps, notifications, live work queues); some run under
  write deadlines and `Flush`.
- **Out-of-band fragments.** These are composed in markup. Wrapper components
  render several fragments in sequence. There are a few dozen `hx-swap-oob`
  uses, sometimes passed through `templ.Attributes`. A couple of OOB responses
  are raw `fmt.Fprintf` strings.
- **Error behavior today.** Generated templ code checks `ctx.Err()` on entry. It
  wraps non-buffer writers in a pooled 4 KiB `bufio` writer, so a failure after
  the first 4 KiB leaves a partial response. The renderer logs and returns.
  Only one handler buffers a full page so that a failure can become a clean
  503. No handwritten component returns an error.
- **Non-HTML output.** Other output formats rendered by a separate binary are
  out of scope.

### The current KTML generator

`internal/generator/gogen` already emits a Prototype-A shape. For each
component it writes:

- `type XProps struct`, with required inputs as `T` and optional/defaulted
  inputs as `*T`
- an unexported bound struct, and `func X(props XProps) ktml.Component`
- an unexported `renderX(ctx, w io.Writer, props XProps) error`

Calls between components use `X(XProps{…}).Render(ctx, w)`. Children travel
through `ktml.WithChildren(ctx, …)` / `ktml.Children(ctx)` (`component.go`).
This mirrors templ's context-carried children and is defective. A child
component with its own `children` outlet, called without children inside a
caller's slot body, reads the *caller's* children from `ctx` and recurses
without end. A scratchpad transcription of the emitted code for
`Layout { section { children } }`, `Inner { em { children } }`, and
`Page { Layout { Inner } }` confirmed the unbounded recursion. The generator also:

- emits per-file helpers (`ktmlWriteString`, `ktmlPtr`), which collide if two
  generated files share a package
- inlines imported components into one package (`GenerateSnapshot`)
- escapes through `html.EscapeString`, which allocates
- has no CLI caller yet

### Requirements

From PLAN.md "Generator requirements", ADR-0001, ADR-0002, and the Host Schema
spec, the generated renderers must:

- parse nothing at runtime and write directly to `io.Writer`
- use no reflection and no untyped maps
- minimize allocations
- apply the contextual escaping contract of ADR-0001
- render void and boolean attributes correctly, with deterministic attribute
  order and collisions
- generate deterministic code and source maps
- return all render errors
- strip development markers, registries, and Explorer integration
- not depend on templ

Request context is never a template scope, and `context.Context` exists only
for cancellation, child rendering, and development tracking. Renderer errors are
limited to writer, cancellation, invalid-state, duplicate-key, and child-render
failures. Nil or invalid states fail before the component writes. Keyed
repetition preflights keys with one set and streams without buffering body
HTML.

## Decision

1. The runtime contract below applies to every candidate. It covers props,
   content, context, errors, escaping, the dev/production boundary, package
   layout, and naming.
2. **Pure Prototype B is eliminated as the public composition API** (see
   [Narrowing](#narrowing-the-field)). Its direct render functions remain in
   the benchmark as the performance floor.
3. **Hybrid H is the frozen public API** (the props struct is itself the
   component). The item-9 prototypes compared it with **Prototype A** (PLAN's
   bound-component constructor) using the
   [evaluation protocol](#evaluation-protocol) and its decision rule; both
   met the budget and templ parity, and H won on call-site ergonomics. `gogen`
   emits only H; Prototype A's generator path is removed, and Prototype B's
   `RenderX` path (`gogen.APIDirect`) is kept only as the benchmark floor.
4. Children and slots are explicit props fields in every candidate. The
   context-carried `WithChildren`/`Children`/`ClearChildren` API is withdrawn
   and must not appear in generated code.

## Common runtime contract

### Packages

| Package | Audience | Contents |
| --- | --- | --- |
| `github.com/karl-tye/ktml` | application code | `Component`, `ComponentFunc`, `Render`, `Join`, `Ptr`, `RenderError`/`ErrorKind`, the opaque contextual value types and their constructors |
| `github.com/karl-tye/ktml/rt` | generated code only | the `Writer` wrapper, contextual escapers, key-set helpers, and error constructors. Its stability is tied to the pinned generator version, not to semver for handwritten callers |
| `github.com/karl-tye/ktml/dev` | development builds only | render-identity markers and registry hooks. Production binaries must not link it |

```go
package ktml

type Component interface {
	Render(ctx context.Context, w io.Writer) error
}
type ComponentFunc func(context.Context, io.Writer) error // implements Component

func Render(ctx context.Context, w io.Writer, cs ...Component) error // nil entries skipped
func Join(cs ...Component) Component                                 // one value for a content slot
func Ptr[T any](v T) *T                                              // optional/defaulted inputs
```

`ktml.Component` has the same method set as `templ.Component`, so values of each
type are assignable to the other without adapters. This is what makes the
Phase 7 incremental migration possible. A KTML page can sit in a templ layout's
children, and a templ widget can fill a KTML slot. It does not reintroduce templ
into KTML: the KTML runtime never imports templ.

### Props struct shape

One Go struct per Component Signature. Field names follow the Host Schema rule:
only the first ASCII letter is uppercased, so `id` becomes `Id`, and hyphenated
input names follow the frozen input contract. Fields appear in signature order.

| KTML input | Go field | Absence / validation |
| --- | --- | --- |
| `name: string\|boolean\|number\|integer` (required) | `T` (`string`, `bool`, `float64`, `int64`) | Required at KTML call sites; Go cannot detect omission (enforced by `ktml vet`; see [Q2](#resolved-questions)) |
| `name?: T` (optional scalar, record, or collection) | `*T`, including `*[]T` for collections | `nil` is `None`; for a present collection, nil and empty slices are both an empty collection. Fields and inputs share this shape ([Host Schema Records v1](../language/host-schema-records-v1.md)) |
| `name: T = literal` (defaulted) | `*T` | `nil` selects the default inside the renderer; a non-nil zero value is respected |
| Host Schema record `R` | `R` (the generated value type) | as generated by the schema slice |
| Presentation State `S` | `S` (generated sealed interface) | `nil` → `ErrInvalidState` before the component writes |
| Contextual value (`URL`, `TrustedHTML`, …) | `ktml.URL`, … | zero value is the empty value of that context |
| `children: content` / `children?: content` | `Children ktml.Component` | required and `nil` → `ErrMissingContent` before the component writes; optional `nil` is absent |
| `actions?: content` (named slot) | `Actions ktml.Component` | as for children |

The struct has no unexported fields and no methods other than `Render` in H,
and its zero value is meaningful. Generated code never mutates props.
Generated-to-generated calls pass `*Props` to internal render functions; a
pointer to a non-escaping composite literal does not allocate. Slot content
written in KTML is lowered to a `ktml.ComponentFunc` closure, which costs one
allocation per fill and is measured by the nested benchmarks. Since
2026-10-20, H lowers a fill that captures only the caller's `props` (or
nothing) to an unexported one-pointer (or empty) struct with a `Render`
method instead, which is stored in the interface without allocating; only the
props value escapes, once per render (see
[Fill allocations](#fill-allocations-2026-10-20)).

ADR-0002 forbids component-valued *data* inputs. Content slots are the only
`ktml.Component` fields KTML generates. Go code may still build heterogeneous
`ktml.Component` values, select them by branch, store them, or `Join` them,
and pass them as slot content.

### Context propagation

- Production generated code passes `ctx` through unchanged and never calls
  `context.WithValue`.
- `ctx.Err()` is checked on entry to each component and before each repetition
  item. Item 9 may relax the per-item check if its cost is measurable and
  cancellation latency stays bounded by one item.
- KTML cannot read `ctx`. The host application's impersonation value and `IsStaging(ctx)`
  become explicit `Layout` inputs (for example `impersonating: boolean`,
  `staging: boolean`), and `Renderer.Page` supplies them from the request.

### Error semantics

```go
type ErrorKind uint8

const (
	ErrWrite ErrorKind = iota + 1 // underlying io.Writer failed (Unwrap returns it)
	ErrCanceled                  // ctx.Err() (Unwrap returns it, so errors.Is(err, context.Canceled) works)
	ErrInvalidState              // nil/unknown Presentation State selector
	ErrMissingContent            // required content slot is nil
	ErrDuplicateKey              // Keyed Repetition preflight found a duplicate sibling key
)

type RenderError struct {
	Kind      ErrorKind
	Component string // canonical component ID: "<package ID>.<Name>"
	Input     string // KTML input or repetition source, when applicable
	Key       string // canonical key encoding for ErrDuplicateKey
	Partial   bool   // bytes had reached w before the error (set at the root call)
	Err       error
}
```

- **Typed errors before any write.** `ErrInvalidState` and `ErrMissingContent`
  are checked in a component's preflight, before that component writes its
  first byte. `ErrDuplicateKey` is raised before any body of that repetition
  writes. Ancestors may already have written, so the guarantee is per
  component, not per response.
- **Mid-stream errors.** Writer and cancellation errors can occur after
  output. Generated code never buffers or rolls back. The first error stops
  rendering and is returned unchanged through every ancestor, so the failing
  component's identity survives and no allocations are added per level.
  `Partial` reports whether *any* byte reached the caller's writer. A caller can
  still send a clean error response when `Partial` is false.
- **Whole-response atomicity is the caller's choice.** Buffering belongs to the
  application boundary (the renderer, `SSERender`), not to generated code. The
  host application's policy is [Q1](#resolved-questions).
- **Child errors.** A `ktml.Component` supplied by Go may return any error.
  Generated code returns it unwrapped.

### Escaping primitive boundary

Generated code writes through `*rt.Writer`. This is a thin, non-buffering
wrapper that holds the target, a written-bytes flag for `Partial`, and an
`io.StringWriter` fast path. `rt.Wrap(w)` returns `w` unchanged when it is
already an `*rt.Writer`, so nested public `Render` calls do not re-wrap.
Generated code adds no `bufio` layer, because `net/http` already buffers
responses. Item 9 measures this against templ's pooled 4 KiB buffer.

| ADR-0001 context | Primitive (in `rt`) | Accepts |
| --- | --- | --- |
| HTML text | `Text(w, string)`, `TextInt(w, int64)`, `TextNumber(w, float64)`, `TrustedHTML(w, ktml.TrustedHTML)` | scalars, `TrustedHTML` |
| HTML attribute value (double-quoted) | `Attr(w, string)`, `AttrInt`, `AttrNumber` | string, number, integer; booleans are lowered to presence by the generator |
| URL attribute | `URL(w, ktml.URL)`, `TrustedURL(w, ktml.TrustedURL)` | policy-checked or trusted URL |
| CSS value | `CSS(w, ktml.CSSValue)` | serializer output |
| JSON data | `JSONScript(w, ktml.JSONValue)`, `JSONAttr(w, ktml.JSONValue)` | container-safe JSON |
| Script body | `Script(w, ktml.TrustedScript)`, `JSONScript`, `ScriptBool` | trusted script, container-safe JSON, booleans |
| Forwarded attributes | `Attributes(w, ktml.Attributes, owned []string)` | bags built by the context-checked constructors |

- The escapers write runs directly to `w`, with no intermediate string.
- Static markup is escaped at generation time by the *same* `rt` escape
  functions and emitted as constant strings.
- The development interpreter calls the same `rt` primitives, so escaping
  parity holds by construction. Differential fixtures still compare bytes.
- Number and integer formatting is defined once in `rt`. ADR-0002 requires
  canonical base-10 output for `integer`.
- The contextual types are opaque structs in package `ktml`. They have
  unexported fields, exported read accessors used by `rt`, and constructors only
  in Go. Their constructor API is the trust/Host Schema slice's decision, not
  this ADR's.

**Amendment (decided by the user on 2026-10-13).** The
[migration-language slice](../language/migration-language-v1.md) adds to
package `ktml` without changing the error kinds:

- **`ktml.Attributes`.** It is an opaque, ordered bag that a component
  forwards with `...name`. Each entry is built per context:
  - `Attr(name, string)`, `BoolAttr`, `URLAttr`, `TrustedURLAttr`,
    `JSONAttr`, `ScriptAttr(name, TrustedScript)`, `ScriptBoolAttr`, and
    `CSSAttr(name, CSSValue)` each return `(Attributes, error)`. The error is
    an `*AttributeError` for an invalid name, or for a name whose context
    does not accept the value: a string for `onclick`, or anything but
    `CSSValue` for `style`.
  - The `Must…` variants panic, and are for constant arguments. Generated
    code calls them only for names and contexts the compiler has verified.
    Nothing panics on data-driven input.
  - `JoinAttributes` combines bags, keeping the first entry of a repeated
    name.
  - `rt.Attributes` writes each entry through its context's escaper. It
    drops the names the element writes itself.
- **`ktml.CSS(ktml.CSSDecl(property, value), …)`.** It returns
  `(CSSValue, error)`, validating each declaration against a closed grammar.
  `MustCSS` is for constants, and `TrustCSS` remains the audit boundary.
- **`rt.Script`.** It rewrites `</script` and `<!--` in a `TrustedScript`
  body, so the value cannot end its element. This is a rewrite, not a new
  error kind: `ErrorKind` is unchanged.

### HTMX partials, SSE, and OOB fragments

The rendering API has no special case for these.

- **HTMX partials.** A partial is an ordinary component rendered alone. The
  page-vs-fragment decision and `HX-*` headers stay in handlers.
- **OOB fragments.** These are markup (`hx-swap-oob` attributes) inside a KTML
  wrapper component, or several components passed to
  `ktml.Render(ctx, w, a, b, …)`.
- **SSE.** `SSERender` keeps its shape with a `ktml.Component` parameter.
  Because renderers only need an `io.Writer`, a future line-prefixing SSE writer
  could replace whitespace collapsing. That change would alter bytes, so it is a
  follow-up, not part of the migration.

### Dev-marker and HMR stripping boundary

The boundary is a Go build constraint, so it is zero-cost:

- The generator emits `x_ktml.go` with `//go:build !ktmldev` and
  `x_ktml_dev.go` with `//go:build ktmldev`. Both have identical exported
  APIs.
- Only the dev variant imports `ktml/dev`. That variant emits Render Identity
  boundaries and registers instances through a tracker that dev middleware
  installs in `ctx`.
- Production files contain no markers, key attributes, registry calls,
  Explorer hooks, or `ctx.Value` lookups.
- A release-gate test asserts that `go version -m`/`go tool nm` on the
  production binary shows no `ktml/dev` or interpreter packages.
- Marker format, registry ownership, and the interpreter's process placement
  belong to the Render Identity and interpreter ADRs. This ADR fixes only that
  they cannot change the exported API or production bytes.

### Generated package and naming layout

- `foo.ktml` generates `foo_ktml.go` in the **same Go package** as its
  directory, matching PLAN's "domain components in their existing domain
  packages" and templ's `_templ.go`. `foo.ktmlschema` generates
  `foo_ktmlschema.go`. The files are not committed; `*_ktml.go` and
  `*_ktmlschema.go` are ignored.
- Package-level helpers move to `rt`; generated files declare no helpers.
- A cross-document import resolves to the target directory's Go import path
  and its exported symbols. `GenerateSnapshot` inlining is replaced.
- Generated unexported identifiers use the `ktml` prefix (`ktmlRenderCard`).
- The generator **fails** if any exported or `ktml`-prefixed name collides with
  a handwritten declaration in the package, or with a Host Schema name. A
  component `JobCandidate` beside a schema record `JobCandidate` is a
  diagnostic, never a silent rename.

## Candidate APIs

Running example: a component `Card` with a required title, optional subtitle,
defaulted compact flag, a state selector, optional children, and a named slot:

```ktml
Card [title: string, subtitle?: string, compact: boolean = false,
      state: CardState, children?: content, actions?: content]
```

### Prototype A: bound component constructor

```go
type CardProps struct {
	Title    string
	Subtitle *string
	Compact  *bool
	State    CardState
	Children ktml.Component
	Actions  ktml.Component
}

func Card(props CardProps) ktml.Component              // returns unexported ktmlCardComponent{props}
func ktmlRenderCard(ctx context.Context, w *rt.Writer, p *CardProps) error
```

| Concern | A |
| --- | --- |
| Props | as the common table, in `CardProps` |
| Children/slots | `Children`, `Actions` fields: `ktml.Component` |
| Heterogeneous composition | `var c ktml.Component = job.Card(job.CardProps{…})`; works in fields, maps, `Join`, slices |
| Same-package generated calls | `ktmlRenderCard(ctx, w, &CardProps{…})`, a static call |
| Cross-package generated calls | `ui.Card(ui.CardProps{…}).Render(ctx, w)`, an interface call (the internal function is unexported) |
| Context | unchanged |
| Errors | common contract |
| Escaping / dev boundary / layout | common contract |
| Exported identifiers per component | 2 (`Card`, `CardProps`) |
| Reserved names | `<Name>Props` |

### Hybrid H: the props struct is the component

This is PLAN's "small KTML `Component` interface backed by generated structs and
direct internal render functions", with the bound struct and the props struct
merged.

```go
type Card struct {
	Title    string
	Subtitle *string
	Compact  *bool
	State    CardState
	Children ktml.Component
	Actions  ktml.Component
}

func (c Card) Render(ctx context.Context, w io.Writer) error {
	return ktmlRenderCard(ctx, rt.Wrap(w), &c)
}
func ktmlRenderCard(ctx context.Context, w *rt.Writer, p *Card) error
```

| Concern | H |
| --- | --- |
| Props | as the common table, in `Card` itself |
| Children/slots | as in A |
| Heterogeneous composition | `var c ktml.Component = job.Card{…}`; boxing costs the same as A when a value is stored as an interface |
| Same-package generated calls | `ktmlRenderCard(ctx, w, &Card{…})`, a static call |
| Cross-package generated calls | `ui.Card{…}.Render(ctx, w)`, a static method call (value receiver, concrete type) |
| Go call site | `job.Card{Title: t}.Render(ctx, w)`, a static call |
| Exported identifiers per component | 1 |
| Reserved names | an input that maps to `Render` (input `render`) is a diagnostic |

The only API difference between A and H is dispatch and spelling. Everything
else is the common contract.

### Prototype B: direct render functions (performance floor only)

```go
type CardProps struct { /* as in A, but slots are ktml.RenderFunc */ }
type RenderFunc func(context.Context, io.Writer) error
func RenderCard(ctx context.Context, w io.Writer, props CardProps) error
```

In pure B, every value-position use becomes a closure:
`Children: func(ctx context.Context, w io.Writer) error { return ui.RenderBadge(ctx, w, p) }`.

### Narrowing the field

**Pure B is eliminated as the public API, without needing a benchmark:**

1. **Composition needs a deferred-render value anyway.** The host application stores components
   in fields, maps, variadics, branch-selected locals, and fallible builders
   (see [Context](#context)). B must spell that value as a func type, which is
   `ComponentFunc` under another name. B therefore does not remove the
   abstraction; it moves construction into closures at every call site.
2. **Migration interop.** `ktml.Component` and `templ.Component` are mutually
   assignable, while B's `RenderFunc` needs adapters at every templ/KTML
   boundary for all of Phase 7.
3. **Codemod risk.** Converting a templ constructor in value position to a
   closure changes when arguments are evaluated: templ evaluates them at
   construction, a closure evaluates them at render. To keep templ semantics
   the codemod would have to hoist arguments into temporaries. A and H rewrite
   arguments in place.
4. **No performance advantage over H.** A scratchpad spike (go1.27.0, amd64,
   a trivial body, cross-package, `io.Discard`) measured:
   - H's `ui.Card{…}.Render(ctx, w)`: 6.7–6.9 ns/op, 0 allocs
   - B's `RenderCard`: 6.8–6.9 ns/op, 0 allocs
   - A's `ui.CardA(p).Render(ctx, w)`: about 16.2 ns/op, 0 allocs (the
     interface call was not devirtualized)
   - storing a value in a `ktml.Component` (slot value): 1 alloc for both A
     and H

   These are indicative only and do not replace the protocol.

B's direct functions are still generated in the benchmark module, as
`RenderX` wrappers around `ktmlRenderX`. They are the floor that A and H are
measured against.

A is not dominated. Its interface-returning constructor hides props after
construction and keeps PLAN's documented shape. Whether the measured
cross-package interface-call cost matters on real host-application paths is an empirical
question.

## Codemod impact

Counts are from the host application's non-test Go unless stated.

| Host construct | A | H | B (for reference) |
| --- | --- | --- | --- |
| templ constructor `job.Card(a, b)` (positional) | `job.Card(job.CardProps{Title: a, State: b})`; needs the templ parameter → KTML input map from migration IR | `job.Card{Title: a, State: b}`; same map | value position: closure plus argument hoisting |
| `.Render(ctx, w)`: a few dozen sites, plus a few hundred in tests | unchanged | unchanged | rewritten to `RenderX(ctx, w, props)` |
| `templ.WithChildren` (`Renderer.Page`) | `Layout(LayoutProps{…, Children: p.Content})` | `Layout{…, Children: p.Content}` | closure |
| `templ.Component` types (Go fields, locals, and templ params) | type rename to `ktml.Component` | same | `RenderFunc` and closures at every producer |
| `features ...templ.Component` (Layout/TopBar, `Renderer.Page`) | `Features: ktml.Join(features...)`; `Renderer.Page` keeps its variadic signature | same | closure over a loop |
| `map[string]templ.Component` (tabular-data interface), `func(Item) templ.Component`, `(templ.Component, error)` | Go side: type rename. KTML side: component-valued data is rejected (ADR-0002), so the consuming templates get a diagnostic and need Host Schema / clause redesign | same | same, plus closures |
| `ctx` reads in the layout template | presenter scaffold plus explicit Layout inputs; `Renderer.Page` edited by hand | same | same |
| `templ.Raw` test fixtures | `ktml.ComponentFunc` literal or a `TrustedHTML` fixture | same | same |

A and H differ in codemod complexity only in the spelling of the constructor
rewrite. The protocol measures this anyway.

## Evaluation protocol

### Where

The prototypes live in a separate Go module, `bench/renderapi/` in this repo, so
templ (pinned `v0.3.1020`) is a dependency of the benchmark module only.

- The host application is not modified. Representative templ components are
  copied in from a recorded revision of it, with their view types reduced to
  the fields they read.
- Features the generator already supports (scalars, inputs, children, static
  markup) are emitted by `gogen` behind an `Options.API` switch (`A`, `H`,
  `B`).
- Constructs not yet in the compiler (Host Schema states, Render Clauses, Keyed
  Repetition, contextual values) are hand-lowered exactly as this ADR
  specifies, and labelled as hand-lowered in the results.
- The static, nested, and form benchmarks must be generator-emitted before
  acceptance.

### Benchmark set

Each benchmark has implementations for A, H, B (floor), templ (baseline), and
handwritten Go (lower bound). All implementations must produce byte-identical
output, checked before timing.

| ID | Shape |
| --- | --- |
| `Static` | ≈4 KiB static page component, no inputs |
| `Nested` | depth-8 chain with a `children` slot at every level; same-package and cross-package variants; plus one named slot per level |
| `ListHeavy` | 1,000-item Keyed Repetition of records, each with a 3-item nested keyed list and an optional field |
| `FormHeavy` | 40 fields with typed/boolean attributes, optional and defaulted inputs, and attribute escaping with adversarial characters |
| `ScriptHeavy` | `JSONScript` data, a `TrustedScript` block, URL attributes, and high-escape text (a common page shape) |
| `HostPage` | `Layout` + `TopBar` with two `features` + a list page body through the item-9 `Renderer.Page` |
| `HostFragment` | an HTMX partial (a list item) through `Renderer.Component` |
| `HostOOB` | an OOB response (an item plus a status-poller OOB fragment), and the same pair rendered with `ktml.Render(ctx, w, a, b)` |
| `HostSSE` | a message-bubble event through `SSERender` |
| `HostError` | a nil state selector at depth 3: asserts `ErrInvalidState`, the component ID, no bytes from the failing component, and `Partial` |

Writers: `io.Discard`, a reset pre-grown `*bytes.Buffer`, and, for the `Host*`
benchmarks, a loopback `net/http` server measured client-side. The last one
captures write-size effects against templ's 4 KiB buffer.

Run with `go test -run '^$' -bench . -benchmem -count=10 -cpu=1` on one idle
machine and a pinned Go toolchain. Compare with `benchstat` and record
`GOAMD64`, CPU, and commit.

### Metrics

1. ns/op, B/op, and allocs/op for each benchmark (benchstat median and
   confidence interval).
2. Size of the generated Go source per corpus, and the change in linked
   `.text` size of a binary that links every benchmark component.
3. Call-site ergonomics: characters and tokens at every rewritten host-application call
   site (constructor, slot fill, page/fragment render) compared with the templ
   original, counted by the codemod prototype over the copied call sites.
4. Codemod complexity: the share of host-application call sites rewritten automatically,
   the number of diagnostics/manual sites, and the codemod's lines of code per
   candidate.

### Hard gates

A candidate that fails any of these is out:

- byte-identical output with the floor and the interpreter fixtures
- every error kind is reachable and typed, and the pre-write guarantee is
  asserted
- no reflection, maps, `context.WithValue`, or templ import in the production
  files
- deterministic generation (two runs are byte-identical)
- `go vet` and staticcheck are clean
- no `ktml/dev` in the production binary

### Decision rule

1. **Performance budget.** Against the B floor, a candidate is within budget
   when, on the `Host*` benchmarks, it has at most +1 alloc/op per top-level
   render and a ns/op geomean ≤ 1.10×, with no single benchmark above 1.25×.
2. **Templ parity.** The chosen candidate must also have a ns/op geomean ≤ 1.00×
   templ and allocs/op ≤ templ on the `Host*` benchmarks. If no candidate
   reaches parity, the session stops and escalates instead of choosing.
3. **If exactly one of A and H is within budget,** choose it.
4. **If both are within budget,** choose the one with the lower ergonomics
   score: rewritten-site characters summed over host-application call sites, plus 50 per
   manual site. A difference under 5% is a tie, and **ties go to H** (one
   exported identifier and static dispatch by default).
5. **Size check.** Reject the rule's winner in favor of the other candidate if
   the winner's linked `.text` delta exceeds the other's by more than 10%.
6. The winning prototype amends this ADR to "Accepted". It records the benchstat
   tables, the chosen shape, the removal of `WithChildren`/`Children`/
   `ClearChildren`, and the answers to the Questions. `gogen` then emits only
   the winner.

## Item-9 results

This section records the item-9 prototypes (2026-10-12). The user confirmed
the outcome on 2026-10-12. The measurements below were taken with all three
candidates; A's generator path and the `ktml generate -api` switch were then
removed (KTML `e5d9670` and the host application's prototype branch
reproduce them).

### What was built

- **Runtime contract.**
  - `rt.Writer` is pooled and non-buffering, records whether any byte was written, and takes the `io.StringWriter` fast path. `rt.Wrap` reuses an existing `*rt.Writer`, and `rt.Finish` sets `Partial`.
  - `ktml.Render` shares one writer across its components, so `Partial` also covers earlier siblings.
  - The contextual escapers (`Text`, `Attr`, `TextInt`, `TextNumber`, `TrustedHTML`, `URL`, `TrustedURL`, `CSS`, `JSONScript`, `JSONAttr`, `Script`) write runs directly and allocate nothing; this is tested.
  - Static markup is escaped by `rt.EscapeString` and coalesced into one write per run.
  - Writer failures become `ErrWrite` with the component ID, and `ctx.Err()` is checked on every component entry and before every repetition item.
  - The `TrustedHTML`, `TrustedScript`, `CSSValue`, and `JSONValue` types have provisional constructors.
  - The `ktmldev` boundary (`x_ktml_dev.go`) was already in place.
- **Candidates.**
  - `gogen.Options.API` selects A, H, or B.
  - Internal renderers are `ktmlRenderX(ctx, *rt.Writer, *Props)`, and same-package calls are static.
  - Cross-package calls are `pkg.X(pkg.XProps{…}).Render` (A), `(pkg.X{…}).Render` (H), and `pkg.RenderX(ctx, w, pkg.XProps{…})` (B).
  - In H, an input named `render` is an error.
  - B's content fields are `ktml.ComponentFunc`.
- **CLI.**
  - `ktml generate [-api A|H|B] <file|dir|dir/...>` generates components through `gogen` with the new default `go` backend (cli-spec.md §1.1). gohtml must now be named explicitly.
  - It writes `foo_ktml.go` and `foo_ktml_dev.go` in the source directory's Go package, after checking for handwritten-name collisions.
- **`ktml vet`.** Q2, below.
- **Benchmarks.**
  - The synthetic set is in `bench/renderapi`, the only module with a templ dependency.
  - The host set uses representative host-application components ported to KTML in a nested prototype module inside the host application's repository (templ only there; removed once the application rendered through generated KTML, so it is not public). Each candidate has a prototype of the application's Renderer, with Q1's policy: fragments and SSE are buffered; pages stream and send a clean 500 only when `Partial` is false.
  - The ported components are Layout/Sidebar/NavItem/TopBar, the impersonation banner, a list panel and item (with membership and action states), status pollers, an OOB response, and a message bubble.
  - The templ baseline is a copy of the same templates at a recorded revision, with view types reduced to the fields read and `ctx` reads made parameters.
  - A codemod prototype measures the call sites.

### Environment

go1.27.0, linux/amd64, `GOAMD64=v1`, AMD Ryzen AI Max+ 395. KTML `e5d9670`
and a recorded revision of the host application. The command is
`go test -run '^$' -bench … -benchmem -count=10 -cpu=1 -timeout 0`
(host set with `-benchtime=0.5s`). Values are benchstat medians. Raw output,
benchstat tables, and the decision script output for the synthetic set are in
`bench/renderapi/results/`; the host set's raw output is kept privately with
the host application. Other sessions were open, so the machine was only approximately idle
(load ≈ 1.7). Most host-set confidence intervals are within ±3%.

### Host-application benchmarks (the decision set)

**Handler path: present + render through the Renderer.** Each cell is
ns/op (allocs/op). All implementations write byte-identical bodies, checked by
the prototype module's scenario tests before timing.

| Benchmark | Writer | A | H | B (floor) | templ | handwritten |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| HostPage | discard | 15,873 (198) | 15,318 (198) | 15,508 (203) | 43,166 (895) | 10,200 (152) |
| HostPage | buffer | 17,629 (198) | 17,043 (198) | 17,566 (203) | 43,366 (895) | 11,860 (152) |
| HostPage | http | 49,453 (280) | 50,210 (280) | 52,040 (285) | 69,200 (967) | 45,346 (234) |
| HostFragment | discard | 605 (11) | 596 (11) | 632 (11) | 1,314 (33) | 500 (10) |
| HostFragment | buffer | 610 (11) | 601 (11) | 635 (11) | 1,309 (33) | 511 (10) |
| HostFragment | http | 12,647 (78) | 12,492 (78) | 12,986 (78) | 16,632 (103) | 12,752 (77) |
| HostOOB | discard | 1,654 (26) | 1,614 (26) | 1,655 (27) | 3,276 (59) | 1,370 (23) |
| HostOOB | buffer | 1,710 (26) | 1,632 (26) | 1,679 (27) | 3,326 (59) | 1,430 (23) |
| HostOOB | http | 14,658 (93) | 13,960 (93) | 14,082 (94) | 19,160 (129) | 13,842 (90) |
| HostOOBPair (`ktml.Render(a, b)`) | discard | 1,637 (27) | 1,645 (27) | 1,654 (27) | 3,274 (60) | 1,385 (23) |
| HostOOBPair | buffer | 1,656 (27) | 1,722 (27) | 1,678 (27) | 3,266 (60) | 1,432 (23) |
| HostOOBPair | http | 14,055 (94) | 14,974 (94) | 14,720 (94) | 20,746 (131) | 14,234 (90) |
| HostSSE | discard | 2,710 (18) | 2,670 (18) | 2,464 (18) | 4,130 (64) | 1,968 (8) |
| HostSSE | buffer | 2,701 (18) | 2,852 (18) | 2,482 (18) | 4,168 (64) | 1,974 (8) |
| HostSSE | http | 14,837 (81) | 15,208 (81) | 14,630 (81) | 16,368 (127) | 14,126 (71) |
| HostError | discard | 2,838 (33) | 2,886 (33) | 2,869 (34) | n/a | n/a |
| HostError | buffer | 2,880 (33) | 2,936 (33) | 2,888 (34) | n/a | n/a |
| HostError | http | 15,531 (102) | 15,124 (102) | 15,302 (103) | n/a | n/a |

**Render only (presenter output prepared once).** This isolates the API.

| Benchmark | Writer | A | H | B |
| --- | --- | ---: | ---: | ---: |
| HostPage | discard | 8,435 (17) | 8,212 (17) | 9,070 (22) |
| HostFragment | discard | 361 (2) | 346 (2) | 386 (2) |
| HostOOB | discard | 514 (2) | 508 (2) | 524 (3) |
| HostOOBPair | discard | 533 (3) | 500 (3) | 507 (3) |
| HostSSE | discard | 2,384 (7) | 2,261 (7) | 1,947 (7) |
| HostError | discard | 2,817 (31) | 2,754 (31) | 2,850 (32) |

The buffer and http rows have the same shape (in the host set's benchstat output).

**What the numbers show.**
- **Page and fragment cost.** Most of the handler-path cost is the presenters, which do the URL, ID, and label derivation that templ does inline. HostError's ≈2.8 µs includes `slog` formatting the logged error (not profiled).
- **SSE.** SSE is the only row where A or H is slower than B, by 0.10–0.23×. Templ is not. Every SSE path makes the same single public call, so the gap is in the generated body (repetition preflight and clause dispatch over a small bubble), not in public-API dispatch. It was not profiled.
- **Templ's allocations.** These come from `templ.Attributes` spreads, class composition, and the pooled bufio writer.
- **HostError (asserted by tests for A, H, and B).** `ErrInvalidState` comes from the list item's action-selector component with input `action` and `Partial` = true. The failing component writes no bytes, and its ancestors' bytes are an exact prefix of the valid output. The buffered Renderer turns it into a clean `500`. A page canceled before its first byte gets a clean `500`. A page failing mid-stream stays a truncated `200` without a 5xx body.

### Synthetic benchmarks

`bench/renderapi`, ns/op (allocs/op), with `w=discard`. Buffer rows and ratios are in its README.

| Benchmark | A | H | B | templ | handwritten |
| --- | ---: | ---: | ---: | ---: | ---: |
| Static (4,144 B) | 15.0 (0) | 14.7 (0) | 15.2 (0) | 78.0 (3) | 3.1 (0) |
| NestedSame (depth 8) | 300 (9) | 299 (9) | 164 (0) | 1,854 (71) | 85.8 (1) |
| NestedCross | 279 (9) | 323 (9) | 187 (0) | 1,872 (71) | 88.7 (1) |
| NestedSlots | 305 (9) | 307 (9) | 210 (0) | 3,202 (111) | 152 (1) |
| ListHeavy (1,000 × 3) | 125,932 (6) | 124,128 (6) | 124,240 (6) | 489,868 (15,988) | 62,810 (1) |
| FormHeavy (40 fields) | 7,636 (95) | 7,762 (95) | 7,734 (95) | 27,896 (852) | 4,580 (1) |
| ScriptHeavy (hand-lowered JSON/script) | 20,685 (4) | 20,786 (4) | 20,856 (4) | 34,936 (538) | 13,106 (1) |

Geomeans over all synthetic rows: A/B 1.194, H/B 1.213, A/templ 0.257, and
H/templ 0.261.

The nested rows are the only synthetic rows where A or H trail B (1.3–1.8×,
+9 allocs):
- **Closure allocations.** Each `children` fill closure is stored in a
  `ktml.Component` field and escapes. It captures the caller's `props`, so the
  root props escape too.
- **B avoids this.** B's concrete `ktml.ComponentFunc` field does not escape.
- **Cross-package spike reversed.** H's cross-package call was 16% slower than
  A's here, the reverse of the earlier spike; it was not profiled.
- **Synthetic only.** These rows are not part of the decision rule. On the host
  page the effect is lost in the noise.

### Generated size

| Corpus | A | H | B |
| --- | ---: | ---: | ---: |
| Host `*_ktml.go` (production) | 97,914 B / 1,872 lines | 91,510 B / 1,773 lines | 92,499 B / 1,773 lines |
| Host linked `.text` delta (prototype module's size script) | 91,008 | 88,928 | 87,456 |
| Synthetic generated | 48,068 B / 1,053 lines | 45,751 B / 1,024 lines | 46,274 B / 1,024 lines |
| Synthetic linked `.text` delta | 105,984 | 104,448 | 102,464 |

### Call-site ergonomics and codemod

The codemod prototype scans the host application's Go code at the recorded
revision. It finds 19 call sites of the ported components, in handlers, the
renderer, and tests:
- **Automatic sites.** 16 (84%) are rewritten automatically, using the templ
  parameter → presenter → KTML input map.
- **Manual sites.** 3 are reported as manual:
  - the `Layout` call and the `templ.WithChildren` call are the hand-edited
    Renderer;
  - the Layout contract test needs the new staging, impersonation, and children
    inputs.
- **Call-site spelling.** `.Render(ctx, w)`, `Render.Component`,
  `Render.Page`, and `SSERender` call sites keep their spelling.

| Measure | templ | A | H |
| --- | ---: | ---: | ---: |
| Characters, 16 automatic sites | 695 | 1,594 | 1,073 |
| Tokens, 16 automatic sites | 149 | 346 | 268 |
| Manual sites | – | 3 | 3 |
| Ergonomics score (chars + 50 × manual) | – | 1,744 | 1,223 |
| Codemod emitter lines (shared scanner ≈ 230 lines) | – | 3 | 3 |

### Hard gates

| Gate | A | H | Evidence |
| --- | --- | --- | --- |
| Byte-identical with the floor | pass | pass | host-set scenario tests (A, H, B, templ, handwritten; also render-only variants); synthetic `go test ./...`; gogen `testdata/apis` compiles and runs A, H, B with identical bytes. The interpreter half was deferred to Phase 3; ADR-0004's parity harness now covers it. |
| Every error kind reachable, typed, pre-write | pass | pass | `ErrWrite`, `ErrCanceled`, `ErrMissingContent` in `testdata/apis` per API; `ErrInvalidState` at depth 4 in the host set; `ErrDuplicateKey` in the board fixture; `Partial` in `rt` and `ktml.Render` tests |
| No reflection, maps, `context.WithValue`, templ in production files | pass | pass | gogen inspection test; the prototype module's `gates.sh` |
| Deterministic generation | pass | pass | gogen determinism tests per API; `gates.sh` regenerates and compares hashes |
| `go vet` and staticcheck clean | pass | pass | `gates.sh` (production and `ktmldev`); `bench/renderapi` |
| No `ktml/dev` in the production binary | pass | pass | `gates.sh`: `go version -m` and `go tool nm` on each production binary. The same check finds `ktml/dev` in the `ktmldev` build. |

### Decision rule

1. **Budget against B (Host\* handler path).**
   - A: geomean 1.003×, worst 1.100× (HostSSE/discard), allocs ≤ B. **Within budget.**
   - H: geomean 1.001×, worst 1.149× (HostSSE/buffer), allocs ≤ B. **Within budget.**
   - The render-only variants agree: A 1.012× (worst 1.225×) and H 0.988× (worst 1.161×), both within budget.
2. **Templ parity (Host\*, excluding HostError, which templ cannot express).**
   - A: 0.572× templ, with fewer allocations on every row.
   - H: 0.570× templ, with fewer allocations on every row.
   - Both reach parity.
3. Not applicable: both A and H are within budget.
4. **Ergonomics.** H 1,223 against A 1,744. H is 30% lower, which is beyond the 5% tie band. **H wins.**
5. **Size check.** H's `.text` delta (88,928) is below A's (91,008), so H stands. Synthetic: 104,448 against 105,984.

**Outcome: Hybrid H (confirmed by the user on 2026-10-12).** The struct is the component, and it is the only exported identifier per component. Same-package calls are static internal calls, and cross-package calls use `(pkg.X{…}).Render(ctx, w)`. `gogen` emits only H. Prototype A's generator path is removed, and Prototype B's `RenderX` path is kept test-only as the benchmark floor (Decision 2).

### Q1 data: host page sizes

These are full-page responses from the host application's local server (in-memory fixtures), crawled two links deep: 66 page responses over 22 route templates.

| Measure | Bytes |
| --- | ---: |
| Layout shell (smallest page) | 10,835 |
| Median route template | 20,897 |
| Templates ≤ 16 KiB / ≤ 32 KiB / ≤ 64 KiB | 7 / 15 / 18 of 22 |
| Largest four route templates | 327,750 / 108,138 / 86,267 / 69,707 |

The prototype's representative list page (10 items) is 25.8 KB. The
local fixtures are small, so production pages, especially list pages, will be
larger.

**Decision (user, 2026-10-12): stream with a 32 KiB prefix buffer** (see Q1). The prototype Renderer implements it, with tests for a late failure under 32 KiB (clean 500) and a large page that streams.

The recommendation before the decision was:

**Recommendation.**
- **Keep pages streaming.** Do not buffer whole pages. Buffering would hold
  up to ≈330 KB per in-flight request and delay the first byte on the largest
  pages.
- **Clean 5xx when nothing was written.** The common failures (cancellation
  and Layout or page preflight states) happen before the first byte. With
  `Partial` and the doctype written with the first bytes, they become clean
  5xx responses, as the prototype shows.
- **Optional 32 KiB prefix buffer.** If more atomicity is wanted, a 32 KiB
  prefix buffer would make 15 of 22 route templates fully atomic while
  streaming the large ones. This adds a bounded copy per page.
- **This is the user's decision.**

### Q2: `ktml vet`

`ktml vet [patterns...]` (`internal/vet`) reports
`[missing-required-input]` for keyed props literals that omit a required input
or required content. It matches both `X{…}` (H) and `XProps{…}` (A/B).
It resolves cross-package selectors through imports and the nearest `go.mod`.
It skips positional literals, generated files, and handwritten same-name
types. It exits 1 on findings.

It is not yet surfaced through the LSP:
- the LSP relays plugin diagnostics per document;
- the htmx plugin already publishes for `.go` URIs, and a second publisher
  would overwrite those diagnostics;
- Go diagnostics would also need to be refreshed when a `.ktml` file changes.

This is recorded as a follow-up.

### Deviations, hand-lowering, and gaps found

- **Hand-lowered.**
  - Synthetic `ScriptHeavy`'s `JSONValue` and `TrustedScript` elements.
  - Host attribute names containing `:`, `@`, or `.` (for example `hx-ext:inherited`, `@change`, `x-bind:value`). These are not in the KTML grammar, so the sources spell them `__c__`, `__at__`, and `__d__`, and the prototype module's generate script rewrites the generated strings.
  - The doctype, which KTML cannot express, is written by the Renderer with the Layout's first bytes.
- **Language gaps** (follow-ups for a language slice). The two above are needed by almost every host template.
- **Templ-copy normalizations for byte identity.**
  - Constant attribute values that contain `'`, `<`, `>`, or `&` are pre-escaped in the copied templ, because templ copies constants verbatim and KTML escapes them. The cost of a constant is unchanged.
  - TopBar's HTML comment is dropped.
  - KTML writes templ's inter-element spaces (for adjacent inline siblings on separate lines) as explicit `" "` text nodes.
- **Protocol deviations.**
  - The host benchmarks run in a nested module of the host application, as directed for item 9, rather than in `bench/renderapi`.
  - The ported host components consume presenter records, so the handler-path rows include presenter cost; the render-only rows exclude it.
  - B's content fields are `ktml.ComponentFunc`, not a new `RenderFunc` type.
- **Generator follow-ups (no output bugs).**
  - `ktml.Ptr` on literal inputs allocates (FormHeavy: 95 allocs).
  - A and H fill closures escape, together with the caller's props (+9 on deep nesting). Reduced to +1 for H on 2026-10-20 (see [Fill allocations](#fill-allocations-2026-10-20)).
  - Fill closures do not release a writer they wrap; generated code never passes them a writer other than `*rt.Writer`.
  - Interpolated attribute values use `rt.Text`, which writes the same bytes as `rt.Attr`.
- **Build-cache robustness.** A stale build-state entry whose output directory was deleted makes every later `ktml generate` fail (`outputOwners` resolves all entries) until `.ktml/` is removed.

### Fill allocations (2026-10-20)

Stored in a `ktml.Component` field and rendered through the interface, a fill
always escapes: an interface method call leaks its receiver
(`go build -gcflags=-m`: `leaking param content: props` on the callee's
render function), so neither a closure nor a pointer to a stack frame can stay
on the stack. What can be avoided is the allocation *per fill*. A struct with a
single pointer field is a direct interface value, so converting it to
`ktml.Component` stores the pointer without allocating. `gogen` (`fills.go`)
therefore rewrites, for H only, each fill closure whose only free variable is
the render function's `props` into

```go
type ktmlFill3Page struct{ props *Page }

func (ktmlFill ktmlFill3Page) Render(ctx context.Context, ktmlW io.Writer) error {
	props := ktmlFill.props
	// …the former closure body…
}
```

and a fill that captures nothing into an empty struct. `-gcflags=-m` on the
regenerated `bench/renderapi/h/nested` reports only `moved to heap: component`
per root render. Fills that capture any other local (a repetition item, a
bind, the development variant's instance) keep their closure and its one
allocation. The public shape is unchanged: content fields are still
`ktml.Component`, and handwritten callers still assign any component,
including `ktml.ComponentFunc`. B keeps its concrete `ktml.ComponentFunc`
fields. Zero is not reachable without changing the API: the props pointer
must flow through the interface call.

`bench/renderapi`, H, `-count=6 -cpu=1` (medians, ns/op, B/op, allocs/op):

| Benchmark | Writer | before | after |
| --- | --- | ---: | ---: |
| NestedSame | discard | 311.7 / 144 / 9 | 185.6 / 16 / 1 |
| NestedSame | buffer | 396.4 / 144 / 9 | 293.1 / 16 / 1 |
| NestedCross | discard | 330.3 / 144 / 9 | 208.2 / 16 / 1 |
| NestedCross | buffer | 408.2 / 144 / 9 | 292.1 / 16 / 1 |
| NestedSlots | discard | 326.5 / 144 / 9 | 240.1 / 16 / 1 |
| NestedSlots | buffer | 418.9 / 144 / 9 | 332.2 / 16 / 1 |

## Consequences

- Children stop riding in `context.Context`, which fixes the confirmed
  recursion defect and removes per-call `context.WithValue` allocations.
- Handwritten host-application Go keeps `.Render(ctx, w)` and a single `ktml.Component`
  currency. Most of the codemod is constructor rewriting and a type rename.
- Escaping lives in one runtime package shared with the interpreter.
- A generated package can fail generation because of a handwritten name
  collision. This is intentional.
- Required value inputs are not enforced by the Go compiler at Go call sites
  (enforced by `ktml vet`, Q2).

## Resolved Questions

Both questions were decided by the user on 2026-10-12; Q1's page policy was
completed from the item-9 measurement the same day.

- **Q1: Response buffering in the host application's renderer.** The KTML `Renderer` buffers
  HTMX fragments and SSE events, so any `RenderError` becomes a clean 5xx.
  Full pages stream through a **32 KiB prefix buffer** (decided by the user on
  2026-10-12 from the [item-9 page-size data](#q1-data-host-page-sizes)): a
  render error before the first 32 KiB has been flushed discards the buffer
  and sends a clean 500; after that the page streams, and `Partial` semantics
  apply (the error is logged and the response is left truncated). 15 of 22
  measured host route templates fit in the prefix. The buffer lives in the
  application's Renderer, not in generated code or the `ktml`
  runtime.
- **Q2: Required inputs at Go call sites.** PLAN's props-struct shape stays. A
  `ktml vet` analyzer, run in CI and surfaced through the LSP, flags
  generated-props literals that omit required fields. Positional required
  parameters are rejected because they change the documented shape and break
  call sites when inputs are reordered.

## Rejected alternatives

- **Context-carried children (templ/current `ktml.WithChildren`).** These cause
  unbounded recursion and an allocation per call, and they make data flow
  invisible.
- **Pure Prototype B.** See [Narrowing](#narrowing-the-field).
- **Buffering in generated code.** It would double-buffer `net/http` and hide
  the atomicity decision from the application.
- **Runtime dev-mode checks in production code.** These cost a `ctx.Value` per
  component. The build constraint costs nothing.
- **A separate generated subpackage per domain package.** It would add imports
  to every call site and diverge from PLAN's package placement.
