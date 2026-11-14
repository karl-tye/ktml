//go:build !ktmldev

package main

import "net/http"

func devHandler(handler http.Handler) http.Handler { return handler }
