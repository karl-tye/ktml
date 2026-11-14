#!/usr/bin/env bash
# Reports the ADR-0003 size metrics of the renderapi module:
#   1. generated source size per API tree (bytes and lines);
#   2. linked .text size of cmd/size per API against the no-KTML baseline,
#      plus the text-symbol bytes of the generated packages and of the KTML
#      runtime (ktml, rt, internal/rtwriter) in each binary.
# Run from bench/renderapi: scripts/size.sh [output-file]
set -euo pipefail

cd "$(dirname "$0")/.."
out=${1:-/dev/stdout}
bin=$(mktemp -d)
trap 'rm -rf "$bin"' EXIT

module=github.com/karl-tye/ktml/bench/renderapi

text_size() { size -A "$1" | awk '$1 == ".text" { print $2 }'; }

# symbol_bytes binary regexp: sum of text-symbol sizes whose name matches.
symbol_bytes() {
	go tool nm -size -type "$1" | awk -v pattern="$2" '($3 == "T" || $3 == "t") && $4 ~ pattern { sum += $2 } END { print sum + 0 }'
}

{
	echo "# renderapi size report"
	echo "go: $(go version)"
	echo "GOAMD64: $(go env GOAMD64)"
	echo "commit: $(git rev-parse --short HEAD)"
	echo
	echo "## Generated source per tree (gogen output: *_ktml.go, *_ktmlschema.go)"
	printf '%-5s %10s %8s %8s\n' tree bytes lines files
	for tree in h b; do
		files=$(find "$tree" -name '*_ktml.go' -o -name '*_ktmlschema.go' | sort)
		printf '%-5s %10d %8d %8d\n' "$tree" "$(cat $files | wc -c)" "$(cat $files | wc -l)" "$(echo "$files" | wc -l)"
	done
	echo
	echo "## Hand-lowered source per tree (script/script_hand.go)"
	printf '%-5s %10s %8s\n' tree bytes lines
	for tree in h b; do
		printf '%-5s %10d %8d\n' "$tree" "$(wc -c <"$tree/script/script_hand.go")" "$(wc -l <"$tree/script/script_hand.go")"
	done
	echo
	echo "## Linked .text (cmd/size, go build -trimpath)"
	go build -trimpath -o "$bin/base" ./cmd/size
	base=$(text_size "$bin/base")
	printf '%-9s %10s %10s %16s %14s\n' binary .text delta generated-syms runtime-syms
	printf '%-9s %10d %10s %16s %14s\n' baseline "$base" - - -
	for tree in h b; do
		go build -trimpath -tags "size$tree" -o "$bin/$tree" ./cmd/size
		text=$(text_size "$bin/$tree")
		generated=$(symbol_bytes "$bin/$tree" "^$module/$tree/")
		runtime=$(symbol_bytes "$bin/$tree" "^github.com/karl-tye/ktml(/rt|/internal/rtwriter)?[.]")
		printf '%-9s %10d %10d %16d %14d\n' "$(echo "$tree" | tr a-z A-Z)" "$text" "$((text - base))" "$generated" "$runtime"
	done
} >"$out"
