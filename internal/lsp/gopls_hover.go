package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/generator/gogen"
	"github.com/karl-tye/ktml/internal/textposition"
)

// hover answers a hover inside a Go-mapped construct, a path, a call, or a
// declaration, with gopls's hover of the Go identifier the source map maps
// the cursor to: the Go type and documentation of the generated field,
// method, or struct. It answers only while the document's overlay was
// generated from the editor's buffer, so the map is current.
func (a *GoplsAdapter) hover(ctx context.Context, path string, position lspPosition) any {
	uri := fileURI(gogen.OutputFile(path, false))
	a.mu.Lock()
	overlay := a.generated[uri]
	document, open := a.documents[path]
	if overlay == nil || overlay.stale || overlay.openedIn == nil || overlay.openedIn != a.server || open && !bytes.Equal(document.source, overlay.sourceText) {
		a.mu.Unlock()
		return nil
	}
	source, text, mappings := overlay.sourceText, overlay.text, overlay.mappings
	a.mu.Unlock()

	offset := textposition.Offset(source, position.Line, position.Character)
	goOffset, start, end, ok := goIdentifierAt(path, source, offset, text, mappings)
	if !ok {
		return nil
	}
	var goPosition lspPosition
	goPosition.Line, goPosition.Character = textposition.Position(text, goOffset)
	raw, err := a.request(ctx, "textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": goPosition})
	if err != nil {
		return nil
	}
	var answer struct {
		Contents json.RawMessage `json:"contents"`
	}
	if json.Unmarshal(raw, &answer) != nil || len(answer.Contents) == 0 {
		return nil
	}
	value := strings.TrimSpace(hoverText(answer.Contents))
	if value == "" {
		return nil
	}
	return map[string]any{"contents": map[string]any{"kind": "markdown", "value": value}, "range": byteRange(source, start, end)}
}

// goIdentifierAt maps a KTML offset to the Go identifier generated for it:
// the innermost mapped KTML range holding the offset, and in the Go selector
// chain at its mapped token, the identifier of the dotted part under the
// cursor. A KTML path a.b.c generates a chain ending in its parts, such as
// props.A.B.C, so the parts align from the end. The identifier must spell
// the part, as the generated Go name or as written; otherwise the map does
// not reach it. It returns the identifier's Go offset and the part's KTML
// range.
func goIdentifierAt(path string, source []byte, offset int, text []byte, mappings []gogen.SourceMapping) (goOffset, start, end int, ok bool) {
	var best *gogen.SourceMapping
	for index := range mappings {
		mapping := &mappings[index]
		if mapping.Source.File != path || offset < mapping.Source.Start || offset > mapping.Source.End || mapping.Source.End > len(source) {
			continue
		}
		if best == nil || mapping.Source.End-mapping.Source.Start < best.Source.End-best.Source.Start {
			best = mapping
		}
	}
	if best == nil || best.Go >= len(text) {
		return 0, 0, 0, false
	}

	// The KTML parts, with their ranges.
	type part struct {
		name       string
		start, end int
	}
	// An interpolation's range holds its braces.
	first, last := best.Source.Start, best.Source.End
	if last-first >= 2 && source[first] == '{' && source[last-1] == '}' {
		first, last = first+1, last-1
	}
	var parts []part
	partStart := first
	for index := first; index <= last; index++ {
		if index == last || source[index] == '.' {
			name := strings.TrimRight(string(source[partStart:index]), "?")
			if !goIdentifier(name) {
				return 0, 0, 0, false
			}
			parts = append(parts, part{name, partStart, partStart + len(name)})
			partStart = index + 1
		}
	}
	current := -1
	for index, candidate := range parts {
		if offset >= candidate.start && offset <= candidate.end {
			current = index
		}
	}
	if current < 0 {
		return 0, 0, 0, false
	}

	// The Go selector chain at the mapped token.
	type ident struct {
		name  string
		start int
	}
	var chain []ident
	for index := best.Go; ; {
		identStart := index
		for index < len(text) && (text[index] == '_' || text[index] >= 'a' && text[index] <= 'z' || text[index] >= 'A' && text[index] <= 'Z' || text[index] >= '0' && text[index] <= '9') {
			index++
		}
		if index == identStart {
			break
		}
		chain = append(chain, ident{string(text[identStart:index]), identStart})
		if index >= len(text) || text[index] != '.' {
			break
		}
		index++
	}
	at := len(chain) - len(parts) + current
	if len(chain) < len(parts) || at < 0 {
		return 0, 0, 0, false
	}
	name := parts[current].name
	if chain[at].name != name && chain[at].name != compiler.GoFieldName(name) && chain[at].name != compiler.GeneratedGoField(name) {
		return 0, 0, 0, false
	}
	return chain[at].start, parts[current].start, parts[current].end, true
}

func goIdentifier(name string) bool {
	if name == "" {
		return false
	}
	for index, character := range name {
		letter := character == '_' || character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z'
		if !letter && (index == 0 || character < '0' || character > '9') {
			return false
		}
	}
	return true
}

// mergeHoverResponses joins the hovers of several responders, KTML's
// first, into one markdown hover with the first range.
func mergeHoverResponses(responses [][]byte) []byte {
	results, rpcErrors := validPluginResponses(responses, nil)
	if len(results) == 0 {
		return deterministicPluginError(rpcErrors)
	}
	var values []string
	var hoverRange any
	for _, response := range results {
		hover, ok := response.result.(map[string]any)
		if !ok {
			continue
		}
		contents, _ := json.Marshal(hover["contents"])
		if value := strings.TrimSpace(hoverText(contents)); value != "" {
			values = append(values, value)
			if hoverRange == nil {
				hoverRange = hover["range"]
			}
		}
	}
	if len(values) == 0 {
		return rpcResult(results[0].message.id, nil)
	}
	merged := map[string]any{"contents": map[string]any{"kind": "markdown", "value": strings.Join(values, "\n\n---\n\n")}}
	if hoverRange != nil {
		merged["range"] = hoverRange
	}
	return rpcResult(results[0].message.id, merged)
}
