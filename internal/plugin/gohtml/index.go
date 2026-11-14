package gohtml

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/symbolindex"
	"github.com/karl-tye/ktml/internal/textposition"
)

// handleIndex answers the host's symbol index request from the project
// snapshot: the whole project for a workspace request, and otherwise the
// changed documents and every document that imports them, directly or not,
// since their references name the changed declarations.
func handleIndex(payload []byte) ([]byte, error) {
	var request struct {
		ID json.RawMessage `json:"id"`
		symbolindex.Request
	}
	if err := json.Unmarshal(payload, &request); err != nil {
		return nil, err
	}
	respond := func(result symbolindex.Result) ([]byte, error) {
		return json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}
	result := symbolindex.Result{Documents: []symbolindex.Document{}}

	if request.Workspace {
		root, err := os.Getwd()
		if err != nil {
			return respond(result)
		}
		snapshot, err := seededProject(projectWorkspace(filepath.Join(root, "ktml.config.json")), true)
		if err != nil {
			return respond(result)
		}
		result.Documents = indexDocuments(snapshot, snapshotFiles(snapshot))
		return respond(result)
	}

	// Documents changed on disk, rather than in an open buffer, are read
	// again; a deleted one leaves the project.
	open := getOpenDocuments()
	byWorkspace := make(map[*compiler.Workspace][]string)
	var order []*compiler.Workspace
	for _, uri := range request.URIs {
		path := strings.TrimPrefix(uri, "file://")
		if !strings.HasSuffix(path, ".ktml") && !compiler.IsSchemaPath(path) {
			continue
		}
		workspace := projectWorkspace(path)
		if byWorkspace[workspace] == nil {
			order = append(order, workspace)
		}
		byWorkspace[workspace] = append(byWorkspace[workspace], path)
	}
	for _, workspace := range order {
		changed := byWorkspace[workspace]
		onDisk := false
		for _, path := range changed {
			if _, isOpen := open["file://"+path]; !isOpen {
				onDisk = true
			}
		}
		workspace.Sync(changed)
		snapshot, err := seededProject(workspace, onDisk)
		if err != nil {
			continue
		}
		files := withDependents(snapshot, changed)
		for _, path := range changed {
			if !snapshot.HasFile(path) {
				result.Removed = append(result.Removed, "file://"+path)
			}
		}
		result.Documents = append(result.Documents, indexDocuments(snapshot, files)...)
	}
	return respond(result)
}

// seededProject returns the project snapshot, seeding open documents on
// first use like projectSnapshot. reread analyzes the project again so that
// documents changed on disk are read.
func seededProject(workspace *compiler.Workspace, reread bool) (*compiler.Snapshot, error) {
	seededProjectsMu.Lock()
	seeded := seededProjects[workspace]
	seededProjects[workspace] = true
	seededProjectsMu.Unlock()
	if !seeded {
		for uri, text := range getOpenDocuments() {
			if open := strings.TrimPrefix(uri, "file://"); strings.HasSuffix(open, ".ktml") || compiler.IsSchemaPath(open) {
				_ = workspace.Overlay(open, 0, []byte(text))
			}
		}
		return workspace.Index()
	}
	if reread {
		return workspace.Index()
	}
	return workspace.Current()
}

// snapshotFiles returns every document a snapshot loaded, in path order.
func snapshotFiles(snapshot *compiler.Snapshot) []string {
	return snapshot.Files()
}

// withDependents returns changed and every loaded document that imports one
// of them, directly or not, in path order.
func withDependents(snapshot *compiler.Snapshot, changed []string) []string {
	importers := make(map[string][]string)
	for _, file := range snapshot.Files() {
		for _, dependency := range snapshot.Dependencies(file) {
			importers[dependency] = append(importers[dependency], file)
		}
	}
	reached := make(map[string]bool)
	var pending []string
	for _, path := range changed {
		if canonical, err := filepath.Abs(path); err == nil && snapshot.HasFile(canonical) && !reached[canonical] {
			reached[canonical] = true
			pending = append(pending, canonical)
		}
	}
	for len(pending) > 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		for _, importer := range importers[current] {
			if !reached[importer] {
				reached[importer] = true
				pending = append(pending, importer)
			}
		}
	}
	files := make([]string, 0, len(reached))
	for file := range reached {
		files = append(files, file)
	}
	sort.Strings(files)
	return files
}

// indexDocuments converts the compiler's symbol index of files to the
// protocol's documents, with LSP positions.
func indexDocuments(snapshot *compiler.Snapshot, files []string) []symbolindex.Document {
	indexed := snapshot.SymbolIndex(files)
	paths := make([]string, 0, len(indexed))
	for path := range indexed {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	documents := make([]symbolindex.Document, 0, len(paths))
	for _, path := range paths {
		positions := newLineTable(snapshot.Source(path))
		symbols := indexed[path]
		document := symbolindex.Document{
			URI:        "file://" + path,
			Symbols:    make([]symbolindex.Symbol, 0, len(symbols.Symbols)),
			References: make([]symbolindex.Reference, 0, len(symbols.References)),
		}
		for _, symbol := range symbols.Symbols {
			document.Symbols = append(document.Symbols, symbolindex.Symbol{
				ID: string(symbol.ID), Kind: symbol.Kind, Name: symbol.Name, Detail: symbol.Detail, Container: string(symbol.Container),
				Range: positions.span(symbol.Range),
			})
		}
		for _, reference := range symbols.References {
			document.References = append(document.References, symbolindex.Reference{
				Symbol: string(reference.Symbol), Kind: reference.Kind, From: string(reference.From), Alias: reference.Alias,
				Range: positions.span(reference.Range),
			})
		}
		documents = append(documents, document)
	}
	return documents
}

// lineTable converts byte offsets of one source to LSP positions without
// rescanning the source for each offset.
type lineTable struct {
	source []byte
	starts []int
}

func newLineTable(source []byte) lineTable {
	starts := []int{0}
	for index, character := range source {
		if character == '\n' {
			starts = append(starts, index+1)
		}
	}
	return lineTable{source: source, starts: starts}
}

func (table lineTable) position(offset int) symbolindex.Position {
	offset = max(0, min(offset, len(table.source)))
	line := sort.Search(len(table.starts), func(i int) bool { return table.starts[i] > offset }) - 1
	_, character := textposition.Position(table.source[table.starts[line]:], offset-table.starts[line])
	return symbolindex.Position{Line: line, Character: character}
}

func (table lineTable) span(r compiler.SourceRange) symbolindex.Range {
	return symbolindex.Range{Start: table.position(r.Start), End: table.position(r.End)}
}
