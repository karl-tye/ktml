# Framework Attribute Names, Doctype, and HTML Comments v1

- Status: Frozen slice contract (attribute-names slice)
- Date: 2026-10-12
- Parent contract: [Element Lines, Literal Attributes, Content Inputs, and Conditions v1](language-gaps-v1.md)
- Builds on: [ADR-0001](../adr/0001-language-boundary.md),
  [ADR-0003](../adr/0003-rendering-api.md)

Prototyping a production templ application found two gaps that almost every
template hits:

- attribute names containing `:`, `@`, or `.`;
- the doctype.

It hand-lowered both. It also dropped an HTML comment, because KTML had no
output comments. PLAN "Supported language model" lists HTML doctypes and
comments. This slice adds all three.

Its interpreter release-gate layer is done: `dev/interp` renders it with
byte, write, and error parity against `gogen`
([ADR-0004](../adr/0004-development-interpreter.md)). The slice stays
unadvertised until `ktml dev` renders live instances through the interpreter
(PLAN Immediate Backlog items 10 and 11).

## 1. Attribute names

### Survey

A survey of a production templ application (82 HTML `.templ` files) found
these forms:

| Form | Examples |
| --- | --- |
| Alpine shorthand `@event`, with modifiers | `@click` (40), `@change`, `@click.prevent`, `@keydown.slash.window`, `@htmx:after:request.window`, `@dragover.prevent` (valueless) |
| Alpine shorthand `:attr` | `:disabled` (39), `:key`, `:class`, `:aria-current` |
| Alpine long forms | `x-bind:value`, `x-on:click`, `x-model.number`, `x-transition:enter-start`, `x-transition.opacity.duration.100ms` |
| HTMX | `hx-on::after-request`, `hx-on:htmx:before:swap`, `hx-ext:inherited`, `hx-indicator:append`, `hx-sse:connect` |
| Namespaced XML-style | `xmlns:swipe`, `safe-area:mode` (XML templates such as Hyperview only, out of scope) |

Every name starts with a letter, or with one `@` or `:` followed by a letter.
After that come letters, digits, `-`, `_`, `:`, and `.` in any order,
including `::` and digits after a dot (`duration.100ms`).

### Lexical rule

```ebnf
attribute_name = [ "@" | ":" ], name_start, { name_char } ;
name_start     = letter | "_" ;
name_char      = letter | digit | "_" | "-" | ":" | "." ;
letter         = "a" … "z" | "A" … "Z" ;
```

- **Position.** The rule applies only to attribute names: in a same-line
  attribute list and inside a `( … )` continuation group. All other names keep
  the identifier rule. That includes element names, component names, input
  names, `#id` names, paths, slot names, and keywords. A component call's
  attributes are its inputs, so a framework name there is `unknown-input`.
- **Maximal munch.** A name extends through every adjacent `name_char`. So
  `x-on:click.outside="open = false"` is one name, and `hx-on::after-request`
  is one name.
- **Separation.** On a header line, a name that starts with `@` or `:` must be
  preceded by whitespace. Without that, it would read as the prototype colon
  chain (`a:href="/x"`, `Profile:user=…`), which ADR-0001 defers. So
  `a:href="/x"` remains a syntax error (`colon-chain`) in both parsers.
  Inside a continuation group, the `(` and the other attributes delimit
  names, so `div (:class="c")` is valid.
- **Malformed names.** A lone `@` or `:`, or one followed by a character that
  cannot start a name (`::x`, `@1`), is a syntax error
  (`invalid-attribute-name`).
- **Keywords are unaffected.** An `if`, `for`, or `slot` that is directly
  followed by `:` or `.` is part of an attribute name, not a keyword. A
  newline still ends the attribute list. The name grammar contains no
  whitespace, quote, `=`, `(`, `)`, `{`, `}`, or `#`, so it cannot overlap
  the attribute value forms, the continuation group, or a body.

The names are **plain lexical forms**. KTML gives `@`, `:`, and `.` no meaning
of its own. Library Plugins cannot add syntax (PLAN Product Decisions), and
none is needed here: the HTMX and Alpine.js integrations only recognize names
that the core grammar already accepts.

### Semantics and output

- An attribute name is a static string. It is rendered verbatim, with its
  case preserved. Since `name_char` excludes everything HTML treats specially
  in a name, a name never needs escaping.
- Values follow the existing rules unchanged. A valueless name is a static
  boolean attribute (`@dragover.prevent`). Literal values render after
  attribute escaping. An exact boolean path controls presence (in `aria-*`
  attributes it is the text `true`/`false`, ADR-0001), and other
  paths render through the attribute escapers.

### Script attributes

The user decided on 2026-10-12 that core KTML treats JavaScript-evaluated
attributes as a script context, and amended [ADR-0001](../adr/0001-language-boundary.md#contextual-output-and-trust)
to match. The classification is lexical and case-insensitive. It lives in
`compiler.ScriptAttribute`, and plugins cannot relax it:

| Class | Names |
| --- | --- |
| HTML event handlers | every name starting with `on` (fail-closed) |
| Alpine.js shorthands | every name starting with `@` or `:` |
| Alpine.js directives (before any `:` or `.`) | `x-data`, `x-init`, `x-show`, `x-text`, `x-html`, `x-model`, `x-modelable`, `x-for`, `x-effect`, `x-if`, `x-id`, `x-on`, `x-bind`, `x-trap`, `x-intersect`, `x-anchor`, `x-sort`, `x-mask` |
| HTMX (also `data-hx-…`) | `hx-on`, `hx-on:*`, `hx-on::*`, `hx-vals`, `hx-vars`, `hx-headers`, `hx-trigger` |

These Alpine.js names are not script, because their values are not
expressions: `x-cloak`, `x-ref`, `x-transition*`, `x-teleport`, `x-ignore`,
and `x-collapse`. The survey found `x-show` (64), `x-text` (51), `x-model`
(42), `x-data` (28), `x-bind` (19), `x-for` (11), `x-init` (5), `x-if` (3),
`x-on` (2), and `onclick`, `onchange`, and `oninput` (27). Among HTMX
attributes it found an interpolated `hx-trigger` and literal `hx-vals`.

In a script attribute:

- **Static literal text** is unchanged: it is attribute-escaped and
  evaluated as written.
- **Interpolated paths** may be:
  - `TrustedScript`, written with `rt.ScriptAttr`;
  - `JSONValue`, written with `rt.JSONAttr`;
  - `boolean`, written with `rt.ScriptBool` as the literal `true` or `false`.
  This holds for exact and mixed values, so `x-show="!{open} && ready"` and
  `x-init="boot({state})"` are valid.
- **A boolean is a value, not presence.** `:disabled="{busy}"` renders
  `:disabled="true"` or `:disabled="false"`, which is what Alpine.js
  evaluates.
- **Every other type is rejected.** A string, number, integer, `URL`, or
  `TrustedURL` path is `script-attribute-value` at the path. The message names
  the attribute and asks for the value to be built in Go with
  `ktml.JSON` or `ktml.TrustScript`.

Outside script attributes:

- `JSONValue` renders as an exact plain attribute value (`rt.JSONAttr`).
- `TrustedScript` is `contextual-type-mismatch`.
- Both are `contextual-type-mismatch` in text.

The compiler lowers these two contextual types from this slice on. `CSSValue`
and `TrustedHTML` stay reserved.

## 2. Doctype

```ebnf
doctype = "doctype", identifier ;   (* both on one line *)
```

```ktml
Layout [title: string, children: content] {
	doctype html
	html lang="en" {
		head {
			title "{title}"
		}
		body {
			children
		}
	}
}
```

- `doctype` is a keyword at the start of a body node, so it cannot name an
  element. It is reserved there in the same way as `else` and `for`. As an
  attribute name it is unaffected.
- Its name must be on the same line. If it is missing, both parsers report a
  syntax error.
- **Semantics:**
  - The only accepted name is `html` (`invalid-doctype` otherwise). HTML is
    the only output vocabulary (ADR-0001).
  - A doctype may appear only directly in a component, `default`, or Render
    Clause body, before every other node except HTML comments
    (`misplaced-doctype`). This also rejects a second doctype, and a doctype
    inside an element, condition, repetition, or slot fill.
- **Output.** It renders `<!doctype html>`. That is templ's spelling, so converted
  pages keep their bytes.
- A top-level `doctype` is a syntax error: top-level nodes are declarations.

## 3. HTML comments

```ebnf
html_comment = "<!--", { any character, so that "-->" does not occur }, "-->" ;
```

```ktml
nav {
	<!-- Mobile menu button -->
	button type="button" "Menu"
}
```

- An HTML comment is a body node. It is legal in every body position and may
  span lines. It ends at the first `-->`. A missing `-->` is a syntax error.
- `//` line comments are unchanged. They are source trivia and never reach
  the output.
- On a header line, `<!--` ends the attribute list, as `if` does. The comment
  is then a sibling node.
- The text is static. Braces in it are literal, never interpolation.
- **Semantics.** The text must be valid HTML comment text: it must not start
  with `>` or `->`, must not contain `<!--` or `--!>`, and must not end with
  `<!-` (`invalid-html-comment`).
- **Output.** It renders verbatim as `<!--text-->`.

## AST

| Node | Fields |
| --- | --- |
| `AttributeNode` | unchanged; `Name` may now contain `@`, `:`, and `.` |
| `DoctypeNode` | `P`, `E`, `Name`, `KeywordRange`, `NameRange` |
| `HTMLCommentNode` | `P`, `E`, `Text` (between the delimiters) |

Both new nodes carry a `_type` in the plugin/WASM JSON payload.

## Formatter

- Attribute names are written verbatim. A name starting with `@` or `:` keeps
  its separating space.
- `doctype html` is one line.
- A comment's text is preserved byte for byte, including its newlines.
  Formatting is idempotent.

## Lowering

- **gogen:**
  - names are written verbatim in the coalesced static markup;
  - `doctype html` writes `<!doctype html>`;
  - a comment writes `<!--text-->`.
  All three are static writes with no runtime cost.
- **gohtml** (prototype backend):
  - framework attribute names with literal values pass through;
  - it fails before output on a doctype, an HTML comment (`html/template`
    strips comments), and any interpolated script attribute, because it
    has no script-attribute context.

## Tooling and HMR

- **LSP:**
  - semantic tokens: `doctype` is a keyword;
  - Tree-sitter highlighting: attribute names (`attribute_name`), the
    doctype, and comments;
  - the HTMX plugin's route attributes match only at an attribute-name
    boundary, so `x-bind:hx-get` and `:hx-get` are not routes.
- **HMR:** attribute renames, doctype changes, and comment edits are body
  changes. When the signature is unchanged they are compatible patches.

## Conformance corpus

- `elements` already asserts each attribute's name and range. It now covers
  framework names.
- A new `markup` field lists every doctype and HTML comment in pre-order. Each
  entry has `kind` (`doctype` or `comment`), the whole range, and either the
  doctype `name` or the comment `text`.

## Release gates

| Layer | This slice |
| --- | --- |
| Canonical contract | this document |
| Go parser | attribute names, separation, doctype, HTML comments, rejected forms |
| Tree-sitter corpus | the same; `attribute_name`, `doctype`, and `html_comment` nodes |
| Formatter | verbatim names, doctype, comments; idempotent |
| Semantic model | `invalid-doctype`, `misplaced-doctype`, `invalid-html-comment`, `script-attribute-value`; `JSONValue`/`TrustedScript` types; framework names on calls are `unknown-input` |
| Interpreter | done: `dev/interp`, with the differential parity harness (ADR-0004) |
| Direct Go generator | names, doctype, comments as static writes; script attributes through `rt.ScriptAttr`, `rt.JSONAttr`, `rt.ScriptBool` |
| gohtml | literal names pass through; doctype, comments, and interpolated script attributes fail before output |
| CLI, LSP, HMR | as above |
| Explorer | not applicable |
| Source maps | deferred, as for the earlier slices |
