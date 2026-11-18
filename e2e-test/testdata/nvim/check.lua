-- Headless check of the KTML Neovim plugin, run by TestNeovim_* with
-- nvim --clean. It writes its findings as JSON to $KTML_OUT.
local function check()
  local out = {}
  vim.opt.rtp:prepend(vim.env.KTML_NVIM)
  require("ktml").setup({ cmd = { vim.env.KTML_BIN, "lsp" }, parser = vim.env.KTML_PARSER, register = false })
  vim.cmd.edit(vim.env.KTML_FILE)
  local buf = vim.api.nvim_get_current_buf()
  out.filetype = vim.bo[buf].filetype

  local ok, parser = pcall(vim.treesitter.get_parser, buf, "ktml")
  out.parser = ok
  if ok then
    local tree = parser:parse(true)[1]
    out.has_error = tree:root():has_error()
    out.highlighter = vim.treesitter.highlighter.active[buf] ~= nil
    local function captures(row, col)
      local names = {}
      for _, capture in ipairs(vim.treesitter.get_captures_at_pos(buf, row, col)) do
        if capture.lang == "ktml" then
          table.insert(names, capture.capture)
        end
      end
      return names
    end
    out.import = captures(0, 1)
    out.element = captures(3, 2)
    out.condition = captures(3, 85)

    out.injections = {}
    local injections = vim.treesitter.query.get("ktml", "injections")
    for _, _, metadata in injections:iter_matches(tree:root(), buf, 0, -1) do
      local language = metadata["injection.language"]
      if language then
        out.injections[language] = (out.injections[language] or 0) + 1
      end
    end

    out.locals = {}
    local locals = vim.treesitter.query.get("ktml", "locals")
    for id, node in locals:iter_captures(tree:root(), buf, 0, -1) do
      local name = locals.captures[id]
      if name == "local.definition.var" or name == "local.definition.parameter" then
        table.insert(out.locals, name .. ":" .. vim.treesitter.get_node_text(node, buf))
      end
    end

    out.queries = {}
    for _, name in ipairs({ "highlights", "injections", "folds", "indents", "locals" }) do
      out.queries[name] = vim.treesitter.query.get("ktml", name) ~= nil
    end
    out.fold = vim.treesitter.foldexpr(5)
    out.foldmethod = vim.wo.foldmethod
  end

  out.lsp_attached = vim.wait(30000, function()
    local client = vim.lsp.get_clients({ bufnr = buf, name = "ktml" })[1]
    return client ~= nil and client.initialized
  end, 100)
  if out.lsp_attached then
    local client = vim.lsp.get_clients({ bufnr = buf, name = "ktml" })[1]
    out.lsp_root = client.root_dir
    local response = client:request_sync("textDocument/documentSymbol", { textDocument = vim.lsp.util.make_text_document_params(buf) }, 15000, buf)
    out.symbols = {}
    for _, symbol in ipairs(response and response.result or {}) do
      table.insert(out.symbols, symbol.name)
    end
  end
  return out
end

local ok, result = xpcall(check, debug.traceback)
if not ok then
  result = { error = tostring(result) }
end
vim.fn.writefile({ vim.json.encode(result) }, vim.env.KTML_OUT)
vim.cmd("qa!")
