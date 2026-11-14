# Generated Go and gopls

KTML generates Go: `foo.ktml` becomes `foo_ktml.go` (and the `ktmldev`
twin `foo_ktml_dev.go`), and `foo.ktmlschema` becomes `foo_ktmlschema.go`.
Two pieces put Go errors on the KTML lines they come from:

- the generator writes a source map into the generated Go as standard
  line directives, which `go build`, `go vet`, and `go test` read;
- `ktml lsp` runs gopls behind a native adapter, which maps gopls
  diagnostics through the same map and adds handwritten Go to Host Schema
  navigation and rename.

## The source map

Every generated token that can carry a Go error is preceded by a
[line directive](https://pkg.go.dev/cmd/compile#hdr-Compiler_Directives)
naming the KTML range it was generated from:

| Generated Go | Mapped to |
| --- | --- |
| a component's struct, `Render` method, and renderer | the component's name |
| a props field, and a defaulted input's default | the input's name |
| a path read: text, attribute, condition, loop collection and key, call input | the interpolation or path |
| a component call, and a call input's field | the call's element name, the attribute name |
| a content outlet | the outlet element |
| Render Clause dispatch | the selector input, and each clause's pattern or the default body |
| a record and its fields | the record and field names |
| a state, its discriminator, Variant constants, payloads, constructors, and accessors | the state and Variant names, and payload field names |

```go
if err := rt.Text(w /*line ui.ktml:12:29*/, props.View.Month); err != nil { /*line ui_ktml.go:40:1*/
```

- A directive names the KTML file relative to the generated file, so the
  output does not depend on where it was generated, and it is
  byte-for-byte deterministic.
- Directives are inline comments inserted after gofmt; the file is then
  formatted again until it is stable, so generated files stay gofmt-clean.
  gofmt keeps a space after a directive and moves one that followed a comma
  before the comma; the directive's column allows for both.
- Every line that maps a token ends with a directive restoring the
  generated file's own line number, so unmapped code (error returns, static
  writes) keeps its real position.
- Columns are exact, except within the first two columns: a directive's
  column cannot be below 1, so a component or record name at column 1 is
  reported at column 2.
- `gogen.GenerateFileMap` and `gogen.GenerateSchema` also return the map
  as Go byte offsets with their KTML ranges. `gogen.StripLineDirectives`
  returns the code without them, for inspection.

The map lives in the generated file, so it cannot be stale relative to the
code it describes; regenerating (`ktml generate`, or `ktml dev` on every
change) rewrites both together. There is no sidecar file.

### Production builds

Line directives stay in production generated files (decided 2026-10-14).
They are compile-time position information, not development markers: the
release gate "Strip development markers" covers runtime and
browser-visible artifacts, which directives are not. They change no
generated statement, so production renders are byte-identical, and they add
no registry, marker, route, or dependency. The only effect on a binary is
that its position tables name `ui.ktml:12` instead of `ui_ktml.go:40`, so a
panic's stack trace points at the KTML line, and `go build` errors in a
normal build name `.ktml` lines. The e2e release gate builds the production
binary and checks that it links no `ktml/dev` package.

### Example

A handwritten `card.go` that declares `type Card struct{}` in the package
where `view.ktml` declares the `Card` component:

```text
$ go build ./...
./view.ktml:3:2: Card redeclared in this block
	./card.go:3:6: other declaration of Card
./view.ktml:14:7: unknown field View in struct literal of type Card
```

A presenter method that shadows a record field, in `present.go`:

```text
./present.go:3:19: field and method with the same name Title
	./view.ktmlschema:2:2[./view_ktmlschema.go:7:31]: other declaration of Title
./view.ktml:5:7: cannot use props.View.Title (value of type func() string) as string value in argument to rt.Text
```

## The gopls adapter

`ktml lsp` starts gopls with the first KTML document of a Go module, as a
client of a shared gopls daemon. The editor keeps its own gopls for `.go`
files; LSP gives one server no way to reach another, so `ktml lsp` cannot
use the editor's session. Through the daemon it can share the editor's
gopls process instead, and several `ktml lsp` sessions share one daemon.

It is on by default. Configure or disable it in `ktml.config.json`:

```json
{ "gopls": { "command": ["gopls"], "remote": "auto", "disabled": false } }
```

| `remote` | gopls runs |
| --- | --- |
| `"auto"` (the default) | as `gopls -remote=auto`: the daemon every gopls started with `-remote=auto` shares, started on first use |
| `"unix;/path/to/socket"` | as a client of the daemon listening on that socket |
| `"host:port"` | as a client of the daemon listening on that TCP address |
| `"off"` | privately, as a child of `ktml lsp` |

`command` is the gopls to run; the adapter adds `-remote=<remote>` after
its first word, unless the command names its own `-remote`.

### The shared daemon

gopls's daemon mode (`gopls -remote`; see `gopls help`) runs one
long-lived gopls that several clients connect to. Each client, here
`ktml lsp`'s and the editor's, starts a small forwarder `gopls -remote=...`
on stdio, which relays LSP to the daemon. What this means, as probed with
gopls v0.23.0:

- **Overlays stay per client.** The daemon gives each connection its own
  session, with its own open files. The generated-Go overlays `ktml lsp`
  opens are never seen by the editor: an editor session on the same module
  still sees the generated files as they are on disk (a protocol test
  checks this).
- **The cache is shared.** Sessions share the daemon's cache of parsed and
  type-checked packages and its memory. Probe: two sessions on the ktml
  repository took about 176 MB in one daemon plus about 16 MB for each
  forwarder, against about 130 MB for each of two private gopls.
- **Shutdown.** A daemon that `-remote=auto` started exits a minute after
  its last client disconnects (`-remote.listen.timeout`, default `1m`) and
  removes its socket. It runs in a session of its own, so it outlives the
  `ktml lsp` that started it, until then.
- **Where `auto` is.** `-remote=auto` uses the socket
  `$XDG_RUNTIME_DIR/gopls-<hash>-daemon.$USER` (or `$TMPDIR` without
  `XDG_RUNTIME_DIR`), where the hash is of the gopls binary's build ID. Only
  gopls processes of the same build and user share it; the editor and
  `ktml lsp` must run the same gopls build.
- **Environment.** Each forwarder sends its `go env` with initialize, so a
  session uses its client's Go environment, not the daemon's.

If the daemon is unreachable (an explicit address with nothing listening
is checked first, without waiting), or fails while in use, the adapter
logs `gopls daemon <remote> unavailable (...); using a private gopls` to
stderr and as `window/logMessage`, starts a private gopls, and opens every
overlay in it again. It does not go back to the daemon in that session.

### Sharing the daemon with Neovim

Start the editor's gopls with `-remote=auto` too, from the same gopls
build, so that it and `ktml lsp` share one daemon. With Neovim 0.11's
built-in configuration:

```lua
vim.lsp.config("gopls", { cmd = { "gopls", "-remote=auto" } })
vim.lsp.enable("gopls")
```

With nvim-lspconfig: `require("lspconfig").gopls.setup({ cmd = { "gopls",
"-remote=auto" } })`. Check it with `gopls -remote=auto remote sessions`,
which lists one client for each editor and `ktml lsp` session, plus its
own.

KTML is tested with gopls `v0.23.0` (`lsp.GoplsPinnedVersion`):
`go install golang.org/x/tools/gopls@v0.23.0`. Without gopls, KTML's own
features work unchanged; `ktml lsp` says once that gopls is unavailable.

The adapter's tests run their own daemons on temporary sockets: a protocol
test connects two `ktml lsp` sessions to one daemon, another checks that
an editor session does not see `ktml lsp`'s overlays, and three cover the
fallback to a private gopls, one of them with an edit made during the
switch, whose diagnostics must win over the regeneration. An e2e test runs two `ktml lsp` processes with
the default configuration and checks that they share one daemon. Test runs
point `XDG_RUNTIME_DIR` at a temporary directory, so they never join the
user's daemon, and stop the daemons they started.

### Overlays

For each open document, the adapter generates the production Go of every
KTML document in its package, and in the packages it imports from, from the
editor's buffers. It opens each file in gopls as an overlay of the
generated file's own path. gopls therefore checks the code the current
buffers would generate, whether or not `ktml generate` has run, and never a
stale file on disk. Packages whose documents import from an edited package
are regenerated too.

### Diagnostics

- gopls reports positions in the generated file; it does not apply line
  directives. The adapter maps each error and warning through the overlay's
  source map to the KTML range of its construct, with gopls as the source
  and the generated position as related information. Hints about generated
  code are dropped.
- A diagnostic of handwritten Go, such as a presenter, that relates to a
  generated declaration (a redeclaration, or a method that collides with a
  field) is also shown on that declaration's KTML range, naming the Go
  position.
- Stale maps are never used: diagnostics are mapped only for the overlay
  version gopls reports them for. A document that stops compiling has its
  mapped diagnostics withdrawn until it compiles again; KTML's own
  diagnostics explain why.
- gopls reads handwritten Go from disk. `ktml lsp` forwards the editor's
  file-change events, so presenter edits count once they are saved.

### Navigation and rename

- **Definition** of a Host Schema record or field, at its declaration or at
  a use in `.ktml`, adds its uses in handwritten Go, such as the
  presenter line that sets it, to the KTML definition.
- **References** of a record, a field, or a component add their uses in
  handwritten Go. Uses in generated files are left out: they are the KTML
  uses the KTML references already list.
- **Rename** of a record or field also renames its Go name in handwritten
  Go through gopls (`title` → `heading` renames `CardView.Title` to
  `CardView.Heading`). It answers only when the KTML rename is valid. When
  gopls refuses or is unavailable, and for states and Variants (whose Go
  names derive several identifiers), the KTML rename still applies and a
  warning says what was not renamed in Go.
- **Go to KTML** from Go is not available through the editor's gopls,
  which lands on the generated declaration. Its directive comment names the
  KTML source and position.

### Limits

- Only the production variant (`!ktmldev`) is overlaid, as gopls checks the
  default build.
- gopls sees handwritten Go as saved on disk, not unsaved editor buffers.
- Signature help, completion, and hover from gopls are not proxied.

## Hover

A hover on a path part, call, or declaration also shows gopls's hover of
the Go identifier the source map maps it to; see
[KTML language features](language-features.md#gopls-in-ktml).
