# Presentation States and Render Clauses v1

- Status: Frozen slice contract (Delivery Slice 2)
- Date: 2026-10-12
- Parent contract: [Host Schema, Render Clauses, and Keyed Repetition v1](host-schema-render-clauses-keyed-repetition-v1.md)
- Builds on: [Host Schema Records v1](host-schema-records-v1.md),
  [ADR-0002](../adr/0002-typed-render-selection.md),
  [ADR-0003](../adr/0003-rendering-api.md)

This document freezes the parts of the parent contract that Delivery Slice 2
needs: closed Presentation States, generated sealed Go state interfaces, Render
Clause syntax and grouping, finite Patterns, payload bindings, dispatch,
exhaustiveness, and the explicit `default` body. It also freezes the minimum
`URL`/`TrustedURL` boundary that the payment proof needs. It records the
smaller decisions the slice made where the parent contract was silent.

Conditions (`if`/`else`, `!`, `&&`, `||`, presence) are **not** part of this
slice. The parent contract's delivery list places them in no slice yet, and
the parser has no condition syntax. Keyed Repetition remains slice 3.

Its interpreter release-gate layer is done: `dev/interp` renders it with
byte, write, and error parity against `gogen`
([ADR-0004](../adr/0004-development-interpreter.md)). The slice stays
unadvertised until `ktml dev` renders live instances through the interpreter
(PLAN Immediate Backlog items 10 and 11).

## Syntax

```ebnf
document            = { import_declaration | record_declaration
                      | state_declaration | component | render_clause } ;
state_declaration   = "state", identifier, "{",
                      [ variant_declaration, { ",", variant_declaration }, [","] ],
                      "}" ;
variant_declaration = identifier, [ "[", [ field_declaration,
                      { ",", field_declaration }, [","] ], "]" ] ;
component           = identifier, input_list, ( body | "default", body | (* none *) )
                    | identifier, body ;
render_clause       = identifier, "[", identifier, "=", pattern,
                      { ",", payload_binding }, "]", body ;
pattern             = identifier, [ ".", identifier ] ;
payload_binding     = identifier, [ "as", identifier ] ;
```

- `state` is a keyword only at the start of a top-level declaration, exactly
  like `record`. A state body uses braces; a payload uses the record field
  brackets.
- **Clause versus legacy override.** A bracketed header whose first entry is
  `name = pattern` with an identifier pattern (`Loading`, `State.Loading`,
  `true`, `false`, `Some`, `None`) is a Render Clause. A first entry whose value
  is a string or number literal (`Button [disabled="true"]`) remains the legacy
  gohtml-only override. This rule is context-free, so both parsers agree
  without tracking groups. An unquoted boolean override (`[disabled = true]`)
  is therefore a clause. No fixture or test used that spelling; ADR-0001 already
  supersedes overrides.
- A clause header contains the selector match, then zero or more bindings
  separated by commas. A trailing comma is not accepted, because the formatter
  always writes a clause header on one line.
- **`default`** is a keyword only directly after a component header's closing
  `]`, and it must be followed by a body. A component literally named
  `default` cannot follow a bodyless signature. `as` is a keyword only inside a
  clause header after a binding name, and in import braces.
- **Bodyless signature.** A header with no body and no `default` ends at its
  `]`. A component without an input list still needs a body.
- `.` is a token only inside a qualified Pattern.
- Comments keep the `//` line form and are not preserved by the formatter.

## Syntax AST

All ranges are zero-based, half-open source byte offsets.

- `StateDecl`: whole declaration, `keyword_range`, `name` and `name_range`,
  `variants` in source order. Its `E` is the end of the closing `}`.
- `VariantDecl`: whole variant, `name`, `name_range`, `payload` (true when
  brackets are present), `payload_range`, and `fields` (`FieldDecl`, exactly as
  in records).
- `ElementDecl` adds `bodyless` (no body and no `default`) and
  `default_range` (the `default` keyword). With `default`, `body` is the
  residual body.
- `RenderClause`: whole clause, `name`, `name_range`, `selector`,
  `selector_range`, `pattern`, `bindings`, and `body`. `pattern` has
  `qualifier` and its range (optional), `name` and `name_range`, and the full
  range. Each binding has `field`, `field_range`, and optional `alias` and
  `alias_range`.

## Semantic Model

### Presentation States

- A state is a nominal type, declared only in a schema Document
  (`state-outside-schema` otherwise). It shares the type namespace with
  records, so imports, aliases, `duplicate-record` (now "type"), and
  `reserved-type-name` cover both. Its identity is its Document path and name.
- Variant names must match `[A-Z][A-Za-z0-9]*` (`invalid-variant-name`) and are
  unique per state (`duplicate-variant`). A state with no Variants is
  `empty-state`.
- A payload with no fields (`Loaded []`) is `empty-variant-payload`; write a
  nullary Variant without brackets.
- Payload fields follow record field rules (`invalid-field-name`,
  `duplicate-field`, `generated-field-collision`, types, optional `?`).
- Fields of records and payloads may use state types. A state that reaches
  itself through payload fields, records, collections, or optionals is
  `recursive-state` (records keep `recursive-record`), reported at the closing
  field type.
- A state input or field has no default. A dotted path cannot step into a
  state (`state-field-access`); a state cannot be rendered as text or an
  attribute (`path-type-mismatch`). States pass intact through exact-path
  component arguments, which compare nominally.

### Clause groups

Grouping is resolved per component Document in source order:

- A **Component Signature** is a bodyless declaration or one with `default`.
  The Render Clauses that immediately follow it with the same name form its
  group. Any other top-level declaration ends the group.
- A signature whose group has no clause is `missing-clauses` (both forms).
- A clause outside a group is fatal:
  - `clause-after-component` when the preceding declaration is an ordinary
    bodyful component with the same name;
  - `noncontiguous-clause` when the same name's group appeared earlier in the
    Document;
  - `clause-without-signature` otherwise, including clauses that name an
    imported component.
- A group is one component. A second base declaration of the name is
  `duplicate-component`, as before.
- The selector is the input that the group's first clause matches. An input
  the signature does not declare is `unknown-selector`; a later clause that
  matches another input is `mixed-selector`.
- The selector type must be a Presentation State, `boolean`, or optional
  (`name?: T`, any type) (`invalid-selector-type`). A defaulted boolean is
  matched after default substitution; an omitted optional is `None`.
- A clause group cannot use the `children` outlet in any body
  (`implicit-children-in-clause`). Content inputs are not implemented yet, so
  a group cannot declare children explicitly; this keeps clause groups from
  changing the component API implicitly.

### Patterns

- For a state selector, a Pattern names a Variant of that state. It may be
  qualified with the state's local type name in the Document (its alias when
  imported with one). An unknown Variant is `unknown-variant`; a qualifier that
  does not name the selector's state is `pattern-type-mismatch`.
- For `boolean`, the Patterns are `true` and `false`. For an optional selector,
  they are `Some` and `None`, even when the payload type is a state whose
  Variants include `Some` or `None`. Any other Pattern is
  `pattern-type-mismatch`.
- Patterns bind to symbols: definition, references, and rename of a Variant
  include every Pattern (and qualifier references to the state) that resolves
  to it.

### Bindings and scope

- Bindings name payload fields of the matched Variant. `Some` exposes the one
  built-in field `value`, typed as the selector's type without the option.
  Nullary Variants, booleans, and `None` accept no bindings
  (`unexpected-binding`).
- An unknown field is `unknown-binding`. Binding a field twice, or introducing
  one local name twice, is `duplicate-binding`. A local name equal to a
  signature input is `shadowing-binding`.
- A binding has the field's type and optionality. An optional payload field is
  bound as an optional value and cannot be read until conditions exist.
- Clause and `default` bodies resolve path roots against the signature inputs
  plus their own bindings. Payload fields not named in the header are not in
  scope. The selector keeps its declared type in every body.

### Exhaustiveness and warnings

- The cases are the state's Variants in declaration order, `true`/`false`, or
  `Some`/`None`. A second clause for one case is `overlapping-clause`, reported
  at its Pattern. Source order never affects dispatch.
- A bodyless signature that misses a case is `non-exhaustive-clauses`, at the
  signature name, listing the missing cases.
- With `default`, the residual cases are covered only by the `default` body.
  This is the nonfatal warning `default-fallthrough`, at the `default` keyword,
  listing those cases in case order. A `default` with complete specialized
  coverage is the nonfatal warning `unreachable-default`, at the same range.
- Warnings have severity `warning`. They never make a snapshot invalid. The CLI
  prints them to stderr as `file:line:col: warning [code] message` before
  generation, and the LSP publishes them with severity 2, from the one semantic
  analyzer.

### Minimum URL boundary

The payment proof needs trusted URL boundaries, so this slice lowers exactly
two contextual types. `CSSValue`, `JSONValue`, `TrustedHTML`, and
`TrustedScript` remain `unsupported-contextual-type`.

- `URL` and `TrustedURL` are opaque, nominal, and distinct. KTML has no
  literals, constructors, or casts for them: a literal argument to a URL input
  is `input-type-mismatch`, and a URL is never a `string`.
- A URL value renders only as an exact attribute value (`href="{receipt}"`).
  In text, in a mixed attribute value, or as a string input, it is
  `contextual-type-mismatch`.
- String values in URL attributes keep their existing escaping; requiring URL
  types for `href`/`src`/`action` belongs to the full trust slice.

## Generated Go

### Runtime

Package `ktml` gains the ADR-0003 error contract and the two URL types:

```go
type ErrorKind uint8 // ErrWrite, ErrCanceled, ErrInvalidState, ErrMissingContent, ErrDuplicateKey
type RenderError struct {
	Kind      ErrorKind
	Component string
	Input     string
	Key       string
	Partial   bool
	Err       error
}
type URL struct{ /* unexported */ }        // NewURL(raw string) URL; (URL).String()
type TrustedURL struct{ /* unexported */ } // TrustURL(raw string) TrustedURL; (TrustedURL).String()
```

`rt.InvalidState(component, input)` builds the invalid-state error. `rt.URL`
attribute-escapes a URL after the policy: relative URLs and the `http`,
`https`, `mailto`, and `tel` schemes pass, and anything else renders
`about:invalid#ktml-unsafe-url`. `rt.TrustedURL` attribute-escapes without the
policy and is the audit boundary. `Partial` stays false until item 9 introduces
the byte-tracking `rt.Writer`.

### States

For `state PaymentState { Unpaid, Paid [receipt: URL, paidOn: string] }` in
package `billing`, the generator writes:

```go
// PaymentState is generated from payment.ktmlschema:1:7.
type PaymentState interface {
	Variant() PaymentStateVariant
	isPaymentState()
}

type PaymentStateVariant uint8

const (
	PaymentStateVariantUnpaid PaymentStateVariant = iota + 1
	PaymentStateVariantPaid
)

func (v PaymentStateVariant) String() string

// PaymentStatePaid is the payload of PaymentState.Paid.
type PaymentStatePaid struct {
	Receipt ktml.URL
	PaidOn  string
}

func NewPaymentStateUnpaid() PaymentState
func NewPaymentStatePaid(payload PaymentStatePaid) PaymentState
func AsPaymentStatePaid(state PaymentState) (PaymentStatePaid, bool)
```

- Variant implementations are unexported (`ktmlPaymentStateUnpaid`) and the
  unexported interface method seals the interface to its package. Discriminator
  constants follow declaration order starting at 1, so the zero
  `PaymentStateVariant` is never a Variant.
- A payload Variant's exported payload struct is the only way to name its
  fields; the struct does not implement the state.
- A state-typed input or required field is the interface (`nil` is invalid). An
  optional one is `*S`, following the shared optional rule.
- Each generated exported name (`S`, `SVariant`, `SVariant<V>`, `S<V>`,
  `NewS<V>`, `AsS<V>`) joins the per-directory collision check with records and
  components, and the CLI checks them against handwritten Go.
- Source-map entries cover the state, each Variant (`S.V`), and each payload
  field (`S<V>.Field`).

### Clause dispatch

A clause group generates one component struct `X` (ADR-0003 Hybrid H) from its signature and one renderer that
selects a body before writing anything:

- A state selector checks `nil`, then switches on `Variant()`. Every
  specialized case gets its own branch; residual cases share the `default`
  body; and the Go `default` branch returns `rt.InvalidState`. Payload bindings
  call `As<S><V>` and also return `rt.InvalidState` if it fails.
- A boolean selector branches on the (default-substituted) value. An optional
  selector branches on `nil`, and `Some` binds the dereferenced value.
- Invalid states are therefore a typed `*ktml.RenderError` with
  `Kind == ErrInvalidState`, the component ID, and the selector input, returned
  before the component writes.

## gohtml

The experimental gohtml generator supports none of this slice. Before any
output it fails on state declarations, Render Clauses, bodyless or `default`
signatures, and `URL`/`TrustedURL` inputs.

## Formatting

```ktml
state SearchState {
	Loading,
	Loaded [
		items: [SearchItem],
		total: integer,
	],
	Failed [
		message: string,
	],
}

SearchResults [state: SearchState, query: string]

SearchResults [state = Loading] {
	SearchSkeleton
}
```

- States use one Variant per line with trailing commas, and payloads use the
  record field layout. An empty state is `state Name {}`.
- Signatures keep the input layout and are written without a body, or with
  ` default {` and the residual body. Clauses keep their Pattern spelling
  (qualified or not) and bindings, including aliases, on one header line.
- A group's members are separated by one blank line, like every top-level
  declaration. Formatting is idempotent and parse/format/parse preserves the
  normalized conformance shape.

## Tooling and HMR

- The LSP serves diagnostics (errors and warnings), completion of Variants
  and `true`/`false`/`Some`/`None` after `selector =`, completion of payload
  fields (or `value`) in binding position, completion of bindings as path
  roots, hover, definition, references, rename of states, Variants, and
  payload fields across `.ktmlschema` and `.ktml`, and semantic tokens for
  `state`, `default`, Variants, Patterns, and bindings.
- Clause Pattern or body edits, added or removed clauses, and `default` body
  edits with an unchanged signature and Host Schema are HMR-compatible; they
  are classified as one changed component. Signature changes follow the
  existing input rules. Every Host Schema change rebuilds and reloads.

## Conformance

Manifest cases may declare `states` and `clauses`. Both parsers assert state
names, Variants, payload markers, payload fields, and ranges; clause names,
selectors, Patterns (with qualifiers), bindings (with aliases), and ranges; and
signature `bodyless`/`default` markers in `declarations`.

## Deferred

- Conditions and narrowing (no slice yet assigns them).
- Keyed Repetition (slice 3).
- Content inputs, so clause groups cannot render children yet.
- `CSSValue`, `JSONValue`, `TrustedHTML`, `TrustedScript`, and URL-typed
  enforcement for string values in URL attributes.
- Explorer Variant controls (no Explorer typed-controls work exists yet).
- CLI per-file component generation through `gogen` (item 9), so the CLI
  generates states natively but `.ktml` clause groups only through `gogen`'s
  API while gohtml is the configured generator.
- Generated-Go source-map navigation in the LSP, and handwritten-Go rename.
- Renaming a binding alias in the LSP (prepareRename declines it); while a
  clause is being edited, completion takes its signature from the nearest
  earlier same-name declaration without checking contiguity.
