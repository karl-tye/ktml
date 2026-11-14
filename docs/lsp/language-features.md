# KTML language features

What `ktml lsp` answers beyond diagnostics and completion. Navigation is
in [Symbol index](symbol-index.md).

## Signature help

Inside a component call the signature is the component's inputs as its
declaration writes them:

```text
Field [label: string, hint?: string, size: string = "md", attrs?: Attributes, children: content, footer?: content]
```

The active argument is:

- the attribute whose value, or name being typed, holds the cursor:
  `Field label="|"`, `Field hi|`;
- the input an attribute bag literal fills: `Field attrs=(id="a" |)`;
- the named slot a `slot` line names, in the call body: `slot |`;
- otherwise the first required argument the call does not give yet.

Each argument's documentation says whether it is required, optional, or
defaulted, and how content and bags are given. The call is found lexically,
so a call still being typed has help, and the component resolves from the
last analysis that compiled. Help triggers on space, `=`, and `(`, and
again inside a quoted value.

## Formatting

Full formatting replaces the document with `ktml fmt`'s output. Range
and on-type formatting apply the line edits between the document and that
same output that touch the requested lines, so formatting part of a
document never disagrees with formatting all of it:

- a range formats the lines it covers;
- typing `}` formats the block it closes, from its `{` line;
- typing a newline formats the line it ended, never the new line the
  editor indented for the cursor.

A document with a syntax error is never changed: formatting prints the
syntax tree, which would lose what the parser skipped.

## Semantic tokens

Full results carry a result ID. A delta request is answered with the one
edit from the data last sent for the document, or with a full result when
the previous ID is unknown. Range requests return the tokens that overlap
the range.

## gopls in KTML

Hover on a path part, call, or declaration also shows gopls's hover of
the Go generated for it: the source map gives the generated token, and a
path's parts align with the Go selector chain they generate (`view.title`
with `props.View.Title`). It is shown only while the document's overlay
was generated from the current buffer.

Completion is not proxied. While a path is being typed the document does
not compile, so its overlay and source map are stale; and where they are
current, gopls's member completion of the generated structs offers the
record fields KTML's typed-path completion already offers, plus Go methods
a KTML path cannot call.

## ktml vet

`ktml vet`'s findings are diagnostics on the handwritten Go that has them:
a props literal that omits a required input, on the literal's type, as a
warning from `ktml vet`. The module is vetted in the background once the
client is initialized, after KTML edits (with the unsaved buffers), and
after Go files change on disk.
