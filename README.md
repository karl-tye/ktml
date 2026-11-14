# KTML

KTML is a small, typed HTML component language for Go web applications built
with Chi, HTMX, Alpine.js, and Tailwind. It compiles to direct Go renderers
(no runtime template parsing), and comes with a development server with
component hot reload, a federated language server, a Component Explorer, and
a converter from [templ](https://templ.guide).

> **Status: early and unstable.** The language, the CLI, and the Go API may
> change in any release before 1.0. Pin a version.

## Install

```bash
go install github.com/karl-tye/ktml/cmd/ktml@latest
```

Or pin it as a Go tool in your application (Go 1.24+):

```bash
go get -tool github.com/karl-tye/ktml/cmd/ktml
go tool ktml generate ./...
```

## Quick start

Declare the data your templates may read in a Host Schema
(`web/model.ktmlschema`):

```ktml
record Task [
	id: string,
	title: string,
]

record Board [
	title: string,
	tasks: [Task],
]
```

Write components (`web/board.ktml`):

```ktml
import { Task, Board } from "./model.ktmlschema"

Page [board: Board] {
	doctype html
	html lang="en" {
		head {
			title "{board.title}"
		}
		body {
			h1 "{board.title}"
			ul {
				for task in board.tasks key task.id {
					TaskItem task="{task}"
				}
			}
		}
	}
}

TaskItem [task: Task] {
	li data-id="{task.id}" "{task.title}"
}
```

Generate Go and render it from a handler:

```bash
ktml generate ./web
```

```go
board := web.Board{Title: "Today", Tasks: []web.Task{{Id: "1", Title: "Write docs"}}}
if err := (web.Page{Board: board}).Render(r.Context(), w); err != nil {
	http.Error(w, "render failed", http.StatusInternalServerError)
}
```

Business logic (formatting, permissions, filtering, state) stays in Go: a
presenter builds the schema values, and templates only compose, read fields,
test booleans and optional values, select Presentation States, and loop over
keyed collections. Text, attributes, URLs, CSS, JSON, and scripts are escaped
for their context.

`ktml generate -check ./...` fails when committed generated Go is missing,
changed, or stale, for applications that commit the output.

## Tools

| Command | What it does |
| --- | --- |
| `ktml generate` | Compiles `.ktml` and `.ktmlschema` files to Go |
| `ktml fmt` | Formats KTML |
| `ktml vet` | Reports component literals that omit a required input |
| `ktml dev` | Development proxy: regenerates, rebuilds, and patches components live |
| `ktml lsp` | Language server for KTML, embedded Go, HTML, HTMX/Chi routes, Alpine.js, and Tailwind |
| `ktml explore` / `ktml stories generate` | Component Explorer over typed `.story.ktml` stories |
| `ktml migrate templ` / `ktml migrate codemod` | Converts templ templates and rewrites their Go call sites |

Editor support: a tree-sitter grammar in [`tree-sitter-ktml`](tree-sitter-ktml)
and a Neovim setup in [`editors/nvim`](editors/nvim).

## Documentation

- [Language specification](spec.md) and the accepted
  [language boundary (ADR-0001)](docs/adr/0001-language-boundary.md)
- [Language documents](docs/language): Host Schemas, Render Clauses, Keyed
  Repetition, stories
- [CLI and plugin architecture](cli-spec.md)
- [Rendering API (ADR-0003)](docs/adr/0003-rendering-api.md)
- [Converting from templ in place](docs/migration/in-place-conversion.md)
- [Testing](TESTING.md) and the [plan](PLAN.md)

## Licence

[MIT](LICENSE). Vendored third-party files are listed in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
