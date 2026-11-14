package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/karl-tye/ktml/internal/textposition"
	"github.com/karl-tye/ktml/internal/vet"
)

const vetSource = "ktml vet"

// vetDelay gathers a burst of edits into one vet run.
const vetDelay = 300 * time.Millisecond

// VetAdapter runs ktml vet over the workspace's Go packages and publishes
// its findings on the handwritten Go files they are in: props literals that
// omit a required input. It vets with the editor's KTML buffers, so a
// component's requirements are current before its document is saved, and
// runs again after KTML edits and Go file changes on disk, in the
// background. Findings vet makes about KTML or Go that does not compile are
// left to the KTML tooling and gopls, which report them already.
type VetAdapter struct {
	root string

	mu        sync.Mutex
	documents map[string][]byte
	// files are the Go files whose findings were last published.
	files map[string]bool

	trigger   chan struct{}
	published chan []byte
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

// NewVetAdapter returns the adapter for the workspace root. It first vets
// once the client is initialized.
func NewVetAdapter(root string) *VetAdapter {
	a := &VetAdapter{
		root:      root,
		documents: make(map[string][]byte),
		files:     make(map[string]bool),
		trigger:   make(chan struct{}, 1),
		published: make(chan []byte, 64),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
	go a.run()
	return a
}

func (a *VetAdapter) Published() <-chan []byte { return a.published }

func (a *VetAdapter) Close() error {
	a.closeOnce.Do(func() { close(a.stop) })
	<-a.done
	return nil
}

// ExecuteLSP records KTML buffers and schedules a run after each change. It
// answers no request.
func (a *VetAdapter) ExecuteLSP(_ context.Context, method string, payload []byte) ([]byte, error) {
	var params struct {
		TextDocument struct {
			URI  string `json:"uri"`
			Text string `json:"text"`
		} `json:"textDocument"`
		ContentChanges []struct {
			Text string `json:"text"`
		} `json:"contentChanges"`
	}
	if len(payload) > 0 && json.Unmarshal(payload, &params) != nil {
		return nil, nil
	}
	path, isKTML := ktmlPath(params.TextDocument.URI)
	switch method {
	case "initialized", "workspace/didChangeWatchedFiles", "textDocument/didSave":
		a.schedule()
	case "textDocument/didOpen", "textDocument/didChange":
		if !isKTML {
			return nil, nil
		}
		text := params.TextDocument.Text
		if len(params.ContentChanges) > 0 {
			text = params.ContentChanges[len(params.ContentChanges)-1].Text
		}
		a.mu.Lock()
		a.documents[path] = []byte(text)
		a.mu.Unlock()
		a.schedule()
	case "textDocument/didClose":
		if isKTML {
			a.mu.Lock()
			delete(a.documents, path)
			a.mu.Unlock()
			a.schedule()
		}
	}
	return nil, nil
}

func (a *VetAdapter) schedule() {
	select {
	case a.trigger <- struct{}{}:
	default:
	}
}

func (a *VetAdapter) run() {
	defer close(a.done)
	for {
		select {
		case <-a.trigger:
		case <-a.stop:
			return
		}
		// Edits typed in a burst make one run.
		for quiet := false; !quiet; {
			select {
			case <-a.trigger:
			case <-time.After(vetDelay):
				quiet = true
			case <-a.stop:
				return
			}
		}
		a.vet()
	}
}

// vet runs once and publishes every Go file whose findings changed,
// including files that no longer have any.
func (a *VetAdapter) vet() {
	a.mu.Lock()
	overlays := make(map[string][]byte, len(a.documents))
	for path, source := range a.documents {
		overlays[path] = source
	}
	a.mu.Unlock()
	diagnostics, err := vet.RunWithOverlays(a.root, nil, overlays)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ktml vet: %v\n", err)
		return
	}
	byFile := make(map[string][]vet.Diagnostic)
	for _, diagnostic := range diagnostics {
		if diagnostic.Code != vet.CodeMissingRequiredInput {
			continue
		}
		file := diagnostic.Path
		if !filepath.IsAbs(file) {
			file = filepath.Join(a.root, file)
		}
		byFile[file] = append(byFile[file], diagnostic)
	}
	a.mu.Lock()
	previous := a.files
	a.files = make(map[string]bool, len(byFile))
	for file := range byFile {
		a.files[file] = true
	}
	a.mu.Unlock()
	files := make([]string, 0, len(byFile)+len(previous))
	for file := range byFile {
		files = append(files, file)
	}
	for file := range previous {
		if _, still := byFile[file]; !still {
			files = append(files, file)
		}
	}
	sort.Strings(files)
	for _, file := range files {
		notification, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"method":  publishDiagnosticsMethod,
			"params":  map[string]any{"uri": fileURI(file), "diagnostics": vetDiagnostics(file, byFile[file])},
		})
		select {
		case a.published <- notification:
		case <-a.stop:
			return
		}
	}
}

// vetDiagnostics converts vet's 1-based byte positions to LSP ranges over
// the literal's type name.
func vetDiagnostics(file string, diagnostics []vet.Diagnostic) []any {
	source, _ := os.ReadFile(file)
	lineStarts := []int{0}
	for index, character := range source {
		if character == '\n' {
			lineStarts = append(lineStarts, index+1)
		}
	}
	converted := []any{}
	for _, diagnostic := range diagnostics {
		start := len(source)
		if line := diagnostic.Line - 1; line >= 0 && line < len(lineStarts) {
			start = min(lineStarts[line]+diagnostic.Column-1, len(source))
		}
		end := start
		for end < len(source) && (source[end] == '.' || source[end] == '_' || source[end] >= 'a' && source[end] <= 'z' || source[end] >= 'A' && source[end] <= 'Z' || source[end] >= '0' && source[end] <= '9') {
			end++
		}
		var lspStart, lspEnd lspPosition
		lspStart.Line, lspStart.Character = textposition.Position(source, start)
		lspEnd.Line, lspEnd.Character = textposition.Position(source, end)
		converted = append(converted, map[string]any{
			"range":    lspRange{Start: lspStart, End: lspEnd},
			"severity": 2,
			"code":     diagnostic.Code,
			"source":   vetSource,
			"message":  strings.TrimSpace(diagnostic.Message),
		})
	}
	return converted
}
