# In-place conversion and the call-site codemod

A templ application converts to KTML in place, batch by batch, in
dependency order. This document covers the `ktml migrate templ --in-place`
mode, the `ktml migrate codemod` command, and how converted and
unconverted templates call each other in between.

## A batch, step by step

```bash
# 1. Convert: outputs go into the source packages.
ktml migrate templ ./internal/orders ./internal/ui/form.templ … \
  --in-place --scaffold-presenters --report .ktml/migrate/batch.json
# 2. Finish the partial nodes by hand in the outputs (kept from now on).
# 3. Rewrite the callers; first without --write, to read the report.
ktml migrate codemod ./... --map .ktml/migrate/batch.json \
  --report .ktml/migrate/codemod.json --write \
  --fragment-render github.com/…/internal/web.RenderFragment --tags e2e
# 4. Edit the manual sites the codemod lists, then remove the templ files.
ktml migrate templ <same batch> --in-place --scaffold-presenters \
  --report … --remove-templ [--hand-finished]
# 5. Generate and test.
go tool ktml generate ./internal/... && go build ./... && go test ./...
```

## In-place mode

- **Where outputs go.** For each templ file, the `.ktml` document, the
  package's `.ktmlschema`, a `_present.go` presenter, the package's
  `migrate_helpers.go`, and a `_bridge.go` (below) are written next to it.
  Generated Go stays uncommitted, as ADR-0003 prescribes. Source maps go
  to `<root>/.ktml/migrate/`, so converter metadata stays out of the
  packages.
- **One package scope.** Presenters are in the package that declared the
  templates, so they call its unexported helpers and use its types
  unqualified. The templ file's Go declarations move into the presenter,
  including exported types and methods. This removes the separate-package
  partial diagnostics: on a large production application, 371 partial
  nodes became 13.
- **Names.** Records and presenters avoid the package's handwritten
  names. A component whose Go type collides with a handwritten
  declaration is a partial `generated-name-collision` node.
- **Kept outputs.** A second run keeps every existing output unless
  `--force` is given, because presenters are finished by hand. Type loading
  hides earlier converter outputs (`_present.go`, `migrate_helpers.go`,
  `_bridge.go`, and generated KTML Go) in packages that still have templ
  files, since the two do not type-check together. It overlays freshly
  generated Go for every templ file of the module, and freshly generated
  KTML Go for every valid `.ktml` and `.ktmlschema` whose templ file is
  gone, so a batch needs no `ktml generate` of earlier batches first.
- **Hand-added bridge fields.** A bridge finished by hand can fill a
  field no templ parameter maps to (for example a `MessagingPanel` and
  an `Associations` slot). When a templ caller of the bridge converts, its
  KTML call is partial, with a TODO naming the field and its Go value.
- **Kept outputs and later converter fixes.** Each output the converter
  writes is also copied, as written, to
  `.ktml/migrate/kept/<output>.pristine`. A later run compares its new
  emission of each kept output with that copy:
  - equal: the kept file differs only by hand edits, and nothing is
    reported;
  - different (a converter fix, or a changed templ source): the new
    emission goes to `<output>.fresh`, the output is listed in the
    report's `outdated` with a `kept-output-outdated` review diagnostic,
    and `--remove-templ` refuses until it is merged:

    ```bash
    git merge-file internal/x/y_present.go \
      .ktml/migrate/kept/internal/x/y_present.go.pristine \
      .ktml/migrate/kept/internal/x/y_present.go.fresh
    mv .ktml/migrate/kept/internal/x/y_present.go.{fresh,pristine}
    ```

  Outputs written before this check have no pristine copy and are not
  compared. `--force` still overwrites every output, hand edits included.
  The schema and `migrate_helpers.go` are merged, not kept, and are not
  compared.
- **Loop keys.** An ID-like field (`ID`, `Key`, `Code`, …) becomes the
  loop key with an `identity-key` review diagnostic and a TODO: the
  converter cannot prove it unique and non-empty, and a repeated key is a
  render error. A nil-able ID (a pointer, interface, …) is never keyed by
  `fmt.Sprint`, which prints `<nil>` for every nil ID: the loop is keyed by
  position with a `position-key` review.
- **Removing the templ files.** `--remove-templ` deletes the batch's
  `.templ` and `_templ.go` files only when the batch is complete:
  - every file was converted, and the KTML validates;
  - no node is partial or blocked, unless `--hand-finished` says the kept
    outputs finish them;
  - no Go or templ code in the module still uses a batch template. The
    codemod scan decides this: a rewritable site means "run the codemod",
    a manual site means "edit by hand".

  Otherwise the report's `removeRefused` lists why.
- **Call mappings.** The report's `conversion.calls` gives, for every
  template, its KTML component, presenter, view field, and where each templ
  parameter goes (presenter argument, scalar field, content slot, or
  attribute bag), plus the implicit context and clock arguments. The
  codemod reads it.

## The Go call-site codemod

`ktml migrate codemod` loads the module's packages, tests included, with
every templ file's Go generated as an overlay. It finds every use of a
mapped template:

| Use | Result |
| --- | --- |
| `pkg.X(a, b)` anywhere: handlers, `Render.Component`, `Page{Content: …}`, `templ.Handler(…)`, `SSERender(…)`, tests | `pkg.X{View: pkg.NewXView(a, b)}`, or the direct component's fields (`int64(n)` for integers, content slots, `ktml.Join` for several) |
| `pkg.X(a).Render(ctx, w)` into an `http.ResponseWriter` | `RenderFragment(ctx, w, pkg.X{…})` with `--fragment-render`, which buffers (ADR-0003); without it, rewritten in place with a review note |
| a presenter taking the request context | `ctx` in scope, else `r.Context()` of an `*http.Request`, else `t.Context()` of a `*testing.T`; with none, manual |
| a presenter reading the clock | `time.Now()` at construction, with a note: templ read it at render |
| a call in an `if`/`for`/`switch` header | parenthesized literal |
| a call in an unconverted `.templ` file | renamed to the bridge, `@ui.X(` to `@ui.XTempl(`, with its body kept; the file's `_templ.go` is regenerated with the pinned templ generator |

Every other use is a **manual** site, with its source range and reason,
and is never edited:

- the template used as a Go function value;
- a template with `{ children... }`, which Go passes through
  `templ.WithChildren` (also reported on its own);
- a non-nil `templ.Attributes` argument, which needs a `ktml.Attributes`
  from the context-checked constructors;
- a spread `...` into content;
- a method template whose package the file does not import;
- a hand-finished presenter that returns an error, or whose parameters
  changed;
- a component whose declared inputs no longer match the mapping (an added
  slot, or a view replaced by Presentation State inputs).

`templ.Component` types (fields, parameters, maps, function types) are
**deferred**: KTML values are assignable to them, and a type rename is
only safe once nothing renders templ, at cutover. The output is gofmt'd,
imports are added as needed, and a second run changes nothing (an
already-rewritten literal is recognized and skipped). Without `--write`,
nothing is written. The command exits 1 when a manual site remains.

## Interop during a migration

`ktml.Component` and `templ.Component` have the same method set
(ADR-0003), so a component value of either kind is assignable to the
other's type. This covers every Go boundary without adapters: the
Renderer, `Page.Content`, `SSERender`, `templ.Handler`, Go-built slot
content, and maps or fields of `templ.Component`.

- **A converted component used by an unconverted templ parent.** templ
  markup can only call functions, and the KTML struct takes the template's
  name. The converter emits a **templ bridge** for each exported template
  that some unconverted `.templ` file calls (`--bridges auto`, the
  default). A bridge is `XTempl` with the templ signature, building the
  component through its presenter at render time and passing templ's
  context-carried children into the `Children` slot:

  ```go
  func PanelTempl(item Item) templ.Component {
      return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
          return Panel{View: NewPanelView(item), Children: templ.GetChildren(ctx)}.Render(templ.ClearChildren(ctx), w)
      })
  }
  ```

  The codemod renames the templ call sites. A bridge is deleted when
  nothing calls it, at the latest at cutover.
- **A converted parent calling an unconverted templ child.** KTML markup
  calls only KTML components, and records cannot hold content (ADR-0002).
  A Go-built templ component can fill a KTML slot. For calls in markup,
  batches follow the dependency order: a call to a template outside the
  batch is a partial `unconverted-dependency` node ("convert it first, or
  in the same batch"). Shared `ui` files usually convert in the same batch
  as their first callers.
- **The next wave.** When a later batch converts a templ parent that calls
  an earlier batch's component through its bridge, the converter reads the
  bridge's component literal: the component, its presenter, and where each
  parameter goes. It emits a KTML call of the component, importing its
  document and view record, so mixed trees convert wave by wave.

## Semantics the converter now keeps

Golden comparisons with templ output exposed two places where a straight conversion
silently changed output. Both are fixed:

- **Forwarded absent content.** templ passes `nil` for an absent content
  argument; a KTML slot fill is always present. When a template passes its
  own optional content to a callee that compares it with `nil` (directly
  or further down), the call is emitted as
  `if icon? { Child { slot icon { icon } } } else { Child }`.
- **Empty content lists.** A bridge joins a variadic content parameter
  with `migrateJoin`, which returns absent content for an empty list, as
  templ's `len(children) > 0` sees it.

## Limits

- The call mapping describes the conversion as emitted. After hand
  edits, the codemod checks presenter signatures and component inputs,
  but not a hand-changed parameter order with the same count.
- Clock reads move from render to construction; the codemod notes each
  one.
- A bridge evaluates the presenter at render time, as templ did. A Go
  call site builds the view when the literal is built, which differs only
  if the arguments change in between.
- Generated KTML Go must exist before a later batch converts bridge
  calls, because the converter reads the presenter's result type
  (`go tool ktml generate` first).
- Source maps are written but not yet read by the LSP.
