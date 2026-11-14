# Dev Server

The development experience subsystem: a reverse proxy HTTP server with live-reload SSE injection, file-system watching, and hot component reload. Provides the `ktml dev` experience.

## Language

**Dev Server**:
The `ktml dev` command experience. Starts a reverse proxy, a file watcher, and the user's application server. Injects livereload scripts into HTML responses. Coordinates the full development feedback loop: watch → generate → reload.
_Avoid_: Dev mode, development server, hot reload server

**File Watcher**:
A filesystem observer (via `fsnotify`) that recursively monitors the project directory. Categorizes changes as reload events (`.ktml`, `.css`, `.js`) or restart events (`.go`, `.mod`, `.sum`). Debounces rapid changes to avoid redundant processing.
_Avoid_: File observer, change detector

**Watch Pattern**:
A glob pattern configured in `ktml.config.json` under `dev.watch` or `dev.restart_watch`. Files matching `watch` trigger a reload (re-generate + livereload). Files matching `restart_watch` trigger a full process restart of the user's application server.
_Avoid_: Watch glob, file filter

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
The transport for livereload events. The browser opens a long-lived HTTP connection to `/__ktml/livereload?session=…` for its page session. The server pushes `reload`, `patch`, and the `diagnostic`/`clear` overlay events. A small inline `<script>` injected into every proxied HTML response sets up the EventSource listener.
_Avoid_: WebSocket (not used), event stream

**Reverse Proxy**:
An HTTP reverse proxy that sits between the browser and the user's application server. Intercepts HTML responses to inject the page's session token and the livereload SSE script. It never sees component props.
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
A named variant of the `dev` section (`dev.profiles.<name>`, run with `ktml dev --profile <name>`) that overrides its command, build, env file and env, ports, proxy target, Explorer address, and ready timeout. `--dry-run` prints the resolved run, with variable names but never values. A profile with an `env_file` may point the app at production services.
_Avoid_: Environment, mode

**Component Picker**:
The dev-only part of the injected client (only with `dev.explorer`): a hotkey toggles pick mode, the hovered component instance and its chain are highlighted and labelled, Shift+wheel walks the chain, and a click opens the component's story in the Explorer, creating a type-derived one when it has none. It sends Render Identities and component names, never props.
_Avoid_: Inspector, element picker

**Component Patching**:
The server-side half of Hot Component Reload. On a compatible change the Dev Server sends the validated sources to the app's token-guarded `/__ktml/dev/swap`; the app swaps its interpreter program and returns the rerendered HTML of every affected root. The Dev Server sends each as an SSE `patch` event to that root's page session. Any failed or ambiguous patch rebuilds and reloads.
_Avoid_: DOM patching (client-side), OOB swap
