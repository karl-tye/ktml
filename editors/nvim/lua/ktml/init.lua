-- KTML for Neovim (0.11 or later): filetype detection, the ktml language
-- server, and the tree-sitter parser and queries of tree-sitter-ktml.
--
--   require("ktml").setup()
--
-- The server is the editor-independent `ktml lsp`; this module only starts
-- it. See editors/nvim/README.md.
local M = {}

local defaults = {
  -- The language server command. It runs in the project root so that it
  -- finds ktml.config.json.
  cmd = { "ktml", "lsp" },
  root_markers = { "ktml.config.json", "go.mod", ".git" },
  -- Start the server for KTML buffers.
  lsp = true,
  -- Use the tree-sitter parser for highlighting, injections, and folds
  -- when it is installed (see :KtmlBuildParser).
  treesitter = true,
  folds = true,
  -- The parser library; by default the first parser/ktml.so on the
  -- runtimepath.
  parser = nil,
}

M.config = vim.deepcopy(defaults)

-- plugin_root is editors/nvim; the repository root is two levels up.
local function plugin_root()
  local source = debug.getinfo(1, "S").source:sub(2)
  return vim.fn.fnamemodify(source, ":p:h:h:h")
end

local function parser_path()
  if M.config.parser then
    return M.config.parser
  end
  local found = vim.api.nvim_get_runtime_file("parser/ktml.so", false)
  return found[1]
end

-- register_parser loads the ktml parser and reports whether it is
-- available.
function M.register_parser()
  local path = parser_path()
  if not path then
    return false
  end
  local ok = pcall(vim.treesitter.language.add, "ktml", { path = path })
  return ok
end

local function attach_treesitter(buf)
  if not M.register_parser() then
    return
  end
  if not pcall(vim.treesitter.start, buf, "ktml") then
    return
  end
  if M.config.folds then
    for _, win in ipairs(vim.fn.win_findbuf(buf)) do
      vim.wo[win][0].foldmethod = "expr"
      vim.wo[win][0].foldexpr = "v:lua.vim.treesitter.foldexpr()"
      vim.wo[win][0].foldlevel = 99
    end
  end
  -- Neovim has no tree-sitter indentation of its own; nvim-treesitter's
  -- reads the indents query.
  local has_indent, indent = pcall(require, "nvim-treesitter.indent")
  if has_indent and indent.get_indent then
    vim.bo[buf].indentexpr = "v:lua.require'nvim-treesitter.indent'.get_indent(v:lnum)"
  end
end

function M.setup(opts)
  M.config = vim.tbl_deep_extend("force", vim.deepcopy(defaults), opts or {})
  vim.filetype.add({ extension = { ktml = "ktml", ktmlschema = "ktml" } })

  if M.config.lsp then
    local command = M.config.cmd
    vim.lsp.config("ktml", {
      -- A function command starts the server in the root directory.
      cmd = function(dispatchers, config)
        return vim.lsp.rpc.start(command, dispatchers, { cwd = config.cmd_cwd or config.root_dir })
      end,
      filetypes = { "ktml" },
      root_markers = M.config.root_markers,
    })
    vim.lsp.enable("ktml")
  end

  if M.config.treesitter then
    local group = vim.api.nvim_create_augroup("ktml_treesitter", { clear = true })
    vim.api.nvim_create_autocmd("FileType", {
      group = group,
      pattern = "ktml",
      callback = function(args)
        attach_treesitter(args.buf)
      end,
    })
    for _, buf in ipairs(vim.api.nvim_list_bufs()) do
      if vim.api.nvim_buf_is_loaded(buf) and vim.bo[buf].filetype == "ktml" then
        attach_treesitter(buf)
      end
    end
  end
end

-- build_parser compiles tree-sitter-ktml into parser/ktml.so of this
-- plugin, with the tree-sitter CLI or a C compiler.
function M.build_parser()
  local root = plugin_root()
  local grammar = vim.fn.fnamemodify(root .. "/../../tree-sitter-ktml", ":p")
  local output = root .. "/parser/ktml.so"
  vim.fn.mkdir(root .. "/parser", "p")
  local command
  if vim.fn.executable("tree-sitter") == 1 then
    command = { "tree-sitter", "build", "-o", output, grammar }
  else
    local cc = vim.env.CC or "cc"
    command = { cc, "-shared", "-fPIC", "-O2", "-I", grammar .. "src", grammar .. "src/parser.c", grammar .. "src/scanner.c", "-o", output }
  end
  local result = vim.system(command, { text = true }):wait()
  if result.code ~= 0 then
    vim.notify("KTML parser build failed:\n" .. (result.stderr or ""), vim.log.levels.ERROR)
    return false
  end
  vim.notify("Built " .. output)
  return true
end

return M
