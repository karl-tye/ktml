package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/generator/gogen"
	"github.com/karl-tye/ktml/internal/gopkg"
	"github.com/karl-tye/ktml/internal/textposition"
)

// Navigation from KTML into handwritten Go. A Host Schema record or field,
// or a component, is a Go declaration in generated code; gopls finds the Go
// that uses it. Locations in generated files are left out: the KTML tooling
// already reports the .ktml uses they were generated from.

// occurrence resolves the Host Schema symbol, or with components set also
// the component, at a position of a KTML document. It answers only while the
// document's last generated snapshot matches the editor's buffer.
func (a *GoplsAdapter) occurrence(path string, position lspPosition, components bool) (compiler.SourceRange, *compiler.SchemaOccurrence, *compiler.Snapshot, int, bool) {
	a.mu.Lock()
	snapshot := a.snapshots[path]
	document, open := a.documents[path]
	a.mu.Unlock()
	if snapshot == nil {
		return compiler.SourceRange{}, nil, nil, 0, false
	}
	source := snapshot.Source(path)
	if open && !bytes.Equal(document.source, source) {
		return compiler.SourceRange{}, nil, nil, 0, false
	}
	offset := textposition.Offset(source, position.Line, position.Character)
	if occurrence, ok := snapshot.SchemaOccurrenceAt(path, offset); ok {
		return occurrence.Symbol.Range, &occurrence, snapshot, offset, true
	}
	if components {
		if component, _, ok := snapshot.SymbolAt(path, offset); ok {
			return component.Range, nil, snapshot, offset, true
		}
	}
	return compiler.SourceRange{}, nil, nil, 0, false
}

// goDeclaration returns the position of the Go declaration generated for a
// KTML declaration: the first token its source map maps there.
func (a *GoplsAdapter) goDeclaration(declaration compiler.SourceRange) (string, lspPosition, bool) {
	uri := fileURI(gogen.OutputFile(declaration.File, false))
	a.mu.Lock()
	overlay := a.generated[uri]
	fresh := overlay != nil && !overlay.stale && overlay.openedIn != nil && overlay.openedIn == a.server
	a.mu.Unlock()
	if !fresh {
		a.refresh(filepath.Dir(declaration.File))
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	overlay = a.generated[uri]
	if overlay == nil || overlay.stale || overlay.openedIn == nil || overlay.openedIn != a.server {
		return "", lspPosition{}, false
	}
	for _, mapping := range overlay.mappings {
		if mapping.Source.File == declaration.File && mapping.Source.Start == declaration.Start {
			var position lspPosition
			position.Line, position.Character = textposition.Position(overlay.text, mapping.Go)
			return uri, position, true
		}
	}
	return "", lspPosition{}, false
}

// goUsages returns the handwritten Go locations that use the Host Schema
// symbol at a position: for a definition request, of records and fields; for
// references, also of components.
func (a *GoplsAdapter) goUsages(ctx context.Context, path string, position lspPosition, references bool) any {
	declaration, _, _, _, ok := a.occurrence(path, position, references)
	if !ok {
		return nil
	}
	uri, goPosition, ok := a.goDeclaration(declaration)
	if !ok {
		return nil
	}
	raw, err := a.request(ctx, "textDocument/references", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     goPosition,
		"context":      map[string]any{"includeDeclaration": false},
	})
	if err != nil {
		return nil
	}
	var locations []struct {
		URI   string   `json:"uri"`
		Range lspRange `json:"range"`
	}
	if json.Unmarshal(raw, &locations) != nil {
		return nil
	}
	kept := []any{}
	for _, location := range locations {
		if generatedGoURI(location.URI) {
			continue
		}
		kept = append(kept, map[string]any{"uri": location.URI, "range": location.Range})
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}

// generatedGoURI reports whether a URI names KTML generator output.
func generatedGoURI(uri string) bool {
	parsed, err := url.Parse(uri)
	return err == nil && gopkg.Generated(filepath.Base(parsed.Path))
}

// rename renames the Go a Host Schema record or field generates in the
// handwritten Go that uses it, beside the KTML edits the KTML tooling
// returns. It answers only where the KTML rename succeeds. What it cannot
// rename is reported as a warning: the Go names a state or Variant derives,
// or any rename gopls refuses or cannot make.
func (a *GoplsAdapter) rename(ctx context.Context, path string, position lspPosition, newName string) any {
	declaration, occurrence, snapshot, offset, ok := a.occurrence(path, position, false)
	if !ok || occurrence == nil || occurrence.Alias {
		return nil
	}
	// The KTML rename validates over every importer in the workspace.
	if indexed := a.indexed(); indexed != nil {
		if resolved, ok := indexed.SchemaOccurrenceAt(path, offset); ok {
			snapshot, occurrence, declaration = indexed, &resolved, resolved.Symbol.Range
		}
	}
	if _, err := snapshot.SchemaRename(occurrence.Symbol.ID, newName); err != nil {
		return nil
	}
	symbol := occurrence.Symbol
	var goName, oldName string
	switch symbol.Kind {
	case compiler.RecordSymbol:
		goName, oldName = newName, symbol.Name
	case compiler.FieldSymbol:
		goName, oldName = compiler.GoFieldName(newName), compiler.GoFieldName(symbol.Name)
	default:
		a.warn(fmt.Sprintf("Renamed %s %s in KTML only: the Go names it generates (%s and the names derived from it) are not renamed in handwritten Go. Update the Go that uses them.", symbol.Kind, symbol.Name, symbol.Name))
		return nil
	}
	if goName == oldName {
		return nil
	}
	if a.ensureServer() == nil {
		a.warn(fmt.Sprintf("Renamed %s %s in KTML only: gopls is unavailable, so Go that uses %s was not renamed. Rename it to %s in Go.", symbol.Kind, symbol.Name, oldName, goName))
		return nil
	}
	uri, goPosition, ok := a.goDeclaration(declaration)
	if !ok {
		a.warn(fmt.Sprintf("Renamed %s %s in KTML only: its generated Go is unavailable, so Go that uses %s was not renamed.", symbol.Kind, symbol.Name, oldName))
		return nil
	}
	raw, err := a.request(ctx, "textDocument/rename", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     goPosition,
		"newName":      goName,
	})
	if err != nil {
		a.warn(fmt.Sprintf("Renamed %s %s in KTML only: gopls could not rename the Go %s to %s (%v). Update the Go that uses it.", symbol.Kind, symbol.Name, oldName, goName, err))
		return nil
	}
	changes := handwrittenChanges(raw)
	if len(changes) == 0 {
		return nil
	}
	return map[string]any{"changes": changes}
}

// handwrittenChanges returns a gopls WorkspaceEdit's text edits to
// handwritten Go files, by URI. Generated files are regenerated from KTML.
func handwrittenChanges(raw json.RawMessage) map[string][]json.RawMessage {
	var edit struct {
		Changes         map[string][]json.RawMessage `json:"changes"`
		DocumentChanges []struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			Edits []json.RawMessage `json:"edits"`
		} `json:"documentChanges"`
	}
	if json.Unmarshal(raw, &edit) != nil {
		return nil
	}
	changes := make(map[string][]json.RawMessage)
	for uri, edits := range edit.Changes {
		if !generatedGoURI(uri) {
			changes[uri] = append(changes[uri], edits...)
		}
	}
	for _, document := range edit.DocumentChanges {
		if document.TextDocument.URI != "" && !generatedGoURI(document.TextDocument.URI) {
			changes[document.TextDocument.URI] = append(changes[document.TextDocument.URI], document.Edits...)
		}
	}
	return changes
}

// indexed returns a snapshot of every KTML document under the root, with the
// editor's buffers.
func (a *GoplsAdapter) indexed() *compiler.Snapshot {
	workspace := compiler.NewWorkspace(a.root)
	a.mu.Lock()
	paths := make([]string, 0, len(a.documents))
	for path := range a.documents {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		_ = workspace.Overlay(path, a.documents[path].version, a.documents[path].source)
	}
	a.mu.Unlock()
	snapshot, err := workspace.Index()
	if err != nil {
		return nil
	}
	return snapshot
}

// warn shows the user a warning.
func (a *GoplsAdapter) warn(message string) {
	notification, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "window/showMessage", "params": map[string]any{"type": 2, "message": message}})
	a.publish(notification)
}
