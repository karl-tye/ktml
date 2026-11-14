# Element Lines, Literal Attributes, Content Inputs, and Conditions v1

- Status: Frozen slice contract (language-gaps slice)
- Date: 2026-10-12
- Parent contract: [Host Schema, Render Clauses, and Keyed Repetition v1](host-schema-render-clauses-keyed-repetition-v1.md)
- Builds on: [ADR-0001](../adr/0001-language-boundary.md),
  [ADR-0002](../adr/0002-typed-render-selection.md),
  [ADR-0003](../adr/0003-rendering-api.md),
  [Canonical Component Input Declarations v1](input-declarations-v1.md),
  [Host Schema Records v1](host-schema-records-v1.md),
  [Presentation States and Render Clauses v1](host-schema-render-clauses-v1.md),
  [Keyed Repetition and Render Identity v1](keyed-repetition-v1.md)

The [payment proof](payment-proof.md) found four gaps that block wider migration
of production templates. The user scheduled them as one slice after Keyed Repetition and
before the item-9 rendering-API prototypes:

1. childless sibling elements merging;
2. literal `"true"` attributes being lowered to bare attributes;
3. content inputs (children and named slots);
4. conditions.

This document freezes their syntax, AST, semantics, lowering, and tooling. It
records the smaller decisions the slice made where the parent contracts were
silent. Explicit attribute forwarding (PLAN "KTML must support") is not placed
in this slice by any contract and is not part of it.

Its interpreter release-gate layer is done: `dev/interp` renders it with
byte, write, and error parity against `gogen`
([ADR-0004](../adr/0004-development-interpreter.md)). The slice stays
unadvertised until `ktml dev` renders live instances through the interpreter
(PLAN Immediate Backlog items 10 and 11).

## 1. Element lines

### Rule

The user decided this rule: **a newline ends an element's attribute list.**

```ktml
div id="a"
div id="b"
```

is two sibling elements. Before this slice the second line parsed as the
attributes `div` and `id="b"` of the first element.

More precisely, an element's header is its name, its attributes, and the
opening token of its child: the `{` of a braced body or a single quoted text
child. The whole header is on one source line. Only a parenthesized
continuation group (below) may span lines.

- A braced body or a text child must start on the header's line. A `{` that
  starts a later line is a syntax error, `detached-element-body`, because a
  newline already ended the element. A quoted string on a later line is a
  sibling text node.
- A line comment ends the line, so `div id="a" // note` is complete.
- The braced body itself spans as many lines as it needs.
- As before, an element's same-line attribute list also ends before `for`
  when `identifier in` follows it ([Keyed Repetition v1](keyed-repetition-v1.md)),
  and before `if` when no `=` follows it. Both then start a sibling.

### Continuation form

A long attribute list is written as one parenthesized group directly after the
element name:

```ktml
form (
	id="phone-card-form"
	action="{cardAction}"
	method="post"
	class="bg-base rounded-xl shadow-xl w-full flex flex-col"
) {
	h2 "Take Payment"
}

button (
	type="button"
	hx-get="{orderLink}"
	hx-target="#global-modal-content"
) "Back"
```

- The `(` is on the element name's line and directly follows the name. It
  replaces the same-line attribute list: `div class="x" (` is a syntax error,
  `mixed-attribute-list`.
- Inside the group, newlines and comments are whitespace, and attributes have
  every same-line form (`name`, `name=value`, `name "value"`, `#id`).
- The child starts on the line of the closing `)`: `) {` or `) "text"`.
- An empty group `div ()` is valid and means no attributes.
- Component calls use the same form for their inputs.

**Why parentheses.** A survey of a large production templ codebase found
155 multi-line tags among 6,764. 76 of them have twelve or more attributes, and
the longest `form` spans 17 lines. Every one
is written as templ/HTML `<form` … `>` with one attribute per line and the
closing `>` on its own line. A delimited group with a closing delimiter keeps
that shape. Parentheses were chosen over the alternatives:

- **`( … )`** is the attribute-list delimiter in Pug, Haml, and Slim, the
  closest indentation-free relatives of KTML element syntax. It is unused in
  element position, and it closes like `>`.
- `[ … ]` already means a typed, comma-separated declaration list
  (`Card [title: string]`, `Card [state = Loaded]`). Element attributes are
  space-separated values, so brackets would suggest commas and declarations.
- A trailing `\` per line is easy to drop by accident, and a missing one
  silently splits the element, which is the bug being fixed.
- Indentation-sensitive continuation would make whitespace significant
  everywhere else in the grammar too, and Tree-sitter would need an
  indentation scanner.

### Formatter

The formatter is canonical for element headers. It writes the same-line form
when the header fits in 100 columns, counting each indentation level as 4
columns and including the trailing ` {`. Otherwise it writes the continuation
form with one attribute per line, indented one level, and `)` at the
element's indentation. As before, it writes every child, a single text child
included, in a braced body, and drops an empty `{}` from an element. Source attribute order is preserved in both forms;
the earlier multi-line formatter sorted attributes, which changed rendered
attribute order, and that sort is removed. Formatting is idempotent.

### AST

`ElementNode` keeps its name, attributes, and children.

- `Multiline` now means the source used the continuation form.
- `attribute_group` is the `(`…`)` range when the form is used.
- The element's `E` and each attribute's `E` are the exact end of their last
  token. They no longer include the following whitespace.

### Migration of existing sources

Sources that relied on merging, such as the `{}` appended to childless
siblings in the payment fixture, no longer need the workaround. The
semantic-model hack that treated an uppercase valueless attribute as a sibling
component call is removed, because siblings are now separate elements.

## 2. Literal attributes

| Source | Output |
| --- | --- |
| `disabled` (valueless) | `disabled` |
| `hx-push-url="true"`, `hx-push-url=true` | `hx-push-url="true"` |
| `aria-hidden="false"`, `x=false` | `aria-hidden="false"` |
| `disabled="{isDisabled}"` with `isDisabled: boolean` | `disabled` when true, nothing when false |
| `label="{text}"` with `text: string` | `label="…"` (escaped) |

- Every literal attribute value is a string and renders verbatim after
  attribute escaping. That includes `"true"`, `"false"`, and the unquoted
  identifiers `true` and `false`, as in HTML, where `hx-push-url=true` is the
  string `"true"`.
- Only a valueless attribute is a static boolean attribute.
- Only an exact boolean path (`"{path}"` whose path is `boolean`) is a dynamic
  boolean attribute. It is present when the value is true and omitted when it
  is false. In an `aria-*` attribute it is the text `true` or `false`
  instead (ADR-0001, 2026-10-13).
- `gogen`, gohtml, and the formatter all keep literal values. Before this
  slice `gogen` and gohtml lowered `"true"` to a bare attribute and `gogen`
  dropped `"false"`.

## 3. Content inputs

### Declarations

```ebnf
input_declaration = identifier, ["?"], ":", component_type,
                    [ "=", scalar_literal ] ;
component_type    = type | "content" ;
```

```ktml
Modal [title: string, children: content, footer?: content] {
	section class="modal" {
		h2 "{title}"
		children
		if footer? {
			footer class="modal-footer" {
				footer
			}
		}
	}
}
```

- `name: content` is a required content input and `name?: content` an
  optional one. `children` is the unlabelled content of a call. Every other
  content input is a named slot.
- Content inputs cannot have defaults (`content-default`), cannot be
  collections (`[content]`, `content-collection`), and cannot be Host Schema
  record or payload field types (`content-field`).
- **Implicit children.** In an ordinary component, a `children` outlet with no
  declaration implicitly declares `children?: content` (ADR-0001). No other
  slot is implicit.
- **Clause groups.** A Component Signature declares children and every named
  slot once, explicitly, for all its Render Clauses and its `default` body.
  An undeclared `children` outlet there is `undeclared-content`. A content
  input cannot be a clause selector (`invalid-selector-type`).

### Outlets

A body node that is a bare identifier naming a content input of the enclosing
component is an **outlet**: it renders that content in place.

- An outlet has no attributes, body, or text (`outlet-with-content`). A
  component whose content input shares an HTML element name, such as
  `header?: content`, therefore cannot also use a `header` element.
- An optional outlet renders nothing when its content is absent. It needs no
  narrowing.
- Outlets are legal in every body position: component and `default` bodies,
  Render Clause bodies, element bodies, repetition bodies, condition
  branches, and slot fills, where they forward the caller's own content.
- Content is not a value: it cannot be interpolated (`content-value-access`),
  compared, or passed as an ordinary attribute.

### Call sites

```ktml
Modal title="Take payment" {
	p "Choose a method."
	slot footer {
		button type="submit" "Pay"
	}
}
```

- The call body's nodes that are not slot fills, in source order, fill
  `children`. A text child (`Button "Save"`) is children too.
- `slot name { … }` fills the named slot. `slot` is a keyword only at the
  start of a body node, when the next token on the same line is an
  identifier with no `=` or value after it. `slot name="x"` is still the HTML
  `<slot>` element, and `span slot="title"` is still an attribute.
- A fill's body is always braced. It may be empty, which supplies empty
  content (present, renders nothing).
- Diagnostics:
  - `slot-outside-call`: a fill that is not a direct body node of a component
    call;
  - `unknown-slot`: the target has no content input of that name;
  - `slot-children`: `slot children { … }` (use the unlabelled call body);
  - `duplicate-slot`: a slot filled more than once;
  - `missing-content`: a required content input with no fill. For
    `children`, an empty or absent call body supplies none;
  - `unexpected-children`: unlabelled content for a component without a
    `children` input;
  - `content-as-attribute`: a content input supplied as an attribute.
- Slot content is lexically scoped at the caller. Its paths resolve against
  the caller's inputs, clause bindings, repetition items, and condition
  narrowings in scope at the fill.

### Lowering

ADR-0003 fixes the Go shape:

- each content input is a props field of type `ktml.Component`, in signature
  order, with the input's Go name (`children` → `Children`), whether required
  or optional;
- required content that is `nil` returns
  `&ktml.RenderError{Kind: ErrMissingContent, Component, Input}` before the
  component writes anything. This preflight runs before clause dispatch, so
  it also precedes `ErrInvalidState`;
- an optional outlet writes nothing for `nil`;
- each KTML fill, including children, lowers to one
  `ktml.ComponentFunc` closure at the call site;
- in the `ktmldev` variant, an outlet inside a repetition renders with
  `dev.WithScope(ctx, identity)`, exactly as a component call does.

The context-carried `ktml.WithChildren`, `ktml.Children`, and
`ktml.ClearChildren` are withdrawn and removed from the runtime package. This
fixes the recursion recorded in ADR-0003: a callee that has its own outlet and
is called without content inside a caller's fill no longer reads the
caller's children from `ctx`. `ktml.Join` combines several Go-built
components into one slot value.

### gohtml

gohtml keeps its prototype `children` outlet. It fails before output on
declared content inputs, named outlets, slot fills, and conditions.

## 4. Conditions

### Syntax

```ebnf
condition_node  = "if", condition, body,
                  [ "else", ( body | condition_node ) ] ;
condition       = or_condition ;
or_condition    = and_condition, { "||", and_condition } ;
and_condition   = unary_condition, { "&&", unary_condition } ;
unary_condition = [ "!" ], ( path | presence | "(", condition, ")" ) ;
presence        = path, "?" ;
path            = identifier, { ".", identifier } ;
```

```ktml
if job.urgent && !job.cancelled {
	span class="badge" "Urgent"
} else if note? {
	p "{note}"
} else {
	p "No note"
}
```

- `if` is a keyword at the start of a body node. `else` is a keyword only
  after an `if` body; any whitespace or comments may come between `}` and
  `else`. An `else` anywhere else is `else-without-if`.
- Conditions are legal in every body position (component, `default`, clause,
  element, repetition, slot fill, and branch bodies). They are not top-level
  declarations.
- Paths are bare dotted identifiers with no whitespace around `.`, as in
  Keyed Repetition headers. `?` directly follows a path.
- `!` applies to one operand. `!!x` is a syntax error; write `!(!x)` if that
  is really meant.
- `&&` binds tighter than `||`. Parentheses group and are preserved.
- Both branches are always braced and may be empty.

### Rejected forms

Each of these is a syntax error with its own code and a message that names
the Go-side alternative:

| Source | Code |
| --- | --- |
| `a == b`, `!=`, `<`, `>`, `<=`, `>=` | `condition-comparison` |
| `if true`, `if "x"`, `if 1` | `condition-literal` |
| `a(b)`, `a.b()` | `condition-call` |
| `a[0]` | `condition-index` |
| `a = b` | `condition-assignment` |
| `a & b`, `a \| b` | `condition-operator` (use `&&` or `\|\|`) |
| `!!a` | `condition-double-negation` |
| `if a {` with no condition, or a dangling operator | `condition-expected` |
| `a ? b : c` and other trailing tokens | `condition-unexpected` |

`true` and `false` are rejected as operands, not resolved as paths.

### Typing

- A path operand must be a present `boolean`: a required or defaulted
  input, a binding, a repetition item, a record field, or an optional one
  that is narrowed. Anything else is `non-boolean-condition` (for example,
  `user.name is string; a condition requires boolean`). The message suggests
  `name?` when the operand is optional or content. Truthiness does not
  exist.
- An optional boolean that is not narrowed is `optional-value-access`, as
  for any other read.
- A presence operand `p?` requires `p` to be optional: an optional input, an
  optional binding, an optional record or payload field, or optional content.
  Otherwise it is `presence-requires-optional`. Every prefix of `p` must be
  readable, so `user.address.city?` requires `user.address` to be present or
  narrowed.

### Narrowing

Presence narrows a path. Inside the region where `p?` is known to hold, `p` has
its value type `T` and can be read, passed to inputs of type `T`, iterated
when `T` is a collection, and used as a prefix (`p.field`).

Narrowing is tracked as two sets of path spellings per condition: `T(c)` holds
when `c` is true, and `F(c)` when `c` is false.

| Condition | `T` | `F` |
| --- | --- | --- |
| `p` (boolean) | none | none |
| `p?` | `{p}` | none |
| `!c` | `F(c)` | `T(c)` |
| `(c)` | `T(c)` | `F(c)` |
| `a && b` | `T(a) ∪ T(b)` | `F(a) ∩ F(b)` |
| `a \|\| b` | `T(a) ∩ T(b)` | `F(a) ∪ F(b)` |

- The `if` body sees `T`. The `else` body, and an `else if` condition and its
  bodies, see `F`.
- Operands short-circuit as in Go: in `a && b`, `b` is typed with `T(a)`, so
  `note? && note.flag` is valid. In `a || b`, `b` is typed with `F(a)`.
- Narrowing is lexical. It reaches nested elements, conditions, repetitions,
  and slot fills inside the branch, and ends with the branch.
- Because shadowing bindings is an error, a spelling identifies one path in
  its scope. `!(p?)` therefore narrows `p` in its `else` branch.
- Clause `Some` bindings remain the other narrowing form. The optional
  collection diagnostic from Keyed Repetition now also suggests `if items? { … }`.

### Lowering

`gogen` writes a Go `if`/`else if`/`else`. Presence is `!= nil`. A boolean
path reads its value, and a narrowed optional reads `*path`. Operators map to
Go's `!`, `&&`, `||`, and parentheses. Narrowed reads anywhere in the branch
dereference (`(*props.Note)`, `(*item.Address).City`). Conditions add no
Render Identity: only keyed fragments do.

## Tooling and HMR

- **CLI:** every new syntax and semantic code is reported as
  `file:line:col [code] message`, and generation writes nothing.
- **LSP:**
  - diagnostics at the exact ranges;
  - completion:
    - slot names after `slot` inside a call body;
    - path roots and fields after `if`, `!`, `&&`, `||`, and `(`, including
      narrowed optionals;
    - `content` in input type position;
  - hover: content inputs (`slot footer?: content`) at outlets and fills,
    condition paths with their narrowed type;
  - definition, references, and rename: from a fill or an outlet to the
    content input declaration, and back;
  - semantic tokens: `if`, `else`, and `slot` as keywords; `!`, `&&`, `||`,
    and `?` as operators; `content` as a type; outlets and fill names as
    parameters.
- **HMR:** a content input is a signature input, so the existing input rules
  apply. An optional content addition is compatible, and a required one is
  not. Removals, renames, and mode changes rebuild. An implicit `children`
  outlet appearing in an ordinary component is an optional addition; removing
  the last one is a removal. Condition and slot-fill body edits, and changes
  to a condition's expression, are compatible patches when the signature is
  unchanged.

## Conformance corpus

The shared corpus gains three normalized fields, asserted by both parsers:

- `elements`: every body element in pre-order with its name, name range,
  depth (enclosing elements only; top-level declarations are not elements),
  whether it used the continuation form, and each attribute's name and whole
  range (`#name` is the attribute `id`). This is what proves sibling
  separation.
- `conditions`: every condition node in pre-order with its range, the `if`
  and `else` keyword ranges, whether the `else` is an `else if`, and the
  normalized condition tree (`path`, `presence`, `not`, `group`, and n-ary
  `and` and `or`, with path parts and ranges). Tree-sitter's binary operator
  nodes are flattened to match.
- `slots`: every slot fill in pre-order with its name, its `slot` keyword
  and name ranges, and its whole range.

Content input declarations are covered by the existing `declarations` field
(type spelling `content`).

## Release gates

| Layer | This slice |
| --- | --- |
| Canonical contract | this document |
| Go parser | element lines, continuation groups, slot fills, conditions, rejected forms |
| Tree-sitter corpus | the same, with an external scanner for line ends |
| Formatter | canonical headers, fills, conditions; idempotent |
| Semantic model | content inputs, outlets, fills, typing, narrowing |
| Interpreter | done: `dev/interp`, with the differential parity harness (ADR-0004) |
| Direct Go generator | content fields, preflight, closures, conditions, goldens |
| gohtml | fails before output |
| CLI, LSP, HMR | as above |
| Explorer | not applicable |
| Source maps | deferred, as for the earlier slices |
