// Command size is the linked-size harness of the ADR-0003 benchmarks. Built
// with the sizeh or sizeb tag, it links and calls every benchmark
// component of that API tree; built with no tag, it is the baseline with the
// same harness and no KTML. scripts/size.sh builds all four and reports the
// .text deltas.
package main

import (
	"context"
	"fmt"
	"os"
)

func main() {
	if err := renderAll(context.Background(), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
