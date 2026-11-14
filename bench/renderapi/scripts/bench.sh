#!/usr/bin/env bash
# Runs the ADR-0003 synthetic protocol: the byte-identity tests, then every
# benchmark with -count=10 -cpu=1, then benchstat by implementation.
# Run from bench/renderapi on an idle machine: scripts/bench.sh [results-dir]
set -euo pipefail

cd "$(dirname "$0")/.."
results=${1:-results}
mkdir -p "$results"

go test -count=1 ./...
{
	echo "# go: $(go version); GOAMD64=$(go env GOAMD64); commit: $(git rev-parse --short HEAD)"
	go test -run '^$' -bench . -benchmem -count=10 -cpu=1 ./...
} >"$results/synthetic.txt"
benchstat -col /impl "$results/synthetic.txt" >"$results/synthetic-benchstat.txt"
