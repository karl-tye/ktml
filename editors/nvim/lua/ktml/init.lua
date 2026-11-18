-- KTML for Neovim (0.11 or later): filetype detection, the ktml language
-- server, and the tree-sitter parser and queries of tree-sitter-ktml.
--
--   require("ktml").setup()
--
-- The server is the editor-independent `ktml lsp`; this module only starts
-- it. It also registers this Neovim so that the component picker of
-- `ktml dev` opens Ctrl+clicked sources in it. See editors/nvim/README.md.
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
  -- Register this Neovim's server and working directory so that the
  -- picker of `ktml dev` (with EDITOR=nvim) opens sources in it.
  register = true,
  -- Called after the picker opened a source, with { file, line, column,
  -- buf, win }; for example to raise the terminal window.
  on_open = nil,
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

-- The registry is a per-user runtime directory with one file per running
-- Neovim, <pid>.json, holding its v:servername and global working
-- directory. ktml dev reads it to find the instance whose working
-- directory holds a picked source. It is never inside a project.
local uv = vim.uv or vim.loop
local registered = nil

local function registry_dir()
  local runtime = vim.env.XDG_RUNTIME_DIR
  if runtime and runtime ~= "" then
    return runtime .. "/ktml", runtime .. "/ktml/nvim"
  end
  local temp = vim.env.TMPDIR
  if not temp or temp == "" then
    temp = "/tmp"
  end
  local base = temp:gsub("/+$", "") .. "/ktml-" .. uv.getuid()
  return base, base .. "/nvim"
end

-- private_dir creates a directory with mode 0700 and reports whether it is
-- ours and nobody else can write it.
local function private_dir(path)
  uv.fs_mkdir(path, tonumber("700", 8))
  local stat = uv.fs_lstat(path)
  if not stat or stat.type ~= "directory" or stat.uid ~= uv.getuid() then
    return false
  end
  if bit.band(stat.mode, tonumber("022", 8)) ~= 0 then
    uv.fs_chmod(path, tonumber("700", 8))
  end
  return true
end

local function alive(pid)
  local ok = uv.kill(pid, 0)
  return ok == 0 or ok == true
end

-- prune removes the files of instances that have exited or whose server
-- is gone.
local function prune(dir)
  local handle = uv.fs_scandir(dir)
  if not handle then
    return
  end
  while true do
    local name = uv.fs_scandir_next(handle)
    if not name then
      break
    end
    local pid = tonumber(name:match("^(%d+)%.json$"))
    if pid and pid ~= vim.fn.getpid() then
      local path = dir .. "/" .. name
      local ok, entry = pcall(function()
        return vim.json.decode(table.concat(vim.fn.readfile(path), "\n"))
      end)
      local server = ok and type(entry) == "table" and entry.server or nil
      if not alive(pid) or type(server) ~= "string" or (server:find("/", 1, true) and not uv.fs_stat(server)) then
        os.remove(path)
      end
    end
  end
end

-- register writes this Neovim's registry file.
function M.register()
  local server = vim.v.servername
  if not server or server == "" then
    return false
  end
  local base, dir = registry_dir()
  if not private_dir(base) or not private_dir(dir) then
    return false
  end
  prune(dir)
  local pid = vim.fn.getpid()
  local data = vim.json.encode({ pid = pid, server = server, cwd = vim.fn.getcwd(-1, -1) })
  local temp = dir .. "/." .. pid .. ".tmp"
  os.remove(temp)
  local fd = uv.fs_open(temp, "wx", tonumber("600", 8))
  if not fd then
    return false
  end
  uv.fs_write(fd, data)
  uv.fs_close(fd)
  registered = dir .. "/" .. pid .. ".json"
  return uv.fs_rename(temp, registered) and true or false
end

-- unregister removes this Neovim's registry file.
function M.unregister()
  if registered then
    os.remove(registered)
    registered = nil
  end
end

local function start_registry()
  local group = vim.api.nvim_create_augroup("ktml_register", { clear = true })
  if not M.config.register then
    M.unregister()
    return
  end
  M.register()
  vim.api.nvim_create_autocmd("DirChanged", { group = group, callback = function() M.register() end })
  vim.api.nvim_create_autocmd("VimLeavePre", { group = group, callback = M.unregister })
end

-- usable reports whether a window can show a file: not floating, and not
-- a terminal, help, quickfix, or other special buffer.
local function usable(win)
  return vim.api.nvim_win_get_config(win).relative == "" and vim.bo[vim.api.nvim_win_get_buf(win)].buftype == ""
end

local function open(file, line, column)
  if type(file) ~= "string" or file:sub(1, 1) ~= "/" or type(line) ~= "number" or type(column) ~= "number" then
    error("open_location takes an absolute path, a line, and a column")
  end
  vim.cmd.stopinsert()
  -- bufadd takes the name as it is: nothing in it is a command.
  local buf = vim.fn.bufadd(file)
  local win = nil
  for _, candidate in ipairs(vim.api.nvim_tabpage_list_wins(0)) do
    if vim.api.nvim_win_get_buf(candidate) == buf then
      win = candidate
      break
    end
  end
  if not win then
    for _, candidate in ipairs(vim.api.nvim_list_wins()) do
      if vim.api.nvim_win_get_buf(candidate) == buf then
        win = candidate
        break
      end
    end
  end
  if win then
    vim.api.nvim_set_current_win(win)
  else
    if not usable(vim.api.nvim_get_current_win()) then
      for _, candidate in ipairs(vim.api.nvim_tabpage_list_wins(0)) do
        if usable(candidate) then
          vim.api.nvim_set_current_win(candidate)
          break
        end
      end
    end
    if usable(vim.api.nvim_get_current_win()) then
      -- A modified buffer that cannot be hidden keeps its window.
      if not pcall(vim.cmd.buffer, buf) then
        vim.cmd.sbuffer(buf)
      end
    else
      vim.cmd.sbuffer(buf)
    end
    win = vim.api.nvim_get_current_win()
  end
  local count = vim.api.nvim_buf_line_count(buf)
  line = math.max(1, math.min(math.floor(line), count))
  local text = vim.api.nvim_buf_get_lines(buf, line - 1, line, false)[1] or ""
  -- The picker's column counts UTF-16 code units.
  local ok, byte = pcall(vim.str_byteindex, text, "utf-16", math.max(0, math.floor(column) - 1), false)
  if not ok or type(byte) ~= "number" then
    byte = math.floor(column) - 1
  end
  vim.api.nvim_win_set_cursor(win, { line, math.max(0, math.min(byte, #text)) })
  vim.cmd("normal! zv")
  if M.config.on_open then
    local info = { file = file, line = line, column = column, buf = buf, win = win }
    vim.schedule(function()
      local hook_ok, err = pcall(M.config.on_open, info)
      if not hook_ok then
        vim.notify("ktml on_open: " .. tostring(err), vim.log.levels.WARN)
      end
    end)
  end
end

-- open_location opens file at line and UTF-16 column, for the picker of
-- `ktml dev`: in a window that already shows it, else in the current
-- window. It returns "opened", or the error.
function M.open_location(file, line, column)
  local ok, err = pcall(open, file, line, column)
  if not ok then
    return "error: " .. tostring(err)
  end
  return "opened"
end

function M.setup(opts)
  M.config = vim.tbl_deep_extend("force", vim.deepcopy(defaults), opts or {})
  start_registry()
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
