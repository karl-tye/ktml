# HMR browser fixture

A small dashboard application for `TestHMR` (e2e-test/hmr_test.go): a doctype
layout, a dashboard page, keyed job cards, Alpine and HTMX islands, and HTMX
fragment roots, a page (`/planner`) that Go passes into the layout's content
slot, generated with the native Go backend and linked with the
development registry through `cmd/server/dev.go` under `-tags ktmldev`.

The test copies this directory into a temporary module, so generated Go never
lands here. `static/alpine.min.js` is Alpine.js 3.14.9 (MIT) and
`static/htmx.min.js` is the fixture's HTMX copy.
