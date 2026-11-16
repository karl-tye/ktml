# Component Explorer v1

`ktml explore` is a development-only server for developing components on
their own. It lists the [stories](../language/stories-v1.md) of a workspace,
renders each one in an isolated preview, derives typed controls from the
component's inputs, applies story and component edits as live patches, and
shows diagnostics inline.

```sh
ktml explore [-addr 127.0.0.1:7332] [root]
```

- `root` defaults to `.`; the Explorer reads `root/ktml.config.json`.
- `-addr` must be a loopback address (`127.0.0.1`, `::1`, or `localhost`);
  any other address is refused. Port `0` picks a free port. The URL is
  printed on start.
- Stop it with Ctrl-C.

## Isolation and the development boundary

- Only the `ktml` command links the Explorer (`internal/explorer`).
  Application binaries never do, and stories are never generated.
- It binds to loopback only and answers only requests whose `Host` is a
  loopback name, so a DNS-rebinding page cannot reach it.
- Previews render in a sandboxed `iframe` (`allow-scripts allow-same-origin
  allow-forms`), each its own document with the project's stylesheets and
  scripts, so the Explorer's own styles never reach a component.
- It loads nothing from the network. HTMX (4.0.0-beta4) and Alpine.js
  (3.14.9) are bundled in the binary; projects can serve their own copies
  instead (below).

## How it renders

The Explorer is a `ktml dev`-shaped pipeline with no application process:

```text
browser ──► loopback front (livereload proxy, Host check)
              ├─ /__ktml/livereload   SSE: patch, reload, diagnostic, clear
              └─► internal loopback backend
                    ├─ hmr.Handler (page sessions)
                    ├─ /                     interface
                    ├─ /_explorer/preview    story ──► interp.Program.RenderStory
                    ├─ /_explorer/edit       typed control edits
                    ├─ /_explorer/requests   the Requests panel's list (GET, DELETE)
                    └─ <static url>          project assets
watcher ──► validate ──► hmr.HMR.Swap ──► patches ──► livereload ──► preview
```

- Previews render through the same development interpreter
  (`dev/interp`) and are tracked as roots of the same render registry
  (`dev/hmr`) that `ktml dev` uses in an application.
- On a valid change to any `.ktml`, `.ktmlschema`, or `.story.ktml` file
  the Explorer calls the same `HMR.Swap` that `ktml dev` calls through the
  application's control channel. The swap rerenders every open preview that
  renders a changed component or story, and the livereload hub sends each
  patch to its preview, whose injected client morphs it in place (Alpine
  and HTMX lifecycles included). A preview whose rerender fails reloads.
  There is no Go code, so schema and signature changes patch too.
- On an invalid change the last valid program keeps rendering; the
  diagnostics show in the interface and in each preview's overlay until the
  next valid change.
- A removed Document rebuilds the program from disk and reloads every
  preview.

### Requests from a preview

Previews have no application behind them. Every HTMX request a preview
makes, of any method and to any URL (the Explorer's own included), is
answered `204 No Content`, which htmx 2 and 4 swap nothing for, so a
component that loads or polls keeps its rendered DOM. A request is HTMX's
when it carries `HX-Request: true` (htmx 2 and 4) or the headers of the
preview hook `/_explorer/assets/preview.js`, which every wrapped preview
loads first and which names the request's element and trigger
(`htmx:configRequest`, `htmx:config:request`). Requests without them,
such as stylesheets, scripts, and images, are served as before. URLs under
`/_story/`, where generated stories put their placeholder URLs, are
answered 204 for any request.

The **Requests** panel under the preview lists the story's HTMX requests:
method, URL, element and trigger (from the hook, else htmx's `HX-Source`
or `HX-Trigger`), and time; newest first, the last 50 distinct ones, with
repeats such as polling collapsed into a count. It updates live through
`/_explorer/events` and has a Clear button.

## Browsing stories

The sidebar groups stories by kind, then package, then component:

- **Pages** are components a route's handler renders, **Components** are
  reached only through KTML calls, and **Other** are the rest. The kind
  comes from the reachability `ktml stories generate` computes (Chi routes
  from `plugin.htmx.settings.chi`, else the whole module), which the
  Explorer computes in the background at start and after each component
  change, and logs the first time (`stories grouped by reachability`).
  Until then, or when it cannot be computed (no Go module, no routes), a
  story's generated header (`// X is a page, reached by:`) gives the kind
  and routes; a component with neither is Other.
- A package is the component Document's directory relative to the root,
  without the parent directories every package shares (`internal/web` is
  `web` when every package is under `internal`). Kinds and packages show
  their story counts. Packages and components sort by name; stories keep
  their Document order.
- Groups start collapsed. The groups you open or close are remembered in
  the browser (`localStorage`), and the selected story's path is always
  open. The URL names the selected story (`/?story=…`), so links to a story
  work.
- Choosing a story opens it in place, without reloading the interface: the
  URL is pushed, and the title, controls, diagnostics, Requests panel, and a
  fresh preview frame are swapped in from `/_explorer/state`, so the
  sidebar keeps its search, open groups, and scroll position. Back and
  Forward open their stories the same way; a story opened with a modifier
  key or the middle button, a deep link, and a reload load the page as a
  link does.
- The search box (`/` focuses it, Escape clears it) filters, as you type
  and ignoring case, by component, story, package, or route; it marks the
  matches, shows a matching route under its component, and opens the
  groups that match.

## Controls

Controls are derived from the component's input types, never from the
values. Every value a control sends is decoded by the server against the
current types before it renders.

| Input | Control |
| --- | --- |
| `string`, `URL` | text field |
| `number` | number field (any step) |
| `integer` | number field (step 1; whole numbers only) |
| `boolean` | checkbox |
| record | a group of its fields' controls |
| collection | a list of its items' controls, with Add and Remove |
| Presentation State | a Variant select, then its payload fields' controls; choosing a Variant starts its payload at zero values |
| enum-like state (Variants without payloads) | a Variant select |
| optional input or field | a `None` switch; switching it off starts the value at its zero value |
| defaulted input | a `use default` switch, then the value's control |
| `TrustedURL`, `TrustedScript`, `JSONValue`, `CSSValue` | a select of the story Document's fixtures of that type, never a text field |
| content (children, slots) | given by the story body |
| attribute bag | none: a story gives only the empty bag `[]` |

A viewer's changes override only the inputs they touched; the rest follow
the story Document, so editing the story updates an open preview. **Reset
controls** returns to the story. A changed value that no longer types after
an edit (for example after an input's type changed) falls back to the
story's value and says so next to its control.

Control changes reload the preview frame with the new values; Alpine state
in the preview is therefore not kept across control changes (it is kept
across HMR patches).

## Diagnostics

The interface lists every syntax and type diagnostic of the workspace, and
the selected story's last render error, each with `file:line:column`, its
code, and the source line with the range marked. A render error names the
component, input, or story it comes from (for example a duplicate key in an
input's collection). The same syntax and type diagnostics appear in the
preview's overlay.

## Configuration

```json
{
  "explorer": {
    "static": { "dir": "static", "url": "/static" },
    "stylesheets": ["/static/css/main.css"],
    "scripts": ["/static/js/htmx.min.js", "/static/js/alpine.min.js"],
    "body_class": "latte bg-base text-text"
  }
}
```

- `static` serves a project directory at the URL path the application
  serves it from, so component markup and stylesheet `url()`s resolve. The
  directory must be inside the project; files are served through an
  `os.Root`, so symlinks cannot escape it.
- `stylesheets` and `scripts` are URL paths each preview loads, in order;
  scripts load `defer`red. Remote URLs are rejected. Without `scripts` the
  bundled HTMX and Alpine.js load.
- `body_class` is the class of each preview's `body`, for themes set there.

### Starting it with `ktml dev`

`ktml dev` also serves the Explorer when the `dev` section configures it:

```json
{
  "dev": {
    "proxy_target": "http://127.0.0.1:8080",
    "command": ["{binary}"],
    "build": ["go", "build", "-tags", "ktmldev", "-o", "{binary}", "./cmd/server"],
    "explorer": { "addr": "127.0.0.1:7332" }
  }
}
```

`addr` follows the rules of `ktml explore -addr`: it defaults to
`127.0.0.1:7332`, port 0 picks a free port, and only a loopback host
(`127.0.0.1`, `::1`, or `localhost`) is accepted; any other address is a
configuration error at `dev.explorer.addr`, which `ktml dev` refuses and the
LSP shows on the value. `ktml dev --explorer-addr host:port` overrides it
with the same rules, and serves the Explorer even without `dev.explorer`.
The Explorer runs in-process over the project root
with its own loopback server, livereload hub, and watcher (the same
`explorer.Server` that `ktml explore` runs), reads the top-level `explorer`
section above, logs `KTML Explorer on http://…/`, and stops with `ktml dev`.
The dev generation loop still ignores story documents. Without
`dev.explorer`, `ktml dev` starts no Explorer; `ktml explore` is unchanged.

### Opening a component from the app

`GET /_explorer/open?component=<import path>.<Name>` redirects (303) to the
component's story: its default story, else its first. It only reads; a
component without a story gets a 404 page naming its story Document. The
component picker that `ktml dev` adds to the app's pages (`dev.picker`; see
[cli-spec.md](../../cli-spec.md) §1) opens it in one reused tab named
`ktml-explorer`. When the picked component has no story, the picker first asks
ktml dev (`POST /__ktml/picker/open`), and the Explorer writes one into the
component's `.story.ktml`: values synthesized from the input types only, as
`ktml stories generate` writes them, under `// ktml:generated`, keeping every
other story in the Document. The story is type-checked and rendered before it
is written and swapped into the program before the tab opens. No value from
the app's page reaches a story this way.

## Running it on an application

The Explorer serves the application's own build outputs, so a preview looks
like the page it will appear in. For an application that builds its
stylesheet with Tailwind v4 (for example `npx @tailwindcss/cli -i site.css -o
static/css/main.css`, with `@import "tailwindcss"` scanning the directories
that hold the `.ktml` and `.story.ktml` files, so classes written in stories
are generated too) and copies HTMX and Alpine.js into `static/js`, point the
Explorer at those outputs in `ktml.config.json`:

```json
"explorer": {
  "static": { "dir": "static", "url": "/static" },
  "stylesheets": ["/static/css/main.css"],
  "scripts": [
    "/static/js/htmx.min.js",
    "/static/js/alpine.min.js"
  ],
  "body_class": "bg-base text-text"
}
```

Then, from the application's root:

```sh
npx @tailwindcss/cli -i site.css -o static/css/main.css   # or run it with --watch
ktml explore -addr 127.0.0.1:7332
```

With the stylesheet build in watch mode, a story or component that adds a
class rebuilds `main.css`; reload the preview to pick up the new stylesheet.
This repository's `e2e-test/testdata/explorerapp` (a fictional bookshop's
helpdesk call audits, reader reviews, and ui components with stories) is
the Phase 5 gate fixture and a small working example.

## Stories to explore

`ktml stories generate` writes stories for every component the application's
routes reach, and `ktml dev --capture-stories <dir>` captures redacted stories
from pages as you browse; the Explorer lists both like hand-written ones. See
[Generated and captured stories](../language/stories-v1.md#generated-and-captured-stories).
For an application with Chi routes under `internal/web`, `go tool ktml
stories generate -chi internal/web ./internal/...` writes a story for each
reachable component, which the Explorer lists and groups by reachability
(pages, components, and other). On a large application with about a thousand
stories, the Explorer starts in well under a second, groups the stories
within a few seconds, and answers each search keystroke within a frame or
two; `TestExplorer_GIVENAHostApplicationsStories_*` measures this when
`KTML_HOST_DIR` names the application's root. To capture stories, the
application's ktmldev build declares its `-local` data (`hmr.LocalData`),
and its `ktml.config.json` adds `"capture": {"allow": true}` to the `dev`
section.

## Limits (v1)

Version one is typed controls only. Interaction tests, visual snapshots,
accessibility checks, viewport and theme controls, documentation pages, and
HTMX route mocks are later releases (PLAN). Also not in v1:

- Attribute bag inputs have no control; a story gives only the empty bag.
- HTMX requests from a preview are answered 204 and listed, never routed
  to a mock response; route mocks remain a later release.
- Control changes reload the preview frame (see Controls).
