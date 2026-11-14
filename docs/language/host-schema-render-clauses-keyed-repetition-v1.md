# Host Schema, Render Clauses, and Keyed Repetition v1

- Status: Accepted target contract; Delivery Slice 1 is frozen and implemented
  by [Host Schema Records, Imports, and Typed Paths v1](host-schema-records-v1.md);
  Delivery Slice 2 by [Presentation States and Render Clauses v1](host-schema-render-clauses-v1.md);
  Delivery Slice 3 by [Keyed Repetition and Render Identity v1](keyed-repetition-v1.md);
  the language-gaps slice (content inputs, conditions, element lines, and
  literal attributes) by [Element Lines, Literal Attributes, Content Inputs, and Conditions v1](language-gaps-v1.md)
- Date: 2026-09-10
- Decision: [ADR-0002](../adr/0002-typed-render-selection.md)

This document specifies the next language contract. Current parser, formatter,
semantic, generator, LSP, Explorer, and HMR behavior must not be assumed to
support it until each delivery slice passes its release gates.

## Terms

- **Host Schema**: the language-neutral contract for values an application may
  expose to KTML.
- **Component Signature**: the one public typed input and slot contract shared
  by every render of a component.
- **Presentation State**: a closed nominal set of mutually exclusive Variants
  prepared by Go for rendering.
- **Variant**: one named case of a Presentation State, optionally carrying a
  named record payload.
- **Render Clause**: one component body selected by matching a Pattern against
  the component's sole selector.
- **Pattern**: a finite structural selector consisting of a Variant, boolean,
  `Some`, or `None`.
- **Keyed Repetition**: structural mapping of a prepared collection into
  repeated render fragments with explicit stable keys.
- **Render Identity**: development identity derived from component identity,
  repetition source identity, the current key, and the full ancestor-key chain.
- **Business Derivation**: Go code that transforms domain/request values into
  Host Schema values and semantic rendering facts.

## Host Schema

### Grammar

```ebnf
schema_document   = { import_declaration | record_declaration | state_declaration } ;
import_declaration = "import", "{", import_name, { ",", import_name }, [","],
                     "}", "from", string_literal ;
import_name       = type_identifier, [ "as", type_identifier ] ;
record_declaration = "record", type_identifier, field_list ;
state_declaration  = "state", type_identifier, "{", variant_list, "}" ;
variant_list       = [ variant_declaration, { ",", variant_declaration }, [","] ] ;
variant_declaration = type_identifier, [ field_list ] ;
field_list         = "[", [ field_declaration, { ",", field_declaration }, [","] ], "]" ;
field_declaration  = field_identifier, ["?"], ":", type ;
type               = scalar_type | contextual_type | type_identifier | collection_type ;
scalar_type        = "string" | "boolean" | "number" | "integer" ;
contextual_type    = "URL" | "TrustedURL" | "CSSValue" | "JSONValue" |
                     "TrustedHTML" | "TrustedScript" ;
collection_type    = "[", type, "]" ;
type_identifier    = upper, { letter | digit } ;
field_identifier   = lower, { letter | digit } ;
```

Imports use the canonical explicit import form:

```ktml
import { SearchItem, SearchState as State } from "./search.ktmlschema"
```

The same form is valid in `.ktml` Documents. A component Document imports
Components from `.ktml` and record/state symbols from `.ktmlschema`; a schema
Document imports only record/state symbols from `.ktmlschema`. Components and
types resolve in distinct namespaces. Wildcard and side-effect imports are not
accepted.

Schema field names are lowerCamel ASCII identifiers. The Go generator exports a
field or accessor by uppercasing only the first ASCII letter. For example,
`displayName` becomes `DisplayName` and `id` becomes `Id`. There is no initialism
dictionary or target-specific source annotation. Two source names that map to
one generated name are an error.

`integer` is a signed 64-bit value and has canonical base-10 formatting. `number`
is not accepted as a Render Identity key. Optional fields use `name?: T`; Go
represents absence as `*T == nil`, while KTML models it as `None | Some(value)`.
Nil and empty Go slices both represent an empty `[T]` collection.

Host Schema dependency graphs must be acyclic. Records and states cannot embed
themselves directly or indirectly. Schema defaults, anonymous unions, open
variants, methods, behavior, validation expressions, and target-language names
are not part of this contract.

### Example

```ktml
record SearchItem [
  id: string,
  title: string,
  url: URL,
]

state SearchState {
  Loading,
  Loaded [items: [SearchItem], total: integer],
  Failed [message: string],
}
```

Generated Go schema packages contain exported record values and sealed state
interfaces. State implementations are private. Public typed constructors create
nullary and payload Variants; public discriminators and payload accessors let
generated renderer packages inspect imported states without exposing concrete
variants. A nil state interface is invalid.

## Render Clauses

### Grammar

The following grammar extends canonical component declarations:

```ebnf
component_group     = component_signature, render_clause, { render_clause } ;
component_signature = identifier, input_list, [ "default", body ] ;
input_list          = "[", [ input_declaration, { ",", input_declaration }, [","] ], "]" ;
input_declaration   = identifier, ["?"], ":", component_type,
                      [ "=", scalar_literal ] ;
component_type      = type | "content" ;
scalar_literal      = string_literal | boolean_literal | numeric_literal ;
render_clause       = identifier, "[", selector_match,
                      { ",", payload_binding }, "]", body ;
selector_match      = identifier, "=", pattern ;
pattern             = qualified_variant | "true" | "false" | "Some" | "None" ;
qualified_variant   = type_identifier, [ ".", type_identifier ] ;
payload_binding     = field_identifier, [ "as", identifier ] ;
```

`identifier`, `body`, literal tokens, `upper`, `lower`, `letter`, and `digit`
retain their canonical shared lexical/concrete grammar definitions. A numeric
literal has one lexical form and is interpreted using its expected type. An
`integer` literal cannot contain a fraction or exponent and must fit signed 64-bit;
the same whole-number spelling remains a `number` when that is the expected type
or when an output position provides no unique expected numeric type.
As in the frozen input contract, `?` and a default are mutually exclusive,
defaults are type-correct scalar literals, and content inputs cannot have
defaults.

An ordinary bodyful component declaration remains valid and is not a Component
Signature for Render Clauses. Following it with a same-name clause is an error.
A Component Signature without `default` is bodyless. A Component Signature with
`default` must be followed by at least one Render Clause.

### Grouping and Resolution

- The signature and clauses are contiguous declarations in one Document.
- The next unrelated top-level declaration terminates the group.
- Clauses cannot precede a signature, resume later, cross Documents, or extend
  imported components.
- Every clause repeats the component name and matches the same signature input.
  That input is the sole selector; a second matched input is an error.
- A selector must be a closed Presentation State, `boolean`, or optional value.
  A defaulted selector is matched after default substitution. Omission of an
  optional selector becomes `None`.
- Variant names resolve contextually through the selector type. Qualification by
  state name is allowed. Definitions, references, and rename bind the Pattern to
  the Variant symbol, not its text.

### Payload Bindings and Scope

Entries after the Pattern bind same-name fields from the selected Variant's
payload. `field as local` renames a binding. Bindings are immutable, may be
partial, and infer their types from the Host Schema. Unknown, duplicate, or
shadowing bindings are errors. Nullary Variants and `None` accept no bindings.
`Some` exposes one built-in payload field named `value`.

All Component Signature inputs, including the original selector, remain in
scope. Payload fields not named in the clause header are not introduced as local
bindings. The selector may be passed intact to another component.

```ktml
Subtitle [subtitle?: string]

Subtitle [subtitle = None] {
  span class="muted" { "No subtitle" }
}

Subtitle [subtitle = Some, value as text] {
  span text
}
```

Children and named slots have one shared Component Signature. A clause group
must declare every content input explicitly; using an outlet in a clause cannot
implicitly change the component API. A clause may choose not to render supplied
content.

### Exhaustiveness and Dispatch

- Clauses are declarative and source order does not affect dispatch.
- Two clauses for the same Variant, boolean value, or option case are an
  overlap error. There are no guards, priorities, wildcard, or specificity.
- A bodyless Component Signature must cover every Variant, both booleans, or
  `Some` and `None`. A gap is a fatal semantic diagnostic.
- A `default` body is the residual set after specialized clauses. Every Variant,
  boolean case, or option case handled only by it produces a nonfatal warning
  listing those residual cases.
- A `default` body with complete specialized coverage is unreachable and
  produces a nonfatal warning.
- The semantic analyzer owns identical warning codes and ranges for CLI, LSP,
  CI, and Explorer consumers.
- A valid closed selector always resolves to one body. Nil or otherwise invalid
  state is rejected before that component writes output; it is never routed to
  `default` and never renders empty output.

```ktml
SearchResults [state: SearchState, query: string]

SearchResults [state = Loading] {
  SearchSkeleton
}

SearchResults [state = Loaded, items, total] {
  Results items=items total=total query=query
}

SearchResults [state = Failed, message as errorMessage] {
  ErrorNotice message=errorMessage
}
```

### Conditions

Render Clauses are the only finite state-selection construct. Ordinary
conditions remain for orthogonal facts and have this closed grammar:

```ebnf
condition  = or_condition ;
or_condition = and_condition, { "||", and_condition } ;
and_condition = unary_condition, { "&&", unary_condition } ;
unary_condition = ["!"], ( path | presence | "(", condition, ")" ) ;
presence   = path, "?" ;
path       = identifier, { ".", identifier } ;
```

Scalar equality, ordering, truthiness, calls, indexing, arithmetic, assignment,
and conversion are not conditions. Go derives those decisions into booleans,
options, or Presentation States.

## Keyed Repetition

### Grammar

```ebnf
keyed_repetition = "for", identifier, "in", path, "key", path, body ;
```

The collection path must resolve to `[T]`. An optional collection must first be
narrowed or carried by a Variant whose clause binds it. The loop introduces one
immutable item binding. The key is either that scalar item itself or a field
path rooted in it and must resolve to `string` or `integer`.

There is no index binding, `else`, range, iterator protocol, filter, sort,
transform, method, call, tuple, or composite-key expression. Go derives stable
composite keys into a string or integer item field.

### Rendering and Identity

- An empty collection renders no loop body and is not an error.
- The body may contain arbitrary declarative markup, components, conditions,
  slots, and nested Keyed Repetitions. It may render zero, one, or multiple root
  nodes for an item.
- Per-item conditions may suppress or vary output, but do not change the
  collection being iterated. Meaningful filtering, sorting, and grouping occur
  during Business Derivation.
- Each nested repetition declares its own key. Uniqueness is scoped to immediate
  siblings in one execution; equal child keys under different parent keys are
  valid.
- Before rendering a loop body, generated Go and the development interpreter
  preflight all keys with one set. A duplicate returns a render error before any
  body in that repetition writes. Rendering then streams a second pass without
  buffering body HTML.
- String and integer keys use distinct canonical encodings. Changing a key is a
  removal plus insertion, not an update.
- Production emits only body HTML. Development surrounds each iteration with
  fragment boundaries identified by component identity, repetition source
  identity, the current key, and the complete ancestor-key chain.
- HMR insertion, removal, and reorder reuse or move surviving keyed fragments,
  preserve focus/form/Alpine state where safe, initialize inserted HTMX/Alpine
  content, dispose removed scopes, and fall back to reload when morphing is
  unsafe.

## Go and Plugin Boundaries

Only generated Host Schema nominal values, KTML scalars, and core contextual
wrappers cross into generated component APIs. Page- or fragment-level presenters
perform Business Derivation. Literal copy and CSS classes remain in KTML so body
edits can use HMR; dates, currency, domain values, permissions, comparisons,
grouping, ordering, filtering, URLs, JSON, and trusted values are prepared in Go.

`context.Context` is available to generated renderers only for runtime concerns
such as cancellation, child rendering, and development tracking. KTML cannot
read it. Locale, actor, CSRF, route, and request values are explicit inputs.

Go alone constructs `URL`, `TrustedURL`, `CSSValue`, `JSONValue`, `TrustedHTML`,
and `TrustedScript`. KTML has no constructors, casts, raw-output operator,
ordinary script interpolation, or automatic JSON conversion. User-displayable
failures are explicit Presentation State Variants. Renderer errors are limited
to writer, cancellation, invalid-state, duplicate-key, and child-render failures.

Official and third-party library plugins share generic contracts. A plugin may
provide attribute/directive schemas, output-context classification, route data,
completion, diagnostics, and embedded-language analysis. It cannot add syntax,
types, control flow, render transforms, or output bytes. Static custom attributes
do not require a plugin; dynamic custom attributes require a pinned active plugin
schema that declares their context and accepted type.

## Compatibility and Tooling

- Valid clause Pattern/body edits with an unchanged Component Signature and Host
  Schema are HMR-compatible.
- Every Host Schema change regenerates Go, restarts the application, invalidates
  incompatible Render Instances, and reloads the browser.
- Required signature additions, removals, renames, and type changes rebuild and
  reload. Existing optional/defaulted signature compatibility rules remain.
- Explorer controls select a Variant and then edit its typed payload. Optionals
  expose `None`/`Some`. Trusted values require explicit fixtures and are never
  unrestricted text controls.
- LSP support includes diagnostics, completion, hover, definition, references,
  rename across `.ktmlschema` and `.ktml`, semantic tokens, and generated-Go
  source maps. Updating handwritten Go references during schema rename may ship
  later, but generated symbols must map back to schema declarations.
- Formatter output is idempotent and preserves every signature, `default`,
  Pattern, payload binding, key, and source association through parse/format/parse.

## Delivery Slices

No language slice starts until the current LSP and HMR high-priority repair gate
is independently approved.

1. Host Schema records, explicit imports, scalar/option/collection lowering,
   generated Go records, and typed dotted field-path resolution.
2. Closed Presentation States, generated constructors/accessors, Render Clause
   syntax and grouping, finite Patterns, payload binding, dispatch,
   exhaustiveness, and the explicit `default` behavior.
3. Keyed Repetition typing and rendering, uniqueness preflight, nested Render
   Identity, and keyed HMR insertion/removal/reorder.

The first state proof is a shop checkout payment lifecycle fixture containing
nullary and payload Variants, distinct SSR shapes, and trusted URL boundaries.
Before broader schema rollout, compare its presenter glue and invalid-state
elimination with the templ/Go equivalent. Keyed repetition must be benchmarked
against equivalent templ and handwritten Go.

## Release Gates

Each syntax slice updates and verifies the canonical contract, Go parser,
Tree-sitter corpus, formatter, typed semantic model, interpreter, direct Go
generator, CLI integration, LSP behavior, Explorer controls when applicable,
HMR classification, source maps, and behavioral tests. No backend-only or
parser-only path is releasable.

Required verification includes focused red/green tests, `./test-conformance.sh`,
the complete non-browser Go suite, relevant `TestApplication` end-to-end tests,
build, `go vet`, staticcheck, and generated-artifact inspection. Browser E2E runs
only when relevant while its known `exec.WaitDelay` cleanup defect remains; a
passing assertion followed by harness cleanup failure is recorded separately,
and only test-created orphan processes verified by PID, start time, and port may
be terminated.

The feature is advertised only after all vertical behavior passes. The Host
Schema approach additionally remains gated by the payment proof, and keyed
repetition remains gated by allocation/throughput results plus generated-source
inspection confirming one preflight set and no body buffer.
