#!/bin/bash
set -euo pipefail

repo_dir="$(cd "$(dirname "$0")" && pwd)"

cd "$repo_dir"
go test -count=1 -run '^TestConformanceCorpus_' ./internal/compiler

"$repo_dir/tree-sitter-ktml/test.sh"
