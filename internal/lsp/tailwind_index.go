package lsp

import (
	"context"
	"maps"
	"net/url"
	"os"
	"slices"
	"strings"

	"github.com/karl-tye/ktml/internal/symbolindex"
	"github.com/karl-tye/ktml/internal/tailwind"
	"github.com/karl-tye/ktml/internal/textposition"
)

// ClassID is the symbol index ID of a class token.
func ClassID(class string) string {
	return "class:" + class
}

// IndexDocuments contributes each document's class tokens to the symbol
// index, as uses of their class: references and highlights of a class reach
// every document that writes it. Classes are declared in stylesheets, which
// the Tailwind server's theme navigation reaches, so the index holds no
// declarations for them.
func (a *TailwindAdapter) IndexDocuments(_ context.Context, request symbolindex.Request) (symbolindex.Result, error) {
	a.mu.Lock()
	open := make(map[string]*tailwindDocument, len(a.documents))
	maps.Copy(open, a.documents)
	a.mu.Unlock()
	result := symbolindex.Result{Documents: []symbolindex.Document{}}
	add := func(uri string, source []byte, extraction tailwind.Extraction) {
		document := symbolindex.Document{URI: uri, Symbols: []symbolindex.Symbol{}, References: make([]symbolindex.Reference, 0, len(extraction.Tokens))}
		for _, token := range extraction.Tokens {
			document.References = append(document.References, symbolindex.Reference{Symbol: ClassID(token.Class), Kind: symbolindex.ReadReference, Range: indexRange(source, token.Start, token.End)})
		}
		result.Documents = append(result.Documents, document)
	}
	if request.Workspace {
		for _, uri := range slices.Sorted(maps.Keys(open)) {
			add(uri, open[uri].source, open[uri].extraction)
		}
		for _, file := range a.index.files() {
			if uri := fileURI(file.Path); open[uri] == nil {
				add(uri, file.Source, file.Extraction)
			}
		}
		return result, nil
	}
	for _, uri := range request.URIs {
		if !strings.HasSuffix(uri, ".ktml") {
			continue
		}
		if document := open[uri]; document != nil {
			add(uri, document.source, document.extraction)
			continue
		}
		parsed, err := url.Parse(uri)
		if err != nil {
			continue
		}
		source, err := os.ReadFile(parsed.Path)
		if err != nil {
			result.Removed = append(result.Removed, uri)
			continue
		}
		add(uri, source, tailwind.Extract(source))
	}
	return result, nil
}

func indexRange(source []byte, start, end int) symbolindex.Range {
	startLine, startCharacter := textposition.Position(source, start)
	endLine, endCharacter := textposition.Position(source, end)
	return symbolindex.Range{
		Start: symbolindex.Position{Line: startLine, Character: startCharacter},
		End:   symbolindex.Position{Line: endLine, Character: endCharacter},
	}
}
