# KTML for Neovim

This directory is a Neovim plugin (Neovim 0.11 or later). It adds:

- filetype detection for `.ktml` and `.ktmlschema`;
- the `ktml lsp` language server, started in the project root (the directory
  with `ktml.config.json`, `go.mod`, or `.git`);
- tree-sitter highlighting, injections, folds, indents, and locals from
  `tree-sitter-ktml/queries` (the `queries` link).

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
attaches. It is skipped when `nvim` is not installed.
