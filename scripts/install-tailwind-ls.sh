#!/bin/sh
# Installs the pinned Tailwind CSS language server, which the ktml lsp
# Tailwind adapter and its e2e test use, into .tools/tailwind-ls. Prints the
# server's path; put it in tailwind.command, or on PATH.
set -eu
version=0.16.0
root=$(cd "$(dirname "$0")/.." && pwd)
dir="$root/.tools/tailwind-ls"
mkdir -p "$dir"
npm install --prefix "$dir" --no-audit --no-fund "@tailwindcss/language-server@$version" >&2
echo "$dir/node_modules/.bin/tailwindcss-language-server"
