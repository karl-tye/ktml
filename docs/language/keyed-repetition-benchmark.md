# Keyed Repetition benchmark

- Date: 2026-10-12
- Gate: [Keyed Repetition v1](keyed-repetition-v1.md#allocation-and-throughput-gate)
- Module: [`bench/keyed`](../../bench/keyed) (separate `go.mod`; templ never
  enters the main module)
- Verdict: **the gate passes.**

## Setup

- go1.27.0, linux/amd64, `GOAMD64=v1`, AMD Ryzen AI Max+ 395, commit of this
  record's slice (Delivery Slice 3).
- `go test -run '^$' -bench . -benchmem -count=6 -cpu=1 -benchtime=0.5s ./list`.
  Values are medians of 6 runs; the spread of every row is under ±2.5%. Raw
  output: [`bench/keyed/results.txt`](../../bench/keyed/results.txt).
  `benchstat` was not installed, so medians were computed directly.
- Implementations, all byte-identical (checked by a test before timing):
  - **KTML**: the generated production variant (`//go:build !ktmldev`) from
    `gogen`, through the public `Component.Render`.
  - **Templ**: `templ v0.3.1020` generated code.
  - **HandwrittenPreflight**: handwritten Go with the same semantics (a
    `map` uniqueness preflight per repetition, then streaming).
  - **Handwritten**: handwritten Go without a preflight (lower bound).
- Shapes:
  - `Flat`: 1,000 records with string keys, an escaped title, and an integer
    attribute.
  - `Nested`: 1,000 records with integer keys, an optional field (half
    present), and a three-item integer-keyed nested list (ADR-0003's
    `ListHeavy`). The optional field is carried but not rendered, because
    conditions do not exist yet.
  - `Small`: 5 records, so the inline key set is measured.
  - `Empty`: a nil collection.
- Writers: `io.Discard`, and a pre-grown `*bytes.Buffer` reset each iteration.

## Results

ns/op (allocs/op)

| Shape | Writer | KTML | Templ | Handwritten + preflight | Handwritten |
| --- | --- | ---: | ---: | ---: | ---: |
| Flat | Discard | 79,488 (1,907) | 124,456 (4,905) | 83,769 (2,905) | 67,640 (2,900) |
| Flat | Buffer | 98,424 (1,907) | 124,082 (4,905) | 89,085 (2,905) | 73,705 (2,900) |
| Nested | Discard | 141,779 (3,007) | 304,014 (11,005) | 203,453 (7,005) | 164,192 (7,000) |
| Nested | Buffer | 176,952 (3,007) | 297,945 (11,005) | 212,378 (7,005) | 173,419 (7,000) |
| Small | Discard | 323 (6) | 696 (25) | 356 (10) | 291 (10) |
| Small | Buffer | 409 (6) | 711 (25) | 386 (10) | 325 (10) |
| Empty | Discard | 12.3 (0) | 106.7 (5) | 12.0 (0) | 5.8 (0) |
| Empty | Buffer | 17.6 (0) | 106.1 (5) | 14.7 (0) | 9.4 (0) |

B/op: Flat 89,480 / 83,000 / 137,408 / 82,800; Nested 92,216 / 151,409 /
228,144 / 191,200; Small 144 / 552 / 360 / 360; Empty 0 / 192 / 0 / 0
(KTML / templ / handwritten + preflight / handwritten).

KTML ÷ templ, ns/op:

| Shape | Discard | Buffer |
| --- | ---: | ---: |
| Flat | 0.64 | 0.79 |
| Nested | 0.47 | 0.59 |
| Small | 0.46 | 0.58 |
| Empty | 0.12 | 0.17 |

The geomean is 0.41 over all eight rows, and at most 0.79 for any row.

## Gate

| Criterion | Result |
| --- | --- |
| allocs/op ≤ templ for every shape | Pass: 1,907 vs 4,905; 3,007 vs 11,005; 6 vs 25; 0 vs 5 |
| ns/op geomean ≤ 1.00× templ | Pass: 0.41 overall; no row above 0.79 |
| exactly one key set per repetition, declared once per execution | Pass: `TestGenerate_GIVENKeyedRepetitionFixture_WHENInspected_THENHasOneSetPerRepetitionAndNoBodyBuffer`, and by reading `bench/keyed/list/list_ktml.go`: one `var ktmlKeysN rt.*Keys` and one `Reset(len(...))` per repetition, before its preflight loop |
| no buffer of body HTML | Pass: the same test finds no `bytes.Buffer`, `strings.Builder`, `bufio`, or `[]byte` in either variant, and the preflight loop never writes; bodies write directly to the caller's writer |

`go build -gcflags=-m ./list` reports no key set moved to the heap, so the
inline set costs nothing for up to eight keys; `Small` and `Empty` allocate no
set, and each larger execution allocates one map.

## Observations

- Against handwritten Go without any preflight, KTML is 18% (Discard) and 34%
  (Buffer) slower on `Flat`, and 14% faster (Discard) or 2% slower (Buffer) on
  `Nested`. Against handwritten Go with the same `map` preflight, it is faster
  on `Flat`/`Discard`, both `Nested` rows, and `Small`/`Discard`, because the
  inline set never allocates a map for the three-item nested lists or for
  `Small`.
- KTML's remaining allocations are not repetition costs: `rt.Text` escapes
  through `html.EscapeString` (one allocation per escaped value) and
  `rt.AttrInt` formats integers through `strconv.FormatInt`. ADR-0003 already
  moves escaping to allocation-free `rt` writers.
- Generated code still writes static markup in many small calls (`<li`,
  ` class="item"`, `>`), which is why `Flat`/`Buffer` (10%) and `Small`/`Buffer`
  (6%) are slower than handwritten Go with a preflight. Coalescing static runs belongs to item 9.
- templ's fixed cost (5 allocations, ~100 ns) is its pooled buffer and
  children context, visible on `Empty`.
