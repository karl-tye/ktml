# Tailwind CSS

KTML's Tailwind support has two parts that share one class extractor
(`internal/tailwind`):

- `ktml lsp` runs the Tailwind CSS language server behind a native adapter
  (PLAN "Federated LSP and IDE Support" → Tailwind).
- `ktml classes` lists the same classes for production builds and the
  Explorer.

Both support Tailwind CSS v4 first, the current major version: `tailwindcss` and
`@tailwindcss/cli` ^4.3, with a CSS-first `site.css`
(`@import "tailwindcss" source("./internal/")`, `@theme`, `@utility`). v3
projects work through the same language server, which reads
`tailwind.config.js`, but are not tested.

## The class extractor

`tailwind.Extract` parses a KTML source and returns its class tokens with
byte ranges:

| Where | Example | Token flags |
| --- | --- | --- |
| a `class` attribute | `div class="flex p-4"` | |
| a conditional attribute group | `div (class="a" if on { class="b" } else { class="c" })` | `Conditional` |
| an attribute bag | `Field attrs=(class="q")` | `Bag` |
| string literals of an Alpine class binding | ``:class="open && 'rotate-180'"``, `x-bind:class` | `Script` |

A source that does not parse yields whatever the parser recovers, so an
editor keeps its classes while a line is broken.

Interpolations in class lists are reported as **dynamic class construction**
(`tailwind-dynamic-class`):

- **Built from parts**, such as `text-{color}-500`, is a warning. No scanner
  can complete the class, so Tailwind never generates it. Write whole class
  names, for example in the presenter, or list them with
  `@source inline(...)`.
- **Whole-token**, such as `class="mt-2 {view.badgeClass}"` or
  `:class="{tab.classKey}"`, is information. Tailwind finds those classes only
  where the presenter writes them whole, for example as string literals in
  Go files under a `@source` path.

In an Alpine binding, interpolations outside string literals are JavaScript
operands (`turnSelected({view.start})`), not classes. The exception is a
binding that is a single interpolation.

## Language server

Add a `tailwind` section to `ktml.config.json` and install the pinned server:

```json
{
  "tailwind": {
    "command": ["tailwindcss-language-server", "--stdio"],
    "settings": { "lint": { "cssConflict": "warning" } }
  }
}
```

```sh
npm install -g @tailwindcss/language-server@0.16.0
# or, for this repository's tests:
scripts/install-tailwind-ls.sh
```

`command` defaults to `tailwindcss-language-server --stdio`. `settings` are
the server's `tailwindCSS` settings; KTML adds
`includeLanguages: {"ktml": "html"}`. The server starts with the first
`.ktml` document, in the directory where `ktml lsp` runs. It finds the
project's CSS entry itself. Without a local `tailwindcss`, it uses its
bundled v4 (4.1.18 in 0.16.0), which reads a v4 `site.css` theme.

### Virtual documents

The adapter sends the server a **virtual document** for each open KTML file,
under the KTML file's own URI, as HTML. It keeps the class attributes
(`class="…"`, `:class="…"`) and blanks every other character, including
interpolations, to spaces. Each blanked character takes as many spaces as its
UTF-16 length, and line breaks are kept, so every LSP position is the same in
both documents. Requests and results therefore need no position mapping; the
adapter only keeps them inside class lists.

### Features

| Feature | Behaviour |
| --- | --- |
| Completion | Inside a class list only. The server lists every class of the project (megabytes), so the adapter keeps the items that match the word being typed, at most 300 of them, and marks a cut list incomplete so the editor asks again. VS Code commands are removed. |
| Hover | Inside a class list: the class's CSS. |
| Diagnostics | The server's (`source: tailwindcss`, such as `cssConflict`) inside class lists, merged per URI with KTML's `tailwind-dynamic-class` and the other analyzers' diagnostics. |
| Code actions | The server's quick fixes whose edits stay inside class lists. |
| Definition | From a class to its theme. The adapter reads the custom properties that the class's CSS uses, from the server's hover, ignoring comments. It returns their declarations in the project's Tailwind stylesheets (CSS files under the root with `@import "tailwindcss"`, `@theme`, `@utility`, or `@tailwind`, plus their relative imports, plus `node_modules/tailwindcss/theme.css` when present). It also returns a custom `@utility` of the class's name. `bg-surface0/30` goes to `--color-surface0` in `site.css`. |
| Workspace symbols | Class tokens named like selectors (`.bg-blue`), for a non-empty query that their class contains. They come from open documents and every `.ktml` file under the root. Files are re-extracted only when their size or modification time changes. |

Completion inside a quoted attribute value no longer falls through to KTML
component names, so class lists show only classes.

### Without the server

If the command is not found, `ktml lsp` publishes a `tailwind-server` error on
`tailwind` (or `tailwind.command`) in `ktml.config.json`. If the server
cannot start or exits (for example, Node.js is missing), the adapter logs to
stderr and sends one `window/showMessage` warning with the install command.
Extraction, `tailwind-dynamic-class`, `@utility` definitions, and class
symbols keep working; nothing crashes.

### Limits

- Completion items are not resolved (`completionItem/resolve`), so they have
  no CSS documentation; hover shows it. Color decorators
  (`textDocument/documentColor`) are not exposed.
- Theme navigation needs the server for variables (hover), and only finds
  declarations in project CSS or an installed `tailwindcss` package. The
  bundled default theme (`--color-red-500`, …) has no file to go to.
- Classes written in Go presenters are not checked; they are reported only
  as whole-token dynamic construction where KTML reads them.
- `:class` object keys must be quoted strings to be read
  (`{ 'font-bold': on }`).
- Changing the `tailwind` section takes effect after a restart (the config
  diagnostic says so).

## Production builds: `ktml classes`

```sh
ktml classes [-o file] [-json] [-check] [paths...]   # paths default to "."
```

- Without flags, it prints each class once, sorted, from every `.ktml` file
  under the paths. Hidden, `vendor`, `testdata`, and `node_modules`
  directories are skipped.
- `-o file` writes the list atomically, and only when it changed, so
  Tailwind's watcher does not rebuild for nothing.
- `-check` prints classes built from parts as
  `file:line:column: [tailwind-dynamic-class] …` and exits 1 when there are
  any. It is meant for CI.
- `-json` prints every token (class, byte range, line, column, and the
  conditional, bag, and script flags) and every dynamic construction per
  file, for the Explorer.

Tailwind v4 reads the list with `@source`:

```css
@import "tailwindcss" source("./internal/");
@source "./static/ktml-classes.txt";
```

```sh
ktml classes -o static/ktml-classes.txt ./internal
```

Tailwind v4 also scans `.ktml` files under `source(...)` as plain text, which
already finds a project's static classes. The list adds what plain-text scanning
cannot know, and `-check` catches classes built from parts before they reach
production. Verified with `@tailwindcss/cli` 4.1: a build from
`@import "tailwindcss" source(none); @source "./ktml-classes.txt";`
generates the listed classes. For v3, add the file to `content`.

Over a converted production application's KTML (`internal/`), `ktml classes`
finds 392 distinct classes and 16 whole-token presenter classes, and no
classes built from parts. Its earlier prototypes had 12 classes built from parts
(`bg-{color}/10`, `text-{color}`, `border-{color}/20`).
