package htmx

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/karl-tye/ktml/internal/symbolindex"
)

// RouteID is the symbol index ID of a route: its method and effective path.
func RouteID(route Route) string {
	return "route:" + route.Method + " " + route.Path
}

// indexDocuments answers the host's symbol index request. Routes are
// declared by their Chi registrations in Go, and every HTMX request
// attribute that reaches a route references it. The Go documents are always
// all returned, since one scan result changes any of them; KTML documents
// are the requested ones, or all for a workspace request. Until the first
// scan arrives nothing is indexed, and the host asks again after each job.
func indexDocuments(payload []byte) ([]byte, error) {
	var request struct {
		ID json.RawMessage `json:"id"`
		symbolindex.Request
	}
	if err := json.Unmarshal(payload, &request); err != nil {
		return nil, err
	}
	answer := symbolindex.Result{Documents: []symbolindex.Document{}}
	if !indexReady() {
		return response(request.ID, answer)
	}
	index, documents, settings, err := requestState()
	if err != nil {
		return response(request.ID, answer)
	}

	byURI := make(map[string][]symbolindex.Symbol)
	for _, route := range index.Routes {
		uri := route.Registration.URI
		byURI[uri] = append(byURI[uri], symbolindex.Symbol{
			ID: RouteID(route), Kind: symbolindex.Route, Name: route.Method + " " + route.Path,
			Range: symbolRange(route.Registration.Range), Detail: route.Method + " " + route.Path + " → " + route.Handler,
		})
	}
	lspState.Lock()
	previous := lspState.indexedGo
	lspState.indexedGo = make(map[string]bool, len(byURI))
	for uri := range byURI {
		lspState.indexedGo[uri] = true
	}
	lspState.Unlock()
	for uri := range previous {
		if _, still := byURI[uri]; !still {
			answer.Removed = append(answer.Removed, uri)
		}
	}
	sort.Strings(answer.Removed)
	uris := make([]string, 0, len(byURI))
	for uri := range byURI {
		uris = append(uris, uri)
	}
	sort.Strings(uris)
	for _, uri := range uris {
		answer.Documents = append(answer.Documents, symbolindex.Document{URI: uri, Symbols: byURI[uri], References: []symbolindex.Reference{}})
	}

	sources := make(map[string]string)
	if request.Workspace {
		sources = ktmlSources(documents, settings)
	}
	for _, uri := range request.URIs {
		if !strings.HasSuffix(uri, ".ktml") {
			continue
		}
		if source, err := documentSource(uri); err == nil {
			sources[uri] = source
		} else {
			answer.Removed = append(answer.Removed, uri)
		}
	}
	ktmlURIs := make([]string, 0, len(sources))
	for uri := range sources {
		ktmlURIs = append(ktmlURIs, uri)
	}
	sort.Strings(ktmlURIs)
	for _, uri := range ktmlURIs {
		answer.Documents = append(answer.Documents, routeReferences(uri, sources[uri], index))
	}
	return response(request.ID, answer)
}

// routeReferences is a KTML document's HTMX request attributes, each a
// reference to every route it reaches.
func routeReferences(uri, source string, index RouteIndex) symbolindex.Document {
	document := symbolindex.Document{URI: uri, Symbols: []symbolindex.Symbol{}, References: []symbolindex.Reference{}}
	for _, attribute := range scanAttributes(source) {
		seen := make(map[string]bool)
		for _, route := range attributeRoutes(attribute, index) {
			id := RouteID(route)
			if seen[id] {
				continue
			}
			seen[id] = true
			document.References = append(document.References, symbolindex.Reference{
				Symbol: id, Kind: symbolindex.ReadReference, Range: symbolRange(offsetRange(source, attribute.ValueStart, attribute.ValueEnd)),
			})
		}
	}
	return document
}

func symbolRange(r Range) symbolindex.Range {
	return symbolindex.Range{
		Start: symbolindex.Position{Line: r.Start.Line, Character: r.Start.Character},
		End:   symbolindex.Position{Line: r.End.Line, Character: r.End.Character},
	}
}
