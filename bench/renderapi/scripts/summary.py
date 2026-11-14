#!/usr/bin/env python3
"""Summarizes results/synthetic.txt as Markdown tables for the README.

Medians of every run per benchmark, implementation, and writer, then the
ns/op ratios A/B, H/B, A/templ, H/templ with geomeans. benchstat
(results/synthetic-benchstat.txt) has the confidence intervals.
Usage: scripts/summary.py [results/synthetic.txt]
"""

import math
import re
import statistics
import sys
from collections import defaultdict

IMPLS = ["A", "H", "B", "templ", "handwritten"]
LINE = re.compile(r"^Benchmark(\w+)/impl=(\w+)/w=(\w+)\s+\d+\s+([\d.]+) ns/op\s+(\d+) B/op\s+(\d+) allocs/op")


def fmt_ns(value):
    if value >= 1000:
        return "{:,.0f}".format(value)
    if value >= 100:
        return "{:.0f}".format(value)
    return "{:.1f}".format(value)


def main():
    path = sys.argv[1] if len(sys.argv) > 1 else "results/synthetic.txt"
    samples = defaultdict(lambda: ([], [], []))
    order = []
    for line in open(path):
        match = LINE.match(line)
        if not match:
            continue
        bench, impl, writer, ns, bytes_, allocs = match.groups()
        row = (bench, writer)
        if row not in order:
            order.append(row)
        ns_list, b_list, a_list = samples[(bench, writer, impl)]
        ns_list.append(float(ns))
        b_list.append(int(bytes_))
        a_list.append(int(allocs))

    def median(row, impl, index):
        return statistics.median(samples[(row[0], row[1], impl)][index])

    print("### ns/op (median of 10)\n")
    print("| Benchmark | Writer | " + " | ".join(IMPLS) + " |")
    print("| --- | --- | " + " | ".join("---:" for _ in IMPLS) + " |")
    for row in order:
        print("| {} | {} | ".format(*row) + " | ".join(fmt_ns(median(row, impl, 0)) for impl in IMPLS) + " |")

    print("\n### allocs/op / B/op (w=discard; w=buffer has the same allocs and B/op within 20 B)\n")
    print("| Benchmark | " + " | ".join(IMPLS) + " |")
    print("| --- | " + " | ".join("---:" for _ in IMPLS) + " |")
    for row in order:
        if row[1] != "discard":
            continue
        cells = ["{:,g} / {:,g}".format(median(row, impl, 2), median(row, impl, 1)) for impl in IMPLS]
        print("| {} | ".format(row[0]) + " | ".join(cells) + " |")

    ratios = [("A", "B"), ("H", "B"), ("A", "templ"), ("H", "templ")]
    print("\n### ns/op ratios\n")
    print("| Benchmark | Writer | " + " | ".join("{}/{}".format(*r) for r in ratios) + " |")
    print("| --- | --- | " + " | ".join("---:" for _ in ratios) + " |")
    logs = defaultdict(list)
    for row in order:
        cells = []
        for num, den in ratios:
            value = median(row, num, 0) / median(row, den, 0)
            logs[(num, den, "all")].append(math.log(value))
            logs[(num, den, row[1])].append(math.log(value))
            cells.append("{:.2f}".format(value))
        print("| {} | {} | ".format(*row) + " | ".join(cells) + " |")
    for scope in ("discard", "buffer", "all"):
        cells = ["**{:.3f}**".format(math.exp(statistics.mean(logs[(num, den, scope)]))) for num, den in ratios]
        print("| geomean | {} | ".format(scope) + " | ".join(cells) + " |")

    print("\n### allocs/op ratios (per top-level render)\n")
    print("| Benchmark | A-B | H-B | A/templ | H/templ |")
    print("| --- | ---: | ---: | ---: | ---: |")
    for row in order:
        if row[1] != "discard":
            continue
        a, h, b, t = (median(row, impl, 2) for impl in ("A", "H", "B", "templ"))
        print("| {} | {:+g} | {:+g} | {} | {} |".format(row[0], a - b, h - b, "{:.2f}".format(a / t) if t else "-", "{:.2f}".format(h / t) if t else "-"))


if __name__ == "__main__":
    main()
