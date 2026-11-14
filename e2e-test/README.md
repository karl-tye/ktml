# KTML E2E Fixture

This directory is both the automated end-to-end fixture and a manual workspace
for testing KTML editor ergonomics.

It models the parts of a typical server-rendered Go web application that KTML
must support:

- Chi routes, including a nested `Route` scope.
- A conventional `cmd/server` and `internal/app` package layout.
- Full-page and `HX-Request` fragment rendering.
- HTMX form submission, `PUT` actions, targeted swaps, and OOB fragments.
- Alpine-compatible attributes and Tailwind utility classes.
- Component Explorer rendering and HMR.
- HTMX-to-Chi completion and definition data through the KTML LSP.

All fixture components declare the inputs they consume. In particular, the
top-level `html` component declares `count: string` so generation exercises
cross-file semantic validation without relying on implicit template data.

## Automated suite

From this directory:

```sh
go test -count=1 .
```

The suite builds the KTML CLI and first-party WASM plugins before exercising the
HTTP, route discovery, LSP, browser, reload, and HMR contracts.

## Neovim

Open this directory as the working directory:

```sh
nvim .
```

Trust `.nvim.lua` when prompted with `:trust`. It registers the `ktml` filetype,
starts `ktml lsp` for KTML and Go buffers, and provides:

- `:KtmlDev` to run the development server.
- `:KtmlTest` to run this fixture's suite.

Open `index.ktml` or `components.ktml` and edit an `hx-get`, `hx-post`, or
`hx-put` value to exercise Chi route completion and navigation.
Open `internal/app/app.go` and rename a static route from its Chi registration to
update the registration and matching HTMX consumers together.

Neovim must have local project configuration enabled (`:set exrc`). Generated
HTML, generated Go, the local CLI, and `.ktml` state are intentionally absent
from the fixture and are created by the test suite or development server.
