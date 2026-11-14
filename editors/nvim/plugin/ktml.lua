if vim.g.loaded_ktml then
  return
end
vim.g.loaded_ktml = true

vim.api.nvim_create_user_command("KtmlBuildParser", function()
  require("ktml").build_parser()
end, { desc = "Build the KTML tree-sitter parser into this plugin" })
