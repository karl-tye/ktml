//go:build !sizeh && !sizeb

package main

import (
	"context"
	"io"
)

func renderAll(_ context.Context, w io.Writer) error {
	_, err := io.WriteString(w, "")
	return err
}
