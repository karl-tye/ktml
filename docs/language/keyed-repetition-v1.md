# Keyed Repetition and Render Identity v1

- Status: Frozen slice contract (Delivery Slice 3)
- Date: 2026-10-12
- Parent contract: [Host Schema, Render Clauses, and Keyed Repetition v1](host-schema-render-clauses-keyed-repetition-v1.md)
- Builds on: [Host Schema Records v1](host-schema-records-v1.md),
  [Presentation States and Render Clauses v1](host-schema-render-clauses-v1.md),
  [ADR-0002](../adr/0002-typed-render-selection.md),
  [ADR-0003](../adr/0003-rendering-api.md)

This document freezes the parts of the parent contract that Delivery Slice 3
needs: Keyed Repetition syntax and typing, the one-set uniqueness preflight,
streamed rendering, development Render Identity markers, and keyed HMR
reconciliation in the browser. It records the smaller decisions the slice made
where the parent contract was silent.

Conditions, content inputs and slots, the childless-sibling merge fix, and
literal `"true"` attributes are **not** part of this slice; they land in the
next language-gaps slice. The server-side render registry (PLAN Immediate
Backlog items 10 and 11) is not part of it either.

Its interpreter release-gate layer is done: `dev/interp` renders it with
byte, write, and error parity against `gogen`
([ADR-0004](../adr/0004-development-interpreter.md)). The slice stays
unadvertised until `ktml dev` renders live instances through the interpreter
(PLAN Immediate Backlog items 10 and 11).

## Syntax

```ebnf
body_node        = text | element | keyed_repetition ;
keyed_repetition = "for", identifier, "in", path, "key", path,
                   "{", { body_node }, "}" ;
path             = identifier, { ".", identifier } ;
```

- `for` is a keyword only at the start of a body node: inside an element body,
  a component or `default` body, a Render Clause body, a component call's body,
  or another repetition body. `in` and `key` are keywords only in their
  positions in the header. Elsewhere all three remain ordinary identifiers, so
  `label for="email"` is unchanged.
- An element's unbraced attribute list ends before a `for` that is followed by
  `identifier in`, so `li class="x"` on one line and a repetition on the next
  are siblings. A repetition is never the unbraced single child of an element:
  `ul for item in items key item.id { … }` is two siblings. Write
  `ul { for … }`.
- The collection and key are bare dotted paths, not quoted interpolations.
  Paths have no calls, indexes, operators, literals, or whitespace around `.`.
- The body is always braced and may be empty (`{}`).
- There is no index binding, `else`, range, iterator, filter, sort,
  transform, tuple, or composite-key syntax. Top-level `for` is not a
  declaration and is a syntax error.

## Syntax AST

All ranges are zero-based, half-open source byte offsets.

`RepetitionNode` is a body node with:

- the whole range, from `for` through the closing `}`;
- `keyword_range` (`for`), `item` and `item_range`, `in_range`;
- `collection`, a `PathExpression` with its parts and ranges;
- `key_keyword_range` (`key`) and `key`, a `PathExpression`;
- `body`, its body nodes.

It round-trips through the AST JSON used by plugins with `_type`
`RepetitionNode`, in every body position.

## Semantic Model

### Scope and the item binding

- A repetition introduces one immutable item binding, visible in its body and
  in its key path only. The collection path is resolved in the enclosing
  scope.
- The item name may not equal a Component Signature input, a Render Clause
  binding in scope, or an enclosing repetition's item (`shadowing-binding`,
  at the item name). Nested repetitions therefore always use distinct names.
- Item bindings are tooling symbols: hover shows `item card: Card` and the
  repetition header, and definition,
  references, and rename cover the declaration and every path root that
  resolves to it.

### Collection

- The collection path is typed like any other path, with the existing
  `unknown-path-root`, `unknown-field`, `scalar-field-access`,
  `state-field-access`, `collection-field-access`, and `optional-value-access`
  diagnostics for its intermediate steps.
- A collection that resolves to anything other than `[T]` is
  `repetition-not-collection`, at the path.
- A collection whose final step is optional (`items?: [T]`, or a binding of an
  optional payload field) is `optional-collection`, at the path. Conditions do
  not exist yet, so the only narrowing is a Render Clause: bind the value with
  `Some` (`[items = Some, value as list]`) or carry a required collection in a
  Variant payload.
- Nil and empty Go slices are both empty collections.

### Key

- The key is the item itself or a dotted path rooted in it. Any other root is
  `key-not-rooted`, at the key path.
- The key must resolve to `string` or `integer`; anything else (`number`,
  `boolean`, a record, state, URL, or collection) is `invalid-key-type`.
- An optional final step is `optional-key`. Optional intermediate steps keep
  `optional-value-access`.
- Uniqueness is a runtime property of one execution and is never checked
  statically.

### Repetition source identity

Each repetition has a stable **source identity** within its component:
`r<n>`, where `n` counts the component's repetitions from zero in source
order (depth-first, pre-order) across the signature's `default` body and every
Render Clause body in source order. Body and key edits keep it; adding or
removing an earlier repetition renumbers the later ones, which HMR treats as
a structural change (see [Render Identity](#render-identity)).

## Rendering

### Production

Each executed repetition renders in two passes over the same slice:

1. **Preflight.** One key set is created per execution and every item key is
   added to it. A duplicate returns `*ktml.RenderError{Kind: ErrDuplicateKey}`
   with the component ID, `Input` set to the collection path spelling, and
   `Key` set to the canonical key, before any body of that repetition writes.
   Ancestors may already have written, as ADR-0003 allows.
2. **Stream.** Before each item, `ctx.Err()` is checked; a cancelled context
   returns `*ktml.RenderError{Kind: ErrCanceled}` wrapping the context error.
   The body is then written directly to the writer. No body HTML is buffered.

An empty collection writes nothing and allocates no set. Production output
contains only body HTML: no markers, comments, or key attributes.

The key set (`rt.StringKeys` or `rt.IntegerKeys`) holds up to eight keys in an
inline array that stays on the stack and compares them linearly; a larger
collection allocates one map sized to the collection on first use. It is one
set per execution in both modes.

**Canonical key encoding.** An `integer` key is its base-10 form (`42`,
`-7`). A `string` key is its Go-quoted form (`"42"`). The two encodings are
therefore distinct, and changing a key is a removal plus an insertion.

### Render Identity

The development variant surrounds every item with fragment boundaries:

```html
<!--ktml:item IDENTITY-->…item body…<!--/ktml:item IDENTITY-->
```

The grammar is unified with component instance markers
([ADR-0004](../adr/0004-development-interpreter.md) decision 4):

```ebnf
identity   = [ root ], frame, { " ", frame } | root ;
root       = "@", instance_id ;                     (* registry root *)
frame      = item_frame | site_frame ;
item_frame = component_id, "#", source_id, "=", canonical_key ;
site_frame = component_id, "#", site_id ;           (* c<n> or o<n> *)
```

- `component_id` is the canonical `<package ID>.<Name>` of the component whose
  source holds the repetition, call, or outlet (the same ID as
  `RenderError.Component`), and `source_id` is the repetition's `r<n>`.
- `site_id` is `c<n>` for a component call and `o<n>` for a content outlet,
  numbered per component in source pre-order across its default and clause
  bodies, like `r<n>`. The instance rendered at a call or outlet has the
  identity of its site.
- The frames are the complete ancestor chain, outermost first: the registry
  root (`@<id>`, present only for a tracked root render), then every enclosing
  call site, outlet, and repetition item. Within one component, nested
  repetitions extend their parent's identity. Called components and content
  receive the caller's identity through the development context, so their
  repetitions extend it across component boundaries. A fill closure extends
  the innermost caller item it was written in, or else the outlet that renders
  it.
- In the comment, `%`, `<`, and `>` are written as `%25`, `%3C`, and `%3E`, so
  a key can never end the comment. The identity is otherwise opaque: the
  browser compares identities for equality and never parses keys.
- A body may render zero, one, or several root nodes; the boundaries delimit
  them all.
- Component instances are marked by the same identities: a body that is
  exactly one HTML element carries `data-ktml-i="IDENTITY"` on it; a body that
  starts with `doctype`, or is exactly one call of such a document component,
  carries nothing; any other body is surrounded by
  `<!--ktml:c IDENTITY-->…<!--/ktml:c IDENTITY-->`, escaped like item
  boundaries. An instance without an identity (the root of an untracked
  render) carries no marker.

### Development and production variants

ADR-0003's build-constraint boundary is implemented for generated component
files. `gogen.GenerateFile` emits the production variant with
`//go:build !ktmldev`; with `Options.Development` it emits the development
variant with `//go:build ktmldev`. Both declare identical exported APIs. Only
the development variant imports `github.com/karl-tye/ktml/dev`, which writes
markers and carries the identity scope in `context.Context`. Production files
contain no markers, `ktml/dev` import, or `ctx.Value` lookups.

## Browser reconciliation

The livereload client morphs each patched element instead of replacing it:

- Elements with the same tag are updated in place: attributes are added,
  changed, or removed, text is updated, and children are reconciled.
  Different tags, or a node type change, replace the node.
- A run of sibling keyed fragments is reconciled by identity. Surviving
  fragments keep their DOM nodes and are moved, not recreated; only fragments
  outside the longest already-ordered subsequence move. Removed identities are
  removed and new identities are inserted. Surviving fragments are then morphed
  against their new content.
- Form state is preserved where safe: the live `value` and `checked` of a
  surviving input, textarea, or select are not overwritten by attribute
  changes, so user edits survive. If the focused element moves, it is focused
  again and its selection range restored.
- A patch with unbalanced or duplicate markers is unsafe and falls back to a
  full page reload.

Patches target a registry root by its identity (ADR-0004); the client is
described in `internal/dev/livereload/client.go`.

## Generated Go

```go
// production (//go:build !ktmldev)
{
	ktmlItems1 := props.Group.Items
	var ktmlKeys1 rt.StringKeys
	ktmlKeys1.Reset(len(ktmlItems1))
	for ktmlIndex1 := range ktmlItems1 {
		if !ktmlKeys1.Add(ktmlItems1[ktmlIndex1].Id) {
			return rt.DuplicateStringKey("example.com/app.Board", "group.items", ktmlItems1[ktmlIndex1].Id)
		}
	}
	for ktmlIndex1 := range ktmlItems1 {
		if err := ctx.Err(); err != nil {
			return rt.Canceled("example.com/app.Board", "group.items", err)
		}
		// body, reading the item as ktmlItems1[ktmlIndex1]
	}
}
```

- The item is read by index, so record items are not copied.
- The development variant is the same code plus `dev.Begin`/`dev.End`
  around each body, an identity built with `dev.StringFrame` or
  `dev.IntegerFrame`, and a `dev.WithScope(ctx, dev.CallFrame(…))` context on
  every component call and content outlet.
- Without a workspace snapshot, `gogen.Generate` rejects repetitions.

## gohtml

The experimental gohtml generator does not support repetitions. It fails
before any output on any `RepetitionNode`, in every body position.

## Formatting

```ktml
ul {
	for item in group.items key item.id {
		li "{item.title}"
	}
}
```

- The header is written on one line with single spaces; the body is indented
  one level. An empty body is written `{}`.
- Item, collection, and key spellings are preserved, so formatting is
  idempotent and parse/format/parse keeps the normalized conformance shape.

## Tooling, CLI, and HMR

- The CLI prints the same semantic diagnostics as every other slice and fails
  before output.
- The LSP publishes the diagnostics, completes collection paths (inputs,
  bindings, item names, and record fields after `.`), completes the item name
  as the key root and item fields after `item.`, and serves hover, definition,
  references, and rename for item bindings, and semantic tokens for `for`,
  `in`, `key` (keyword) and item bindings (parameter).
- Repetition edits inside a component body (collection, key, item name, or
  body) with an unchanged Component Signature and Host Schema are
  HMR-compatible and classified as one changed component, like any other body
  edit.

## Conformance

Manifest cases may declare `repetitions`. Both parsers assert, in source order
and through every nesting level, each repetition's whole range, `for` keyword,
item name and range, `in` range, collection path name, parts, and ranges,
`key` keyword range, and key path name, parts, and ranges. Nested repetitions
are listed after their parent, in pre-order.

## Allocation and throughput gate

The gate compares generated renderers (production variant) with equivalent
handwritten Go and templ (`v0.3.1020`) on byte-identical output:

- `Flat`: 1,000 records with string keys;
- `Nested`: 1,000 records, each with an optional field and a three-item nested
  keyed list (ADR-0003's `ListHeavy`);
- `Small`: 5 records, so the inline set is measured;
- `Empty`: an empty collection.

It passes when, for every shape, generated Go has allocs/op ≤ templ and a
ns/op geomean ≤ 1.00× templ, and generated-source inspection finds exactly
one key set per repetition, declared once per execution, and no buffer of
body HTML. Handwritten Go with and without the preflight is reported as the
lower bound. Results live in [Keyed Repetition benchmark](keyed-repetition-benchmark.md).

## Deferred

- Done in items 10 and 11 (ADR-0004): `ktml dev` swaps the app's interpreter
  program and patches keyed HTML rerendered from the server-side registry, and
  the client disposes and initializes Alpine scopes and calls `htmx.process`
  on inserted fragments.
- Conditions, content inputs and slots, the sibling-merge grammar fix, and
  literal `"true"` attributes (language-gaps slice).
- Generated-Go source maps for repetitions.
