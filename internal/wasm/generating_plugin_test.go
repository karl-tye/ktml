package wasm

import "github.com/karl-tye/ktml/internal/buildstate"

// anyOutputs lets a test plugin write generated files anywhere in the
// project outside the host's deny-list.
var anyOutputs = []buildstate.OutputRoot{{Dir: "."}}
