# ADR-0001: KTML Language Boundary

- Status: Accepted; amended by [ADR-0002](0002-typed-render-selection.md), by
  the script-attribute context (2026-10-12, below), by the script data,
  style, and attribute-bag contexts (2026-10-13, below), by the URL
  attribute names and safe prefix (2026-10-13, below), by boolean
  paths as text (2026-10-13, below), by void element content
  (2026-10-13, below), and by `aria-*` booleans (2026-10-13, below)
- Date: 2026-09-06
- Deciders: KTML maintainers
- Scope: Phase 0 language and semantic boundary

## Context

KTML needs one language contract that can be implemented independently by the
compiler parser, Tree-sitter, semantic analysis, development interpreter, and
production generators. The prototype does not provide that contract:

- `spec.md` describes implicit children, overrides, element injection, virtual
  attributes, unions, and colon/indentation shorthand.
- The Go parser accepts a smaller bracketed declaration language and stores
  values as strings in a lossy AST.
- Tree-sitter additionally accepts parenthesized parameters, import aliases,
  arrays, unions, conditions, and unkeyed loops.
- Generators infer references from strings and implement backend-specific
  behavior directly from the syntax AST.

The executable v1 conformance corpus intentionally covers only the concrete
syntax currently shared by both parsers. It is a bootstrap baseline, not the
complete language definition.

## Decision

KTML is a declarative, typed rendering language over values prepared by Go. A
template may select, test presence, iterate, compose, and render those values. Go owns
data access, request and service interaction, mutation, validation, formatting,
sorting/filtering, URL construction, and every operation that can fail or have a
side effect other than writing the render result.

This ADR is normative where it conflicts with the prototype `spec.md`, the core
language context, either parser, or a generator. Those artifacts remain evidence
of current behavior until replaced; they do not enlarge the accepted language.

### Component contract

ADR-0002 supersedes the host-declared nominal type source below. Dedicated
language-neutral Host Schemas are authoritative and generate the nominal Go
records and closed Presentation States accepted by renderers.

A component has a stable identity, a typed input schema, a slot schema, and a
body. The canonical declaration delimiters are brackets and braces:

```ktml
UserCard [user: UserView, compact: boolean = false, heading: string = "Profile", actions?: content] {
  article {
    if !compact { h2 heading }
    p user.name
    children
    actions
  }
}
```

The schema rules are:

- `name: T` is required. Omission is an error.
- `name?: T` is optional. Its semantic type is `Option<T>`; absence is distinct
  from the zero value and must be narrowed by a condition before value access.
- `name: T = literal` is defaulted. Omission substitutes that typed compile-time
  literal before rendering. Defaults cannot reference another input.
- `?` and `=` are mutually exclusive. A default must type-check as `T`.
- Value types are `string`, `boolean`, `number`, `integer`, a Host Schema nominal
  or contextual type, or a collection `[T]`. Go-specific spelling and methods
  are not part of the KTML type system.
- Using a `children` outlet implicitly declares optional `children?: content`.
  It may instead be declared as `children: content` to require content. Without
  an outlet or declaration, the component does not accept children. Render
  Clause groups declare children and every named slot explicitly in their shared
  Component Signature.
- Named slots are declarations of type `content` in the same schema, for example
  `actions?: content`. Content slots cannot have defaults or be supplied as
  ordinary value attributes.

A component instance supplies value inputs by name. Its unlabelled body fills
`children`; `slot name { ... }` fills a declared named slot. A slot may be filled
at most once. Supplying undeclared values or slots, omitting a required value or
slot, or supplying children to a component that does not consume them is a
semantic error. A bare slot name in a component body is a slot outlet. Slot
content is lexically scoped at the caller and is not an untyped value.

### Expressions and control flow

The expression grammar is deliberately closed:

```ebnf
value_expression = literal | path ;
condition   = or_condition ;
or_condition = and_condition, { "||", and_condition } ;
and_condition = unary_condition, { "&&", unary_condition } ;
unary_condition = ["!"], (path | presence | "(", condition, ")") ;
presence    = path, "?" ;
path        = identifier, { ".", identifier } ;
literal     = string_literal | numeric_literal | "true" | "false" ;
```

Paths may resolve only to inputs, loop bindings, their exported data fields, or
slots where a slot outlet is legal. They never resolve to functions or methods.
`path?` is the only presence test and requires an optional input; a successful
branch narrows that input to `T`. An optional value otherwise cannot be read as
`T`. Boolean operators require booleans. Scalar comparison, ordering, implicit
truthiness, and string/number conversions are not supported.

Numeric literals have one lexical form and are interpreted using the expected
type. `integer` rejects fractional/exponent spellings and values outside signed
64-bit; an integer-looking token remains `number` when `number` is expected or
when an output position supplies no unique expected numeric type.

Value expressions are allowed only in value-input assignments, output positions,
collection/key positions, and supported dynamic attributes. Conditions use the
separate boolean/presence grammar. Quoted text may interpolate a path using
`{path}`; it may not hide a general expression.
Calls, method calls, indexing, slicing, arithmetic, assignment, increment,
declarations, mutation, constructors, casts, pipes, arbitrary Go, and immediately
invoked functions are syntax or semantic errors. In particular, a call remains
illegal even if it appears pure or returns a component.

Conditions have `if condition { ... }` and optional `else { ... }` branches.
The condition must be boolean. Prototype declaration overrides remain rejected.
ADR-0002 introduces canonical Render Clauses as finite, exhaustive component
selection over one Host Schema value; they do not inherit override semantics.

ADR-0002 also refines the dynamic iteration and Render Identity rules below,
including integer-only numeric keys, arbitrary fragment bodies, nested identity,
and uniqueness preflight. Dynamic iteration retains exactly this source shape:

```ktml
for user in users key user.id {
  UserCard user=user
}
```

The collection must be `[T]`. The loop introduces one immutable `T` binding. A
key is mandatory for every dynamic loop, not only in development mode, and must
be a deterministic `string` or `integer` path rooted in the loop binding. Keys
must be unique among siblings at runtime; duplicate keys are fatal to that
render. There are no index bindings, unkeyed loops, ranges, filters, or mutation.

### Components and imports

HTML element names and component symbols occupy distinct semantic namespaces.
An uppercase spelling is a convention, not identity. A component reference must
resolve to one local declaration or one explicit import:

```ktml
import { Button, Link as NavLink } from "../ui/navigation.ktml"
```

An import names explicit exported symbols. A `.ktml` Document may import
Components from `.ktml` or Host Schema records/states from `.ktmlschema`; a
schema Document may import only schema symbols. Component and type symbols occupy
distinct namespaces. Wildcard and side-effect imports are illegal. Aliases
affect only the importing document. After configuration aliases and relative
segments are resolved, a component ID is the tuple
`(canonical package ID, exported component name)`. It must not contain an
absolute machine path, an import alias, a generator name, or source position.
Duplicate declarations, unresolved imports, import cycles, inaccessible
components, and local-name collisions are errors. Component instances in the
semantic IR refer to component IDs, never names inferred by a backend.

### Contextual output and trust

Every dynamic output has a semantic output context. Backends must implement the
same context operation rather than choosing escaping from raw syntax:

| Context | Accepted value | Required behavior |
| --- | --- | --- |
| HTML text | scalar or `TrustedHTML` | stringify and HTML-escape scalars; emit `TrustedHTML` verbatim |
| HTML attribute | string/number/integer/boolean | attribute-escape; booleans control presence, except in `aria-*` attributes, where they are the text `true`/`false` |
| Script attribute | `TrustedScript`, `JSONValue`, or boolean | attribute-escape `TrustedScript` and `JSONValue`; write a boolean as the JavaScript literal `true`/`false`; reject every other interpolation |
| URL attribute | `URL` or `TrustedURL` | escape and enforce the URL policy; only `TrustedURL` bypasses policy |
| CSS value (the `style` attribute) | `CSSValue` | emit only through the CSS serializer; reject every other interpolation |
| JSON data | `JSONValue` | serialize as JSON and make embedding safe for its HTML container |
| Script body | `TrustedScript`, `JSONValue`, or boolean, in expression positions of static script | no ordinary string interpolation is permitted; the closing tag can never occur |
| Forwarded attributes | `Attributes` | each entry escaped for the context it was built for |

**Script attributes (amendment, decided by the user on 2026-10-12).** The
browser, Alpine.js, and HTMX evaluate some attribute values as JavaScript.
Attribute escaping does not protect them, because the browser decodes it
before the script runs. Core KTML therefore classifies these names
lexically and fail-closed:

- `on*`, and any name starting with `@` or `:`;
- the Alpine.js expression directives `x-data`, `x-init`, `x-show`,
  `x-text`, `x-html`, `x-model`, `x-modelable`, `x-for`, `x-effect`, `x-if`,
  `x-id`, `x-on`, `x-bind`, `x-trap`, `x-intersect`, `x-anchor`, `x-sort`,
  and `x-mask`, with any `:argument` or `.modifier`;
- the HTMX attributes that run script: `hx-on*`, `hx-vals`, `hx-vars`,
  `hx-headers`, and `hx-trigger`, whose `[filter]` is JavaScript. These
  also count with a `data-` prefix.

The list lives in the core and does not depend on any plugin. Plugins may add
classifications but may never relax these. Static literal values are
unchanged.

An interpolated value must be `TrustedScript` or `JSONValue` built in Go, or a
boolean. The boolean renders as `true`/`false`, which is safe in any
JavaScript position, rather than controlling presence. A string, number,
integer, URL, or other value is the error `script-attribute-value` at the
path, with a hint to build the value in Go. JavaScript built with
`fmt.Sprintf` in templ attributes therefore moves into presenters, as
`ktml.JSON` data or a `ktml.TrustScript` audit boundary.

**JSON constructor (amendment, decided by the user on 2026-10-14).**
`ktml.JSON(v any) (JSONValue, error)` encodes `v` with `encoding/json`, which
escapes `<`, `>`, `&`, U+2028, and U+2029, and returns the encoder's error
wrapped; `ktml.MustJSON` panics on it, like the other `Must*` constructors.
Presenters build JSON data with it. `ktml.TrustJSON` stays the audit boundary
for JSON encoded some other way.

**Script data, style, and attribute bags (amendment, decided by the user on
2026-10-13).** The [migration-language slice](../language/migration-language-v1.md)
adds three contexts and one rule:

- **Script body.** The quoted text of a `script` element is static
  JavaScript, written byte for byte. It may interpolate `JSONValue` (made
  safe for the element), boolean (`true`/`false`), and `TrustedScript`
  paths, as script attributes do. An exact `JSONValue` body is the JSON data
  element. Static text that contains `</script` or `<!--` is an error. At
  runtime a `TrustedScript` body has `</script` and `<!--` rewritten so it
  cannot end its element. Static raw text is written with the
  `` ``` … ``` `` literal, which `style` also accepts.
- **Interpolation position.** In a script body and in a script attribute
  alike, an interpolated path must be in an expression position of the
  static JavaScript around it. It is rejected inside a string or template
  literal (substitutions included), a comment, or a regular expression, and
  after a `/` the lexer cannot classify. Both contexts share this one rule,
  so a value can never land where JavaScript would reinterpret its text.
- **Style.** The `style` attribute is a CSS context. Interpolated values
  must be `CSSValue`, built in Go by the validated `ktml.CSS` constructor or
  the `ktml.TrustCSS` audit boundary.
- **Attribute bags.** `Attributes` values are forwarded with `...path`. A
  bag cannot bypass the script-attribute or style contexts: KTML bag
  literals are typed per entry name at the caller, and the Go constructors
  accept a name only with a value of its context (ADR-0003 amendment of
  2026-10-13).

**URL attributes (amendment, decided by the user on 2026-10-13).** The
URL context is classified lexically and fail-closed, like script
attributes, on every element and case-insensitively: `href`, `src`,
`srcset`, `action`, `formaction`, `cite`, `poster`, `background`, `data`,
`codebase`, `longdesc`, `usemap`, `manifest`, `ping`, `icon`, `xlink:href`,
and the HTMX request attributes `hx-get`, `hx-post`, `hx-put`, `hx-patch`,
and `hx-delete` (also with a `data-` prefix). Script attributes keep their
own context. Static literal values are unchanged.

- One exact interpolated path must be `URL` or `TrustedURL`.
- A path may follow literal text only when that text, before the first
  path, fixes the scheme and host: it starts with `/` followed by a
  character other than `/` or `\`, or with `?` or `#`, or it is an
  absolute `http`, `https`, `mailto`, or `tel` prefix that already reaches
  the path, as in `https://example.com/`. Tab and newline characters,
  which URL parsers remove, are ignored. The paths after it must be
  `string`, `integer`, or `number` and are attribute-escaped, not
  percent-encoded. They can still change the path, query, and fragment,
  but never the scheme or host.
- Every other interpolation is the error `url-attribute-value`, with a hint
  to build the URL in Go as `ktml.URL` (`ktml.NewURL`).

`ktml.Attr` rejects these names; a bag carries them through `URLAttr` or
`TrustedURLAttr`, and a bag literal's static or safe-prefix text is
template source, as for an element.

**`srcdoc` (amendment, Phase 8 security review, 2026-10-14).** `srcdoc`
holds an HTML document that the browser parses and runs in the page's
origin, so attribute escaping is no protection: the browser decodes the
entities first. KTML has no trusted HTML value, so data never reaches it.
Any path in a `srcdoc` value (on every element, case-insensitively) is the
error `html-attribute-value`; literal text is unchanged. `ktml.Attr`,
`URLAttr`, `TrustedURLAttr`, and `JSONAttr` reject the name, and only
`BoolAttr` accepts it.

**Boolean paths as text (amendment, decided by the user on 2026-10-13).**
KTML never stringifies a boolean. A `boolean` path is the error
`boolean-text` at the path when it is written as text: in HTML text, exact
or among literal text (`p "{on}"`, `p "open: {on}"`); among literal text in
an attribute value (`title="x {on}"`), including a conditional attribute or
a bag entry; and in a component call argument other than an exact path to a
`boolean` input (`X label="n {on}"`). The hint is to choose the text with
`if path { … } else { … }`, or to build the string in the presenter. The
generator and the interpreter already rejected these programs; the compiler
now reports them, so the editor, `ktml generate`, and HMR show one
diagnostic. The defined boolean meanings are unchanged:

- an exact attribute value (`disabled="{busy}"`) controls presence: the
  attribute is written bare when true and omitted when false (an `aria-*`
  attribute writes `true`/`false` instead; see the amendment below);
- in a script attribute or a script body it is the JavaScript literal
  `true`/`false`;
- an exact path to a `boolean` input passes the value.

An `aria-*` state that must read `"true"`/`"false"` was therefore written
as `if open { aria-expanded="true" } else { aria-expanded="false" }`; the
`aria-*` boolean amendment below makes `aria-expanded="{open}"` mean that.

**Void element content (amendment, decided by the user on 2026-10-13).**
A void element (`area`, `base`, `br`, `col`, `embed`, `hr`, `img`, `input`,
`link`, `meta`, `param`, `source`, `track`, `wbr`) has a start tag only, so
content in one cannot be rendered where it is written. Any child of a void
element, including a comment, a condition, or a repetition, is the compile
error `void-content` at the element name. The generator and the
interpreter already rejected these programs; the compiler now reports them,
so the editor, `ktml generate`, and HMR fail early with one ranged
diagnostic. The HTML analyzer does not repeat it.

**`aria-*` booleans (amendment, decided by the user on 2026-10-13).** ARIA
states are the text `true` or `false`; a bare `aria-expanded` is not
`true`. An exact `boolean` path in an attribute whose name starts with
`aria-` (matched case-insensitively) therefore renders as the text:
`aria-expanded="{open}"` writes `aria-expanded="true"` or
`aria-expanded="false"`, and is always written. This holds on elements, in
conditional attributes, and in attribute bag entries; the interpreter
matches gogen. Every other attribute keeps presence (`disabled="{busy}"`),
a script attribute keeps the JavaScript literal (`:aria-expanded="{open}"`
is an Alpine binding), a valueless `aria-*` attribute stays bare, and a
literal value stays a string. `ktml.BoolAttr` in Go keeps presence for
every name, as templ's `templ.Attributes` booleans do. The templ converter
writes templ's `aria-x?={ cond }`, which is presence, as
`if cond { aria-x }`, so converted output renders what templ rendered.

`URL`, `CSSValue`, `JSONValue`, `Attributes`, and all `Trusted*` values are opaque nominal
types created in Go or generated boundary code. KTML has no constructors, casts,
or raw-output operator, so a template cannot elevate an untrusted string.
`TrustedHTML`, `TrustedURL`, and `TrustedScript` are explicit audit boundaries.
Context is derived from the resolved output position; placing a value in an
incompatible context is an error. HTML is the only accepted output vocabulary
for this phase; XML remains outside the language.

### Syntax, semantic IR, and backends

Source handling has three non-interchangeable representations:

1. A lossless concrete syntax tree retains every source byte and trivia token.
2. A syntax AST represents every construct and carries zero-based, half-open
   byte ranges for the construct, identifiers, operators, and values.
3. Typed semantic IR removes trivia, resolves symbols and paths, records input
   modes and types, assigns output contexts, and represents components,
   elements, slot outlets/fills, conditions, and keyed loops explicitly.

Formatting and editor rewrites operate from lossless syntax. Name resolution,
type checking, interpretation, and generation operate from semantic IR. Source
ranges survive lowering for diagnostics and source maps, but syntax-only details
such as colon chains or quote choice never affect semantics.

Semantic IR is backend-independent. It cannot contain Go expressions,
`html/template` actions, interpreter closures, generated identifiers, DOM/HMR
markers, or pre-escaped backend strings. The interpreter and every production
backend must consume the same valid semantic model and produce equivalent
contextual output. A backend may reject an unsupported target, but may not add a
language feature or reinterpret a valid program.

### Parser conformance and fatal diagnostics

The versioned language contract consists of this boundary, a lexical/concrete
grammar, a normalized syntax/semantic schema, and one valid/invalid fixture
manifest. For every accepted construct, the Go parser and Tree-sitter must:

- agree on validity from the same source fixture;
- expose equivalent named constructs and zero-based half-open byte ranges after
  normalization; and
- preserve enough concrete syntax for byte-identical round trip when no edit is
  requested.

For invalid fixtures both parsers must reject the source and report normalized
error spans. Parser-specific recovery trees may differ and may be tested
separately, but recovered nodes cannot enter semantic analysis as valid input.
The existing conformance schema v1 remains the bootstrap acceptance/recovery
contract. It must be versioned and expanded to normalized shape and semantics as
the canonical grammar and IR are implemented; Tree-sitter-only syntax is not
accepted merely because Tree-sitter parses it.

Any error-severity lexical, parse, import, resolution, type, trust-context,
key-uniqueness, or backend validation diagnostic is fatal to that compilation or
render. No generator/plugin invocation, cache publication, output/state write,
or HMR patch may result from an invalid snapshot. Development tooling may retain
and serve the last valid artifact while reporting diagnostics. Warnings cannot
silently discard or reinterpret source.

### Staged prototype compatibility

Compatibility is source migration, not permanent dual semantics:

| Prototype behavior | Accepted contract and staging |
| --- | --- |
| Bracketed declarations, braced bodies, scalar defaults, implicit `children`, and `{name}` interpolation | Retained; interpolation grows to field paths. |
| Values stored as raw strings and references inferred by generators | Transitional only; replace with expression syntax nodes and typed semantic values before adding features. |
| Tree-sitter import aliases | Accepted syntax, but unsupported until both parsers and resolution conform. |
| Parenthesized parameter lists | Not accepted; diagnose and offer a bracket rewrite. |
| Tree-sitter unkeyed `for item in items` | Not accepted; diagnose a missing `key` clause. |
| Tree-sitter conditions/comparisons | Boolean/presence condition syntax is retained. Scalar comparison and ordering are superseded by ADR-0002 and must be diagnosed. |
| Declaration overrides such as `Button [disabled="true"]` | Superseded by explicit `if`; migration tooling may rewrite simple cases, otherwise diagnose. |
| Element injection, virtual attributes, element-valued inputs, unions, postfix `T[]` types, colon/indentation chains, and `+=` attribute modifiers | Deferred. They must not be silently accepted by one parser/backend; each needs a later ADR or grammar amendment. |
| Uppercase/attribute-position heuristics for component calls | Superseded by symbol resolution and explicit slot content. |
| `html/template` actions, browser-visible prop caches, or generated-Go behavior inferred directly from the syntax AST | Non-conforming implementation detail to be removed in later slices. |

Until a construct reaches both parsers and semantic analysis, existing behavior
may remain behind prototype code paths for repository compatibility, but the CLI
must not advertise it as accepted language. A compatibility path must either
lower to this contract without ambiguity or emit a source-ranged diagnostic; it
must never select semantics by backend.

## Rationale

A small closed expression language keeps business behavior testable in Go and
makes static typing, deterministic generation, safe escaping, LSP support, and
interpreter/generator parity tractable. Explicit input modes and keyed loops make
component API and HMR compatibility structural rather than heuristic. Opaque
context values put trust decisions at reviewable Go boundaries. Separating
lossless syntax from semantic IR lets parsers and formatters preserve source
without leaking syntax accidents into rendering backends.

## Consequences

- View models will contain more presentation-ready fields and contextual value
  wrappers than the prototype requires.
- Some concise syntax in `spec.md` is no longer accepted, and migration tooling
  must report or rewrite it.
- The current AST, both parsers, workspace model, and generators are incomplete
  implementations of the accepted contract.
- Required/optional/defaulted changes, slots, and keys become explicit semantic
  data suitable for API compatibility and HMR classification.
- A future backend can be added without changing template semantics or trust
  rules.
- Language additions require a contract/schema version and shared conformance
  fixtures before a parser or backend may expose them.

## Rejected alternatives

- **Embed Go expressions or pure helper calls.** Purity and side effects cannot
  be enforced across arbitrary Go calls, and interpreter parity would require a
  Go evaluator.
- **Permit calls from an allowlist.** This creates backend/plugin-specific
  semantics and moves presentation preparation back into templates.
- **Use one stringly AST for formatting and generation.** It loses trivia and
  source precision while forcing every consumer to repeat parsing and typing.
- **Make the generated Go representation canonical.** That couples semantics to
  one public rendering API before its separate prototype ADR is decided.
- **Treat Tree-sitter as the only parser.** Compiler acceptance and editor
  recovery have different operational needs; conformance is the contract.
- **Require keys only for HMR.** Production and development would then assign
  different identities and could render/reconcile different programs.
- **Allow raw strings with escaping flags.** Boolean flags are easy to lose or
  misuse; opaque contextual types preserve the trust boundary through lowering.
- **Keep declaration overrides as control flow.** Two condition systems make
  typing, precedence, migration, and tooling unnecessarily ambiguous.

## Follow-up order

1. Version and freeze the lexical/concrete grammar plus normalized syntax and
   semantic schemas described here; add valid and invalid fixtures for every
   expression and schema rule.
2. Introduce lossless syntax and complete source ranges in the Go parser, then
   lower the current shared subset into typed semantic IR.
3. Align Tree-sitter named nodes and normalized ranges with the same fixtures;
   reject its extra prototype syntax until each feature is shared.
4. Implement component/import identity and typed required, optional, defaulted,
   and slot checking.
5. Add Host Schema records and typed field paths, then closed states and Render
   Clauses, then Keyed Repetition, extending conformance before each feature is
   enabled as specified by ADR-0002.
6. Move the interpreter and generators from syntax AST/string inference to the
   semantic IR and add cross-backend output/trust parity fixtures.
7. Add source-ranged compatibility diagnostics and migration rewrites for the
   superseded prototype syntax.

The rendering API prototype remains a separate Phase 0 decision and is not
chosen by this ADR.
