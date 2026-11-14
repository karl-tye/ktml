# Symbol index

`ktml lsp` keeps one persistent, incrementally updated index of the
workspace's symbols. Every analyzer contributes to it: the KTML tooling,
the HTMX routes, Alpine, and Tailwind. Definition, references, document
highlights, and call hierarchy answer from it. No KTML request falls back
to matching words: a word that only spells a declaration's name finds
nothing.

## What it holds

| Kind | Declared by | Referenced by | Provider |
| --- | --- | --- | --- |
| `component` | its base declaration | imports, calls, overrides, Render Clause headers, stories | gohtml |
| `input`, `slot` | the declaration's input list | call arguments, path roots, outlets, slot fills, story arguments | gohtml |
| `binding`, `loop-binding` | a clause binding, a repetition item | path roots in its body | gohtml |
| `alias` | an import's `as` name | | gohtml |
| `record`, `state`, `variant`, `field` | the Host Schema | type uses, Patterns, path parts | gohtml |
| `story`, `fixture` | the story document | | gohtml |
| `route` | the Chi registration in Go | each `hx-*` request attribute that reaches it | htmx |
| `alpine-binding` | `x-data` properties, methods, getters, `x-for` items, `x-ref`s | the expressions that resolve to it | alpine |
| `class` | | each class token | Tailwind adapter |

A reference made inside a component or story carries it as `from`: those
are the call graph's edges. Classes are declared in stylesheets, so the
index holds their uses; Tailwind's theme navigation finds the stylesheet.

## The protocol

A provider is a plugin whose manifest declares the `symbol-index`
capability, which needs plugin ABI 2, or a native adapter that implements
`lsp.IndexProvider`. The host sends it `$/ktml/index` through
`ktml_lsp_handle`, and only to plugins that declare the capability:

```json
{"id": 1, "workspace": true}
{"id": 2, "uris": ["file:///app/ui/card.ktml"]}
```

The answer is a JSON-RPC response whose result is the documents the
provider indexed, and the URIs whose contributions to drop:

```json
{"documents": [{"uri": "file:///app/ui/card.ktml",
  "symbols": [{"id": "/app/ui/card.ktml::component::Card", "kind": "component", "name": "Card",
    "range": {"start": {"line": 0, "character": 0}, "end": {"line": 0, "character": 4}}}],
  "references": [{"symbol": "/app/ui/badge.ktml::component::Badge", "kind": "call", "from": "/app/ui/card.ktml::component::Card",
    "range": {"start": {"line": 3, "character": 1}, "end": {"line": 3, "character": 6}}}]}],
 "removed": []}
```

- A returned document replaces everything that provider contributed for its
  URI. Other providers' contributions for the same URI stay.
- Symbol IDs are opaque strings shared across providers: a reference may
  name a symbol that another document, or another provider, declares.
- For changed URIs, a provider also returns any document whose symbols the
  change affects: gohtml returns every document that imports a changed
  one, directly or not, and HTMX returns every Go file with routes.
- Ranges are LSP ranges (zero-based lines, UTF-16 columns). Reference kinds
  are `read`, `write`, `call`, `import`, and `type`.

The Go types are in `internal/symbolindex`.

## Keeping it current

The host asks every provider for the workspace once the client is
initialized. After that it asks for the documents each change names:

- `textDocument/didOpen`, `didChange`, and `didClose`;
- `workspace/didChangeWatchedFiles`: the host registers watchers for
  `**/*.go`, `**/*.ktml`, `**/*.ktmlschema`, and `ktml.config.json`; a
  deleted document leaves the index, a created one joins it;
- a plugin's background job result, such as an HTMX route scan: that
  plugin is asked for the workspace again.

One worker asks the providers, concurrently with each other, in the order
the changes arrive, so a later answer always replaces an earlier one. A
burst of edits waiting for the worker becomes one request. Navigation
requests (definition, references, highlights, call hierarchy, rename,
prepare rename, code lenses, workspace symbols) wait until every earlier
change is indexed before any analyzer sees them, so the index and the
analyzers answer from the same state. Completion, hover, and the other
requests a keystroke sends do not wait.

gohtml keeps one project analysis that every edit updates and every
request reads; other workspaces record the edit and analyze it only when
asked. A workspace reuses its last analysis while no overlay changed and
no file it read, or failed to find, changed on disk. Without file watching
in the editor, changes made outside it are seen at the next edit of an
open document.

## Answers

| Request | Answer |
| --- | --- |
| `textDocument/definition` | the declarations of the symbol at the cursor, beside the analyzers' answers, without repeats |
| `textDocument/references` | its declarations (when asked) and every use, beside the analyzers', without repeats |
| `textDocument/documentHighlight` | its declarations (write) and uses (read or write) in the document; empty off a symbol |
| `textDocument/prepareCallHierarchy` | the component or story at the cursor |
| `callHierarchy/incomingCalls` | the components and stories that call it, with the call sites |
| `callHierarchy/outgoingCalls` | the components it calls, with the call sites |

## Performance

Measured on the development machine on 2026-10-20 by
`TestSymbolIndex_GIVENALargeGeneratedWorkspace_WHENIndexedAndEdited_THENStaysWithinBudget`
and, with `KTML_HOST_DIR` naming a large host application (and
`KTML_HOST_HUB` and `KTML_HOST_LEAF` a widely imported and an unimported
document in it), `TestSymbolIndex_GIVENAHostApplicationsTree_*` (gohtml and
Alpine plugins, through the real host). Edit latency is from
`didChange` to the answer of a references request that needs the edit
indexed.

| Workspace | Initial index | Host heap (all plugins) | Index alone | Leaf edit | Hub edit |
| --- | --- | --- | --- | --- | --- |
| generated: 400 documents, 20 directories | 2.3 s | 115 MiB | 2.7 MiB (2,407 symbols, 11,337 references) | 0.65 s | 1.6 s |
| a large host application: 83 documents and their schemas | 4.4 s | 255 MiB | 10.5 MiB (8,003 symbols, 18,271 references) | 0.73 s | 0.68 s |

The generated hub is imported, directly or not, by every document, so its
edit reindexes all 400. Most of an edit's cost is analyzing the project in
the plugin's WebAssembly guest, which runs the compiler about 50 times
slower than native Go; the index request itself takes about 35 ms.
