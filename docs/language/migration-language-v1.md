# Migration-Unblocking Language Features v1

- Status: Frozen slice contract (migration-language slice)
- Date: 2026-10-13
- Parent contracts:
  [Element Lines, Literal Attributes, Content Inputs, and Conditions v1](language-gaps-v1.md),
  [Framework Attribute Names, Doctype, and HTML Comments v1](attribute-names-doctype-comments-v1.md)
- Builds on: [ADR-0001](../adr/0001-language-boundary.md) (amended
  2026-10-13), [ADR-0003](../adr/0003-rendering-api.md) (amended
  2026-10-13), [ADR-0004](../adr/0004-development-interpreter.md)

A templ inventory of a production application (2026-10-12) found
140 blocked nodes in 29 files, and only 23 of 82 HTML files
dependency-ready. Its priority list is this slice:

1. raw-text `script`/`style` bodies, after making the existing defect fail
   closed;
2. conditional valued attributes;
3. explicit attribute forwarding;
4. `CSSValue` in `style` attributes;
5. the script data context;
6. `*` in attribute names.

`templ.NewOnceHandle` is not a language feature (see
[Conversion rules](#conversion-rules)).

After the slice, the inventory has no blocked node, and all 82 HTML files
are dependency-ready.

## 0. Fail-closed raw text

Text inside `script` and `style` used to be HTML-escaped:
`script "if (a && b) {}"` rendered `if (a &amp;&amp; b) {}`. That is
silent wrong output. Every child of a raw-text element is now checked:

- The raw-text elements are `script`, `style`, `xmp`, `iframe`, `noembed`,
  `noframes`, and `plaintext`, matched case-insensitively.
- Only `script` and `style` accept content, of the forms below. Any other
  child is `raw-text-body` at the child's range. That covers an element,
  condition, outlet, comment, or text in `style`, and any child of the other
  raw-text elements.
- gogen's no-snapshot path, the interpreter, and gohtml fail before output
  as well.

## 1. Raw-text literals

```ebnf
raw_text = "```", { any character, so that "```" does not occur }, "```" ;
```

```ktml
script ```
	document.addEventListener('alpine:init', () => {
		console.log(`${label} <ready>`)
	})
	```
style {
	```.a > .b { content: "&"; }```
}
```

- **Syntax.** A raw-text literal is a body node, and also an element's
  same-line child, like a text child. It may span lines, and it ends at the
  first `` ``` ``. A missing closing fence is the syntax error
  `unterminated-raw-text` at the opening fence. A raw-text literal is never
  an attribute value: Tree-sitter's line scanner treats `` ``` `` after a
  valueless attribute name as the child.
- **Placement.** A raw-text literal is valid only as a direct child of
  `script` or `style`. Elsewhere it is `raw-text-placement`.
- **Closing-tag rule.** A literal that contains its element's end tag
  (`</script` or `</style`, case-insensitively) is `raw-text-end-tag`. So
  is a script literal that contains `<!--`, which starts the HTML "escaped"
  state, in which `</script>` no longer ends the element.
- **Output.** The text between the fences is written byte for byte, with no
  escaping. The source's indentation is part of the text.
- **Limitation.** A raw-text literal cannot contain three backticks in a row.
  Such a script moves to a static file.

## 2. Conditional attribute groups

Inside a parenthesized attribute list (the continuation group, which may
stay on one line):

```ebnf
attribute_condition = "if", condition, attribute_block,
                      [ "else", ( attribute_block | attribute_condition ) ] ;
attribute_block     = "{", { group_entry }, "}" ;
group_entry         = attribute | attribute_condition | spread ;
```

```ktml
option (value="6" if six { selected }) "6 months"
a (
	href="{href}"
	if current { aria-current="page" class="on" } else { class="off" }
	if form? { form="{form}" } else if oob { hx-swap-oob="true" }
) "{label}"
```

- **Syntax.**
  - The condition grammar is the closed boolean/presence grammar of
    language-gaps-v1, with the same rejected forms
    (`condition-comparison`, …).
  - Branches hold attributes of every form, nested groups, and spreads.
  - As in a body, `if =` and `if:x` stay attribute names.
  - On a header line, `if` still starts a sibling condition, so conditional
    attributes need the parenthesized list.
- **Typing.** A branch sees the paths its condition narrows, so
  `if form? { form="{form}" }` is valid and narrowing ends with the branch.
  Values keep their contexts: script attributes, `style`, and URLs.
- **Collisions** (`duplicate-attribute`). Names compare
  case-insensitively.
  - A conditional attribute may share its name only with the other branches
    of its own group.
  - It is an error when the same name is also unconditional, appears in
    another group, or appears twice in one branch.
  - Duplicates among unconditional attributes keep their earlier behaviour.
- **Component calls.** A call's attributes are inputs, so a conditional
  group there is `conditional-call-input`.
- **Lowering.**
  - gogen writes a Go `if`/`else` around the branch's static and dynamic
    writes.
  - The interpreter compiles an `ifOp`.
  - Neither adds Render Identity.

## 3. Attribute forwarding

### Language

```ktml
Field [label: string, attrs?: Attributes] {
	input (type="text" class="field" if attrs? { ...attrs })
}

Form [busy: boolean, extra: Attributes] {
	Field (
		label="Name"
		attrs=(required hx-post="/x" if busy { disabled } ...extra)
	)
}
```

- **Type.** `Attributes` is a contextual type, Go `ktml.Attributes`. It is
  valid as an input or record field type.
  - It renders only through a spread. In text or an attribute value it is
    `contextual-type-mismatch`.
  - An optional bag must be narrowed before it is spread, as any optional
    value must (ADR-0001), so write `if attrs? { ...attrs }`.
- **Spread.** `...path` is an attribute-list entry on a header line (after
  whitespace), in a parenthesized list, or in a conditional branch. It has no
  space inside: `... attrs` is `invalid-spread`.
  - The path must be a present `Attributes` (`spread-type-mismatch`).
  - A component call takes no spread (`spread-call-input`); pass the bag to
    an `Attributes` input instead.
- **Bag literal.** `name=( entries )` supplies an `Attributes` input of a
  component call.
  - Entries are the group entries above, and newlines inside are whitespace.
  - Each entry is typed in the caller's scope, in the context its name
    requires.
  - A bag literal anywhere else is `attribute-bag-placement`.
  - A script or `style` entry takes a literal or one whole value. Mixed text
    there is `attribute-bag-value`.
- **Collision rules.** These are deterministic, and names compare
  case-insensitively:
  1. An element owns every name it writes itself, in any form and any
     branch. A forwarded entry with an owned name is dropped. That matches
     what browsers did with templ's output, where the spread came last and
     the parser kept the first attribute.
  2. An element writes at most one spread (`multiple-spreads`). Spreads in
     exclusive branches of one group are allowed. Join several bags in Go
     with `ktml.JoinAttributes`.
  3. A bag literal names each attribute once, unconditional ones included,
     except in exclusive branches (`duplicate-attribute`).
  4. `ktml.JoinAttributes` keeps the first entry of a repeated name.
- **Output.** Forwarded entries are written at the spread's position, in
  bag order.

### How bags carry context

The script-attribute and `style` contexts cannot be bypassed by
forwarding. Each bag entry records the context its value was checked for
when the bag was built, and is written through that context's escaper
(`rt.Attributes`):

| Built by | Context check |
| --- | --- |
| A KTML bag literal | the compiler types each entry's value for its name, exactly as on an element |
| Go: `ktml.Attr(name, string)` | plain attributes only |
| Go: `ktml.BoolAttr(name, present)` | every name (no value) |
| Go: `ktml.URLAttr`, `ktml.TrustedURLAttr` | plain attributes only; `URL` keeps the URL policy |
| Go: `ktml.JSONAttr` | plain and script attributes |
| Go: `ktml.ScriptAttr(name, TrustedScript)`, `ktml.ScriptBoolAttr` | script attributes only |
| Go: `ktml.CSSAttr(name, CSSValue)` | `style` only |

- Every constructor also checks the name against the attribute-name rule.
- A violation returns an `*ktml.AttributeError`. It never panics on data.
- The `Must…` variants panic, and are for constant arguments. Generated code
  uses them only for names and contexts the compiler has already verified.

### Lowering

- **gogen.**
  - A spread is `rt.Attributes(w, bag, owned)`, where `owned` is a package
    variable holding the element's lower-cased names.
  - A bag literal builds a `ktml.Attributes` at the call site with the
    `Must` constructors and `ktml.JoinAttributes`.
  - A conditional bag entry is a Go `if`/`else` that assigns a local, so a
    branch never reads an optional its condition did not prove present.
- **Interpreter.** It uses the same constructors and `rt.Attributes`, and
  builds conditional entries lazily too.

## 4. `CSSValue` in `style`

- **The style context.** The `style` attribute is now a fail-closed CSS
  context, like the script attributes.
  - An interpolated path there, exact or among literal text, must be
    `CSSValue`. It is written through `rt.CSS`, which attribute-escapes it.
  - Any other type is `style-attribute-value`.
  - Literal style values are unchanged.
- **Elsewhere.** A `CSSValue` anywhere else is `contextual-type-mismatch`
  (`script-attribute-value` in a script attribute).
- **Go constructor.** `ktml.CSS(ktml.CSSDecl(property, value), …)` builds a
  `CSSValue` and returns a `*ktml.CSSError` for a declaration outside its
  closed grammar:
  - **Properties.** A property name, an optional leading `-` (vendor), or a
    `--custom` property.
  - **Value characters.** Letters, digits, spaces, and `. , % # ( ) + - * / _ !`.
  - **Functions.** Only `calc`, `min`, `max`, `clamp`, `var`, `rgb`, `rgba`,
    `hsl`, `hsla`, `translate`, `translateX`, `translateY`, `scale`,
    `scaleX`, `scaleY`, and `rotate`, plus the grid track functions below.
    `url()` and `expression()` are therefore impossible.
  - **Grid track functions.** `repeat()`, `minmax()`, and `fit-content()`
    have a closed argument grammar of their own:
    - a `repeat()` count is a positive integer, `auto-fill`, or `auto-fit`,
      and its track list is one or more space-separated tracks;
    - a track is a length, a percentage, an `fr` value, `0`, `auto`,
      `min-content`, `max-content`, `minmax(a, b)` of those, or
      `fit-content(<length-percentage>)`;
    - `minmax()` and `fit-content()` may also stand alone in a value;
    - nothing else nests inside them (no `var()`, `calc()`, or nested
      `repeat()`), and their arguments allow only spaces and tabs as
      whitespace, so `repeat(auto-fill, minmax(12rem, 1fr))` is accepted
      and `repeat(3, url(x))` is a `*ktml.CSSError`.
  - `MustCSS` is for constants. `ktml.TrustCSS` remains the audit boundary
    for anything else.

## 5. The script data context

The quoted text of a `script` element is JavaScript:

```ktml
script type="application/json" id="chart-data" "{data}"
script "createChart({id}, {data});"
script "window.debug = {debug}; {boot}"
```

- **Static text.** Literal text is written byte for byte.
- **Paths.** They may be:
  - `JSONValue`, written through `rt.JSONScript`, which escapes `<`, `>`,
    `&`, U+2028, and U+2029, so it is valid JSON and valid JavaScript;
  - `boolean`, written as `true` or `false`;
  - `TrustedScript`, written through `rt.Script`.

  Any other type is `script-body-value`.
- **The JSON data element.** An exact `JSONValue` body is the JSON data
  element (`templ.JSONScript`).
- **`style` bodies** still accept only raw-text literals.

### Interpolation positions (ADR-0001 amendment of 2026-10-13)

A lexer reads the static JavaScript around each path of a script body or a
script attribute. A path must be in an expression position, such as a call
argument, an array element, or the right side of an assignment. These are
`script-interpolation-position` at the path:

- a path inside a string literal (`'…'`, `"…"`);
- a path inside a template literal, including any `${ … }` substitution
  inside it;
- a path inside a line or block comment;
- a path inside a regular expression literal;
- any path after a `/` the lexer cannot classify as a regular expression or
  a division. That is a `/` after `)` or `}`, where it fails closed.

### End tags

- **Static text.** It cannot contain `</script` or `<!--`
  (`raw-text-end-tag`).
- **A `TrustedScript` path** cannot directly touch `<` before it, or `/`,
  `!`, or `-` after it (`script-body-adjacency`), so it cannot complete
  either sequence with the static text.
- **Runtime.** `rt.Script` rewrites `</script` as `<\/script` and `<!--` as
  `<\!--`, case-insensitively. Valid JavaScript contains them only in
  strings, regular expressions, and comments, where the rewrite means the
  same. ADR-0003's error kinds are unchanged.

### KTML quoting limits

Quoted text keeps the interpolation grammar of quoted-interpolation-v1:

- `{name}` interpolates, and `{ ` followed by a space or tab starts literal
  braces up to the next `}`;
- so a template-literal substitution is written `${ name }`;
- and a path cannot appear inside a JavaScript block on the same line,
  because `{ go({x}) }` is not a valid quoted value.

Static scripts with braces belong in raw-text literals. Data goes in through
`JSONValue` paths in expression positions, built in Go with `ktml.JSON`.

## 6. `*` in attribute names

```ebnf
name_char = letter | digit | "_" | "-" | ":" | "." | "*" ;
```

- htmx `response-targets` names such as `hx-target-4*` and `hx-target-*`
  parse in both parsers and render verbatim.
- `*` never starts a name, and maximal munch still applies.
- The script-attribute classification now also matches:
  - the htmx 2 dash forms of `hx-on` (`hx-on-click`, `hx-on--after-request`,
    and their `data-` forms), which it used to miss;
  - names whose base is followed by `*` (`hx-trigger*`).

The rules live in `internal/attrname`, shared by the compiler and the
runtime constructors.

## Conversion rules

PLAN's conversion policy records these:

| templ | KTML |
| --- | --- |
| `<script>`/`<style>` with a static body | a raw-text literal (review if the body contains three backticks) |
| `<script>` with Go values | static JavaScript with presenter-built `JSONValue` paths (`ktml.MustJSON`) in expression positions |
| `if c { name="v" }` attributes | a conditional attribute group; a derived condition becomes a presenter boolean |
| `templ.Attributes` input, `{ attrs... }` | an `Attributes` input forwarded with `...attrs`; Go-built bags use the `ktml.*Attr` constructors |
| `style={ fmt.Sprintf(…) }`, `templ.KV("display: none", c)` | a presenter-built `CSSValue` (`ktml.CSS`), or a conditional literal `style` |
| `templ.SafeCSS` | `ktml.TrustCSS`, a review boundary |
| `templ.JSONScript(id, v)` | `script type="application/json" id="…" "{v}"` with a presenter-built `JSONValue` |
| `templ.JSFuncCall(f, literals…)` | a static script, ``script ```f("a", 1)``` `` |
| `templ.JSFuncCall(f, values…)` | `script "f({a}, {b})"` with presenter-built `JSONValue` arguments (`ktml.MustJSON`) |
| `templ.JSONString(v)` in a script attribute | a presenter-built `JSONValue` (`ktml.MustJSON`) |
| `templ.JSUnsafeFuncCall` | review: a presenter-built `TrustedScript` |
| `templ.NewOnceHandle().Once()` | not a language feature: move the guarded script into the static bundle |

## Tooling and HMR

- **CLI.** Every new code is reported as `file:line:col [code] message`, and
  generation writes nothing.
- **LSP.**
  - Diagnostics at exact ranges.
  - Semantic tokens: `if`/`else` of attribute groups are keywords, and
    their condition paths and the `...` operator are tokenized.
  - Tree-sitter highlights `raw_text`, `spread_attribute`, and
    `attribute_bag`, and injects JavaScript and CSS into the raw text of
    `script` and `style`.
- **HMR.** A raw-text, conditional-group, spread, or bag edit is a body
  change, compared structurally. When the signature is unchanged it is a
  compatible patch. Adding an `Attributes` input follows the input rules.

## Conformance corpus

- `markup` gains the kind `raw-text`.
- `attributeConditions` lists every conditional group with its keyword
  ranges, condition tree, and branch attributes.
- `forwarding` lists every spread and bag literal.
- The new valid fixtures are `raw-text`, `conditional-attributes`,
  `attribute-forwarding`, and `star-attributes`.
- The new invalid fixtures are `unterminated-raw-text`,
  `conditional-attribute-comparison`, and `spaced-spread`.

## Release gates

| Layer | This slice |
| --- | --- |
| Canonical contract | this document; ADR-0001 and ADR-0003 amendments of 2026-10-13 |
| Go parser | raw-text literals, conditional groups (with group-level recovery), spreads, bag literals, `*` names |
| Tree-sitter corpus | `raw_text`, `attribute_condition`, `attribute_block`, `spread_attribute`, `attribute_bag`, `*` names; conformance fields above |
| Formatter | raw text byte for byte; groups, spreads, and bags canonical; idempotent |
| Semantic model | every code above; `Attributes` and `CSSValue` lowered; script positions |
| Interpreter | all features, with byte, write, and error parity in the differential harness |
| Direct Go generator | all features, with adversarial render tests |
| gohtml | fails before output on every new construct, and on interpolated `style` |
| CLI, LSP, HMR | as above |
| Explorer | not applicable |
| Source maps | deferred, as for the earlier slices |
