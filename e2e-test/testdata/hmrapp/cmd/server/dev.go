//go:build ktmldev

package main

import (
	"net/http"

	"github.com/karl-tye/ktml/dev/hmr"
)

// devHandler is the one application wiring file that links the development
// registry (ADR-0004); production builds use prod.go.
func devHandler(handler http.Handler) http.Handler { return hmr.Install(handler) }
