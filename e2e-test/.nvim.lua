vim.filetype.add({ extension = { ktml = "ktml" } })

local root = vim.fn.getcwd()

vim.api.nvim_create_autocmd("FileType", {
  pattern = { "ktml", "go" },
  callback = function(args)
    vim.lsp.start({
      name = "ktml",
      cmd = { "go", "run", "../cmd/ktml", "lsp" },
      root_dir = root,
    }, { bufnr = args.buf })
  end,
})

vim.api.nvim_create_user_command("KtmlDev", function()
  vim.cmd("botright split | terminal go run ../cmd/ktml dev")
end, { desc = "Start the KTML development server" })

vim.api.nvim_create_user_command("KtmlTest", function()
  vim.cmd("botright split | terminal go test -count=1 .")
end, { desc = "Run the KTML E2E fixture suite" })
