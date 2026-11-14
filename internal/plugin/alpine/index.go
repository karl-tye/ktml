package alpine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/symbolindex"
	"github.com/karl-tye/ktml/internal/textposition"
)

// indexDocuments answers the host's symbol index request with each
// document's Alpine scope bindings and the references bound to them. Alpine
// scopes never cross documents, so a changed document is indexed alone.
func indexDocuments(payload []byte) ([]byte, error) {
	var request struct {
		ID json.RawMessage `json:"id"`
		symbolindex.Request
	}
	if err := json.Unmarshal(payload, &request); err != nil {
		return nil, err
	}
	answer := symbolindex.Result{Documents: []symbolindex.Document{}}
	var analyses []*analysis
	if request.Workspace {
		if dir, err := os.Getwd(); err == nil {
			// A scan-only workspace: indexing the serving one would make
			// every later edit analyze the whole project. Story Documents
			// declare no components, so they are left out; an opened one
			// is indexed through its URI.
			scan := compiler.NewWorkspace(workspaceRoot(filepath.Join(dir, "ktml.config.json")))
			scan.DeferStories()
			if snapshot, err := scan.Index(); err == nil && snapshot != nil {
				for _, file := range indexedFiles(snapshot) {
					if current, ok := analyzeIn(snapshot, file); ok {
						analyses = append(analyses, current)
					}
				}
			}
		}
	}
	for _, uri := range request.URIs {
		path := uriPath(uri)
		if !strings.HasSuffix(path, ".ktml") {
			continue
		}
		if _, err := os.Stat(path); os.IsNotExist(err) && !isOpen(path) {
			answer.Removed = append(answer.Removed, pathURI(path))
			continue
		}
		if current, ok := analyze(path); ok {
			analyses = append(analyses, current)
		}
	}
	for _, current := range analyses {
		answer.Documents = append(answer.Documents, current.indexDocument())
	}
	return result(request.ID, answer)
}

func isOpen(path string) bool {
	state.Lock()
	defer state.Unlock()
	return state.open[path]
}

func (a *analysis) indexDocument() symbolindex.Document {
	document := symbolindex.Document{URI: pathURI(a.path), Symbols: []symbolindex.Symbol{}, References: []symbolindex.Reference{}}
	span := func(start, end int) symbolindex.Range {
		startLine, startCharacter := textposition.Position(a.source, start)
		endLine, endCharacter := textposition.Position(a.source, end)
		return symbolindex.Range{
			Start: symbolindex.Position{Line: startLine, Character: startCharacter},
			End:   symbolindex.Position{Line: endLine, Character: endCharacter},
		}
	}
	for _, binding := range a.document.Bindings {
		id := "alpine:" + a.path + "#" + strconv.Itoa(binding.Start)
		document.Symbols = append(document.Symbols, symbolindex.Symbol{
			ID: id, Kind: symbolindex.AlpineBinding, Name: binding.Name, Range: span(binding.Start, binding.End),
			Detail: "Alpine " + binding.Kind.String() + " " + binding.Name,
		})
		for _, reference := range a.document.ReferencesTo(binding) {
			kind := symbolindex.ReadReference
			if reference.Write {
				kind = symbolindex.WriteReference
			}
			document.References = append(document.References, symbolindex.Reference{Symbol: id, Kind: kind, Range: span(reference.Start, reference.End)})
		}
	}
	return document
}
