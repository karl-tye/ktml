package main

import (
	"fmt"
	"io"
	"os"

	"github.com/karl-tye/ktml/internal/vet"
)

// vetCmd runs ktml vet [patterns...] from the working directory, prints one
// line per finding to stdout, and returns the process exit status: 1 when
// there are findings or vet fails, and 0 otherwise.
func vetCmd(stdout, stderr io.Writer, patterns []string) int {
	workDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	diagnostics, err := vet.Run(workDir, patterns)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	for _, diagnostic := range diagnostics {
		fmt.Fprintln(stdout, diagnostic)
	}
	if len(diagnostics) > 0 {
		return 1
	}
	return 0
}
