# Dev Server

The development experience subsystem: a reverse proxy HTTP server with live-reload SSE injection, file-system watching, and hot component reload. Provides the `ktml dev` experience.

## Language

**Dev Server**:
The `ktml dev` command experience. Starts a reverse proxy, a file watcher, and the user's application server. Injects livereload scripts into HTML responses. Coordinates the full development feedback loop: watch → generate → reload.
_Avoid_: Dev mode, development server, hot reload server

**File Watcher**:
A filesystem observer (via `fsnotify`) that recursively monitors the project directory. Categorizes each debounced batch as a reload event (`watch`: `.ktml`, `.ktmlschema`) or a restart event (`restart_watch`: `.go`, `.mod`, `.sum`), and reports `reload_watch` static assets (`.css`, images) apart. A restart batch still generates its KTML sources, before the rebuild. Debounces rapid changes to avoid redundant processing.
_Avoid_: File observer, change detector

**Watch Pattern**:
A glob pattern configured in `ktml.config.json` under `dev.watch`, `dev.restart_watch`, or `dev.reload_watch`. Files matching `watch` trigger a reload (re-generate + livereload). Files matching `restart_watch` trigger a full process restart of the user's application server. Files matching `reload_watch` are Static Assets.
_Avoid_: Watch glob, file filter

**Static Asset**:
A file matching `dev.reload_watch`, such as a stylesheet or image, that pages reload without generation or a rebuild. The SSE `assets` event names the changed files; the client swaps a changed stylesheet in place and reloads the page for anything else. A `reload_watch` match is never a restart.
_Avoid_: Static file reload, CSS injection

**Debounce**:
A configurable delay after a file change before the Watcher emits an event. Prevents rapid successive saves from triggering multiple generations and reloads. Configured via `dev.debounce` in Configuration.
_Avoid_: Coalesce, throttle

**Live Reload**:
The mechanism that pushes updates to the browser in real time via SSE. Two modes: Full Reload (hard page refresh) and Hot Component Reload (DOM patching of individual Components without page navigation).
_Avoid_: Auto-refresh, browser sync

**Full Reload**:
A complete browser page refresh triggered by `window.location.reload()` via an SSE `reload` event. Used when the change is too complex for Hot Component Reload (multiple components changed, structural AST changes, new files, or generation errors).
_Avoid_: Hard refresh, page reload, cold reload

**Hot Component Reload**:
Targeted DOM patching that morphs only the live Component instances a change affects, preserving all other page state (focus, form inputs, Alpine state, scroll position). Triggered when the change is compatible with the Component Signature. The app rerenders each affected Render Instance root and the browser matches it by its Render Identity (`data-ktml-i` or `<!--ktml:c …-->` markers; see ADR-0004).
_Avoid_: Hot reload, HMR, patch reload

**SSE (Server-Sent Events)**:
The transport for livereload events. The browser opens a long-lived HTTP connection to `/__ktml/livereload?session=…` for its page session. The server pushes `reload`, `patch`, `assets`, and the `diagnostic`/`clear` overlay events. A small inline `<script>` injected into every proxied HTML response sets up the EventSource listener.
_Avoid_: WebSocket (not used), event stream

**Reverse Proxy**:
An HTTP reverse proxy that sits between the browser and the user's application server. Intercepts HTML responses to inject the page's session token and the livereload SSE script. When the page's Content-Security-Policy headers restrict scripts, it allows the injected scripts with a per-response nonce (a policy in a `<meta>` tag cannot be amended and gets a warning). It never sees component props.
_Avoid_: Dev proxy, middleware

**Render Registry**:
The development-only registry inside the app process (`ktml/dev/hmr`, ADR-0004) that keeps each root Render Instance's typed props server-side, per page session, bounded by TTL, count, and size. It replaced the browser-visible `__ktml_cache` templates, which are removed.
_Avoid_: Component cache, instance manifest

**Process Runner**:
Manages the lifecycle of the user's application server as a child process. Starts the server, restarts it on relevant file changes (via SIGTERM/SIGKILL), and ensures clean shutdown on `ktml dev` exit. Uses process groups for signal propagation.
_Avoid_: App runner, server manager, process manager

**Component Explorer**:
A development UI over the project's `.story.ktml` stories, served by `ktml explore` or beside `ktml dev` through `dev.explorer`, on a loopback address only (see [docs/explorer/explorer-v1.md](../../explorer/explorer-v1.md)). The dev proxy serves no component browser of its own: the legacy `/ktml/elements` routes were removed (2026-10-15).
_Avoid_: Component browser, element inspector, storybook

**Dev Profile**:
A named variant of the `dev` section (`dev.profiles.<name>`, run with `ktml dev --profile <name>`) that overrides its command, build, env file and env, ports, proxy target, Explorer address, and ready timeout. `--port`, `--proxy-target`, and `--explorer-addr` override those keys again over the profile, with the same validation, and `{target_port}` in the command, build, or env values follows the resolved proxy target's port. `--dry-run` prints the resolved run, with variable names but never values. A profile with an `env_file` may point the app at production services.
_Avoid_: Environment, mode

**Component Picker**:
The dev-only part of the injected client (only with `dev.explorer`): a hotkey toggles pick mode, the hovered element is highlighted and labelled with its component chain and its Element Path, Shift+wheel walks out element by element and then component by component, and a click opens the innermost component's story in the Explorer, creating a type-derived one when it has none. Ctrl+click (Cmd+click) opens the selection's Element Source in `dev.picker.editor`, or copies it. It sends Render Identities, component names, and element tags, ids, and classes, never props or text.
_Avoid_: Inspector, element picker

**Element Path**:
The elements from the top of the innermost component instance (its `data-ktml-i` element, a top-level element of its `ktml:c` range, or `html` for a document component) down to the hovered element, each as its tag, id, and classes, such as `div.timeline-row › span.badge`.
_Avoid_: Selector, DOM path

**Element Source**:
The `.ktml` declaration (`file:line:column`) of a picked element, found by Element Location.
_Avoid_: Source map

**Element Location**:
How ktml dev finds an Element Source without adding anything to rendered output (see the design note below).
_Avoid_: Source mapping

## Design note: element picking (2026-11-16)

The picker highlighted only component instances, because only instances carry
markers (ADR-0004). Picking an element inside a component, and knowing where
it is declared, needs a way from a DOM element back to a `.ktml` element node.

- **What the dev build knows.** The page carries Render Identities on
  instances only. ktml dev holds the Explorer's interpreter program, whose
  snapshot has every Document's AST with byte positions, the `c<n>` site of
  every call node, and the `o<n>` site of every outlet. Render Identities name,
  frame by frame, which component's source was rendering, so the chain of
  calls that supplied each outlet's content can be recovered from the
  identity alone.
- **Decision: structural matching in ktml dev, no new markers.** The client
  sends `POST /__ktml/picker/locate` with the innermost instance's identity
  and the Element Path. ktml dev matches the path against that component's
  bodies: markup elements by tag, with each static `id` and every static
  class token required on the page element (dynamic values match anything);
  `if`/`else` branches and `for` bodies are transparent; calls are opaque,
  since their output is a nested instance; an outlet continues the match in
  the content its caller supplied (the call's children or slot body), found
  by the instance identity's call frame, and so on outward. The highest
  scoring match wins (an id match counts most, then each class), and the
  first in source order breaks a tie. Every element of the path gets the
  source of its match, or none.
- **Rejected: a dev-only source attribute on every element.** It would change
  the `ktmldev` bytes of every element, the parity harness, morphing, and the
  HTMX fragments, for a feature only the picker uses. Production bytes are
  unchanged either way; the chosen approach changes no rendered bytes at all.
- **Limits.** Elements that browsers or scripts create (an implicit `tbody`
  is skipped; Alpine `x-for` output and HTMX swaps of non-KTML markup are not
  in any Document) have no source. Two identical siblings from different
  branches resolve to the first. Content built in Go has no KTML source.
- **Component values at outlets.** A component value built in Go and
  rendered at a content outlet (a page passed into a layout's slot) is marked
  with the outlet's scope, an identity ending in `<component>#o<n>`, which the
  program cannot name. The app's dev hook records the component it rendered
  at each such scope with the root (`hmr.RenderInstance.Outlets`, component
  IDs only), `POST /__ktml/dev/components` names them, and the picker
  otherwise takes the next frame's component when the outlet's own instance
  was not rendered by a KTML call (its content is then Go's). The value is
  then a level of the chain with its own box, and locating stops at it,
  since its own outlets hold Go content.
- **Click.** A plain click opens the innermost component's story, as before,
  whatever level is selected below it; on a component level it opens that
  component. Ctrl+click (Cmd+click) opens the selected level's Element
  Source (a component's declaration for a component level) in
  `dev.picker.editor`, an argument list with `{file}`, `{line}`, and
  `{column}`, such as `["code", "-g", "{file}:{line}:{column}"]`; without an
  editor it copies `file:line:column`, as `C` does in pick mode. `$EDITOR`
  is not used: it is usually a terminal editor, which ktml dev has no
  terminal to give.
- **Editor safety.** `POST /__ktml/picker/edit` takes the same identity and
  Element Path (or a component), never a file name: ktml dev locates the
  source again from its own program, checks that the file is a `.ktml`
  Document inside the project, and runs the configured command directly
  (no shell). The route is behind the picker's same-origin, loopback-Host,
  `application/json` POST check, and the page cannot choose the command or
  the file.

**Component Patching**:
The server-side half of Hot Component Reload. On a compatible change the Dev Server sends the validated sources to the app's token-guarded `/__ktml/dev/swap`; the app swaps its interpreter program and returns the rerendered HTML of every affected root. The Dev Server sends each as an SSE `patch` event to that root's page session. Any failed or ambiguous patch rebuilds and reloads.
_Avoid_: DOM patching (client-side), OOB swap
