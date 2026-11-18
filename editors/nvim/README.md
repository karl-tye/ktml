# KTML for Neovim

This directory is a Neovim plugin (Neovim 0.11 or later). It adds:

- filetype detection for `.ktml` and `.ktmlschema`;
- the `ktml lsp` language server, started in the project root (the directory
  with `ktml.config.json`, `go.mod`, or `.git`);
- tree-sitter highlighting, injections, folds, indents, and locals from
  `tree-sitter-ktml/queries` (the `queries` link);
- opening sources from the `ktml dev` component picker: with `EDITOR=nvim`,
  Ctrl+click on a page opens the picked element's `.ktml` source in the
  Neovim you are already running (see below).

The server is editor-independent; this plugin only starts it.

## Install

1. Install the `ktml` command and build the language-server plugins your
   `ktml.config.json` names:

   ```sh
   go install ./cmd/ktml
   for p in gohtml alpine htmx; do
     GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o plugins/$p.wasm ./cmd/plugins/$p
   done
   ```

2. Add the plugin. With lazy.nvim:

   ```lua
   {
     dir = "~/dev/ktml/editors/nvim",
     config = function()
       require("ktml").setup()
     end,
   }
   ```

   Without a plugin manager:

   ```lua
   vim.opt.rtp:append(vim.fn.expand("~/dev/ktml/editors/nvim"))
   require("ktml").setup()
   ```

3. Build the parser once, and again after the grammar changes:

   ```vim
   :KtmlBuildParser
   ```

   It writes `editors/nvim/parser/ktml.so` with the `tree-sitter` CLI, or
   with `cc` when the CLI is missing. Neovim uses the first
   `parser/ktml.so` on the runtimepath, so remove an older one (for example
   in `~/.local/share/nvim/site/parser`) or pass `parser = "/path/ktml.so"`.

Highlighting of injected JavaScript (`script`, Alpine `x-*`, `@event`, and
`:binding` values), CSS (`style`), and JSON (`script type="application/json"`,
`hx-vals`, `hx-headers`) needs those parsers, for example from
nvim-treesitter. Indentation uses nvim-treesitter's indent module when it is
installed; Neovim itself has none.

If you configured `ktml lsp` yourself (for example as `ktml_lsp` with
nvim-lspconfig), remove that configuration, or pass `lsp = false`, so the
server does not start twice.

## Options

```lua
require("ktml").setup({
  cmd = { "ktml", "lsp" },                            -- server command
  root_markers = { "ktml.config.json", "go.mod", ".git" },
  lsp = true,                                         -- start the server
  treesitter = true,                                  -- highlighting and injections
  folds = true,                                       -- tree-sitter folds
  parser = nil,                                       -- path to ktml.so
  register = true,                                    -- let ktml dev's picker open files here
  on_open = nil,                                      -- function(info) after the picker opened a file
})
```

## Opening sources from the picker

When `dev.picker.editor` is not set, `ktml dev` opens a Ctrl+clicked
element's source with `$VISUAL` or `$EDITOR`. For `nvim` it opens the file
in a running Neovim rather than a new one, so load this plugin at startup
(not lazily, for example `lazy = false` with lazy.nvim) and call `setup()`.

How it works:

1. On startup, and whenever the working directory changes, the plugin
   writes `<pid>.json` with this Neovim's `v:servername` and global working
   directory into `$XDG_RUNTIME_DIR/ktml/nvim/` (without
   `$XDG_RUNTIME_DIR`, `ktml-<uid>/nvim/` in `$TMPDIR` or `/tmp`). The
   directory has mode 0700 and the file 0600; nothing is written into your
   project. The file is removed when Neovim exits, and files left by
   instances that crashed are removed by the next one that starts.
2. On Ctrl+click, `ktml dev` picks `$NVIM` first, when it runs inside a
   Neovim terminal, then the registered instance whose working directory
   most closely holds the file, and calls
   `require("ktml").open_location(file, line, column)` there through
   `nvim --server <address> --remote-expr`. The path is passed as data
   (hex digits), never pasted into a command.
3. `open_location` jumps to the window that already shows the file, or
   opens it in the current window (in a split when the current buffer is
   modified and cannot be hidden, and never in a terminal or floating
   window), and puts the cursor on the line and column.
4. With no registered Neovim, `ktml dev` runs `nvim` in a new terminal
   window (`xdg-terminal-exec`, else `$TERMINAL -e`), or copies
   `file:line:column` when it finds no terminal.

Turn registration off with `register = false`; `ktml dev` then always
starts `nvim` in a new terminal. `require("ktml").register()` and
`require("ktml").unregister()` do it by hand.

Neovim cannot raise its own terminal window. `on_open` runs after the file
opens, with `{ file, line, column, buf, win }`, so a window manager can do
it. With Hyprland, focus the window of the terminal this Neovim runs in:

```lua
require("ktml").setup({
  on_open = function()
    local windows = {}
    for _, client in ipairs(vim.json.decode(vim.fn.system({ "hyprctl", "clients", "-j" }))) do
      windows[client.pid] = client.address
    end
    -- Walk up from this Neovim to the terminal process that owns a window.
    local pid = vim.fn.getpid()
    while pid and pid > 1 do
      if windows[pid] then
        vim.system({ "hyprctl", "dispatch", "focuswindow", "address:" .. windows[pid] })
        return
      end
      local stat = io.open("/proc/" .. pid .. "/stat")
      if not stat then
        return
      end
      pid = tonumber(stat:read("*a"):match("^%d+ %b() %S+ (%d+)"))
      stat:close()
    end
  end,
})
```

## Tailwind CSS

Class completion, hover, conflicts, and theme navigation come from the
server when `ktml.config.json` has a `tailwind` section and the Tailwind CSS
language server is installed:

```sh
npm install -g @tailwindcss/language-server@0.16.0
```

See [docs/lsp/tailwind.md](../../docs/lsp/tailwind.md).

## gopls

`ktml lsp` runs gopls for Go errors on KTML lines, as a client of the
shared gopls daemon (`gopls -remote=auto`). Start Neovim's own gopls the
same way, from the same gopls build, so both share one gopls process:

```lua
vim.lsp.config("gopls", { cmd = { "gopls", "-remote=auto" } })
vim.lsp.enable("gopls")
```

`gopls -remote=auto remote sessions` lists the daemon's clients. See
[docs/lsp/gopls.md](../../docs/lsp/gopls.md#sharing-the-daemon-with-neovim).

## Test

`e2e-test/nvim_test.go` runs this plugin in a clean headless Neovim. It
checks the parser, highlights, injections, folds, locals, and that `ktml lsp`
attaches. `internal/dev/picker/nvim_integration_test.go` starts a headless
Neovim with this plugin on a temporary socket and runtime directory and
checks registration and `open_location`. Both are skipped when `nvim` is
not installed.
