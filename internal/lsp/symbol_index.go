package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/karl-tye/ktml/internal/symbolindex"
	"github.com/karl-tye/ktml/internal/wasm"
)

// IndexProvider is a native adapter that contributes documents to the
// symbol index, like a plugin that declares symbol-index.
type IndexProvider interface {
	IndexDocuments(ctx context.Context, request symbolindex.Request) (symbolindex.Result, error)
}

type indexProvider struct {
	name  string
	index func(ctx context.Context, request symbolindex.Request) (symbolindex.Result, error)
}

// pluginIndexProvider asks a plugin through ktml_lsp_handle; the plugin
// answers with a JSON-RPC response whose result is a symbolindex.Result.
func pluginIndexProvider(name string, plugin *wasm.Plugin) indexProvider {
	return indexProvider{name: name, index: func(ctx context.Context, request symbolindex.Request) (symbolindex.Result, error) {
		params, err := json.Marshal(struct {
			ID int `json:"id"`
			symbolindex.Request
		}{1, request})
		if err != nil {
			return symbolindex.Result{}, err
		}
		raw, err := plugin.ExecuteLSP(ctx, symbolindex.Method, params)
		if err != nil {
			return symbolindex.Result{}, err
		}
		var response struct {
			Result *symbolindex.Result `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			return symbolindex.Result{}, fmt.Errorf("invalid symbol index answer: %w", err)
		}
		if response.Error != nil {
			return symbolindex.Result{}, fmt.Errorf("symbol index: %s", response.Error.Message)
		}
		if response.Result == nil {
			return symbolindex.Result{}, nil
		}
		return *response.Result, nil
	}}
}

// indexTask asks providers for documents. An empty providers set means
// every provider.
type indexTask struct {
	request   symbolindex.Request
	providers map[string]bool
	// seq is the number of the last change the task covers.
	seq uint64
}

// symbolIndexService keeps the index current. One worker asks the
// providers, in the order changes arrive, so a later answer always replaces
// an earlier one; queries wait until every change made before them is
// indexed.
type symbolIndexService struct {
	ctx       context.Context
	index     *symbolindex.Index
	providers []indexProvider

	mu      sync.Mutex
	changed *sync.Cond
	pending []indexTask
	queued  uint64
	done    uint64
	closed  bool
}

func newSymbolIndexService(ctx context.Context, providers []indexProvider) *symbolIndexService {
	service := &symbolIndexService{ctx: ctx, index: symbolindex.New(), providers: providers}
	service.changed = sync.NewCond(&service.mu)
	if len(providers) > 0 {
		go service.run()
	}
	return service
}

// enqueue asks providers (every provider when none is named) for request.
func (s *symbolIndexService) enqueue(request symbolindex.Request, providers ...string) {
	if len(s.providers) == 0 {
		return
	}
	task := indexTask{request: request}
	if len(providers) > 0 {
		task.providers = make(map[string]bool)
		for _, name := range providers {
			task.providers[name] = true
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	// Edits arrive faster than they are indexed: a document change joins
	// the last waiting change for the same providers.
	s.queued++
	task.seq = s.queued
	if last := len(s.pending) - 1; last >= 0 && !task.request.Workspace && !s.pending[last].request.Workspace && sameProviders(s.pending[last].providers, task.providers) {
		s.pending[last].request.URIs = appendNew(s.pending[last].request.URIs, task.request.URIs...)
		s.pending[last].seq = task.seq
	} else {
		s.pending = append(s.pending, task)
	}
	s.changed.Broadcast()
}

// wait returns once every change enqueued before it is in the index.
func (s *symbolIndexService) wait() {
	s.mu.Lock()
	defer s.mu.Unlock()
	target := s.queued
	for s.done < target && !s.closed {
		s.changed.Wait()
	}
}

func (s *symbolIndexService) close() {
	s.mu.Lock()
	s.closed = true
	s.changed.Broadcast()
	s.mu.Unlock()
}

func (s *symbolIndexService) run() {
	for {
		s.mu.Lock()
		for len(s.pending) == 0 && !s.closed {
			s.changed.Wait()
		}
		if s.closed {
			s.mu.Unlock()
			return
		}
		task := s.pending[0]
		s.pending = s.pending[1:]
		s.mu.Unlock()

		// Providers answer concurrently; each one's answer replaces only
		// its own documents.
		var wg sync.WaitGroup
		for _, provider := range s.providers {
			if task.providers != nil && !task.providers[provider.name] {
				continue
			}
			wg.Add(1)
			go func(provider indexProvider) {
				defer wg.Done()
				result, err := provider.index(s.ctx, task.request)
				if err != nil {
					fmt.Fprintf(os.Stderr, "symbol index: %s: %v\n", provider.name, err)
					return
				}
				s.index.Apply(provider.name, result)
			}(provider)
		}
		wg.Wait()

		s.mu.Lock()
		s.done = task.seq
		s.changed.Broadcast()
		s.mu.Unlock()
	}
}

func sameProviders(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for name := range a {
		if !b[name] {
			return false
		}
	}
	return true
}

func appendNew(values []string, more ...string) []string {
	for _, value := range more {
		found := false
		for _, existing := range values {
			found = found || existing == value
		}
		if !found {
			values = append(values, value)
		}
	}
	return values
}

// indexMethods are the requests the index answers, beside the responders.
var indexMethods = map[string]bool{
	"textDocument/definition":           true,
	"textDocument/references":           true,
	"textDocument/documentHighlight":    true,
	"textDocument/prepareCallHierarchy": true,
	"callHierarchy/incomingCalls":       true,
	"callHierarchy/outgoingCalls":       true,
}

// navigationMethods are the requests that wait for every earlier change to
// be indexed before the responders see them, so plugins and the index
// answer from the same state. Completion, hover, and the other requests a
// keystroke sends do not wait.
var navigationMethods = map[string]bool{
	"textDocument/definition":           true,
	"textDocument/references":           true,
	"textDocument/documentHighlight":    true,
	"textDocument/prepareCallHierarchy": true,
	"callHierarchy/incomingCalls":       true,
	"callHierarchy/outgoingCalls":       true,
	"textDocument/prepareRename":        true,
	"textDocument/rename":               true,
	"textDocument/codeLens":             true,
	"workspace/symbol":                  true,
}

// LSP DocumentHighlightKind values.
const (
	highlightRead  = 2
	highlightWrite = 3
)

type indexQuery struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position symbolindex.Position `json:"position"`
	Context  struct {
		IncludeDeclaration bool `json:"includeDeclaration"`
	} `json:"context"`
	Item struct {
		Data struct {
			Symbol string `json:"symbol"`
		} `json:"data"`
	} `json:"item"`
}

// answer returns the index's response to a request, once every earlier
// change is indexed, or nil when it has nothing to add. Definition and
// references add semantic locations to the responders'; a document
// highlight comes from the index alone, empty rather than a word match.
func (s *symbolIndexService) answer(method string, id json.RawMessage, params json.RawMessage) []byte {
	if !indexMethods[method] {
		return nil
	}
	var query indexQuery
	if json.Unmarshal(params, &query) != nil {
		return nil
	}
	s.wait()
	uri := query.TextDocument.URI
	switch method {
	case "callHierarchy/incomingCalls":
		return rpcResult(id, s.incomingCalls(query.Item.Data.Symbol))
	case "callHierarchy/outgoingCalls":
		return rpcResult(id, s.outgoingCalls(query.Item.Data.Symbol))
	}
	occurrence, found := s.index.At(uri, query.Position)
	switch method {
	case "textDocument/documentHighlight":
		highlights := []map[string]any{}
		if found {
			for _, declaration := range s.index.Declarations(occurrence.Symbol) {
				if declaration.URI == uri {
					highlights = append(highlights, map[string]any{"range": declaration.Range, "kind": highlightWrite})
				}
			}
			for _, use := range s.index.References(occurrence.Symbol) {
				if use.URI == uri {
					kind := highlightRead
					if use.Kind == symbolindex.WriteReference {
						kind = highlightWrite
					}
					highlights = append(highlights, map[string]any{"range": use.Range, "kind": kind})
				}
			}
		}
		return rpcResult(id, highlights)
	case "textDocument/definition":
		if !found {
			return nil
		}
		locations := []symbolindex.Location{}
		for _, declaration := range s.index.Declarations(occurrence.Symbol) {
			locations = append(locations, symbolindex.Location{URI: declaration.URI, Range: declaration.Range})
		}
		if len(locations) == 0 {
			return nil
		}
		return rpcResult(id, locations)
	case "textDocument/references":
		if !found {
			return nil
		}
		locations := []symbolindex.Location{}
		if query.Context.IncludeDeclaration {
			for _, declaration := range s.index.Declarations(occurrence.Symbol) {
				locations = append(locations, symbolindex.Location{URI: declaration.URI, Range: declaration.Range})
			}
		}
		for _, use := range s.index.References(occurrence.Symbol) {
			locations = append(locations, symbolindex.Location{URI: use.URI, Range: use.Range})
		}
		return rpcResult(id, locations)
	case "textDocument/prepareCallHierarchy":
		if !found {
			return rpcResult(id, nil)
		}
		item, ok := s.callItem(occurrence.Symbol)
		if !ok {
			return rpcResult(id, nil)
		}
		return rpcResult(id, []any{item})
	}
	return nil
}

// callItem is a CallHierarchyItem for a component or story.
func (s *symbolIndexService) callItem(id string) (map[string]any, bool) {
	for _, declaration := range s.index.Declarations(id) {
		kind := 0
		switch declaration.Kind {
		case symbolindex.Component:
			kind = 5 // Class
		case symbolindex.Story:
			kind = 12 // Function
		default:
			continue
		}
		return map[string]any{
			"name": declaration.Name, "kind": kind, "detail": declaration.Detail,
			"uri": declaration.URI, "range": declaration.Range, "selectionRange": declaration.Range,
			"data": map[string]any{"symbol": id},
		}, true
	}
	return nil, false
}

// incomingCalls groups a component's call sites by the component or story
// that makes them.
func (s *symbolIndexService) incomingCalls(id string) []any {
	calls := []any{}
	ranges := make(map[string][]symbolindex.Range)
	var callers []string
	for _, use := range s.index.References(id) {
		if use.Kind != symbolindex.CallReference || use.From == "" {
			continue
		}
		if _, seen := ranges[use.From]; !seen {
			callers = append(callers, use.From)
		}
		ranges[use.From] = append(ranges[use.From], use.Range)
	}
	for _, caller := range callers {
		if item, ok := s.callItem(caller); ok {
			calls = append(calls, map[string]any{"from": item, "fromRanges": ranges[caller]})
		}
	}
	return calls
}

// outgoingCalls groups the calls a component or story makes by callee.
func (s *symbolIndexService) outgoingCalls(id string) []any {
	calls := []any{}
	ranges := make(map[string][]symbolindex.Range)
	var callees []string
	for _, use := range s.index.Outgoing(id) {
		if use.Kind != symbolindex.CallReference {
			continue
		}
		if _, seen := ranges[use.Symbol]; !seen {
			callees = append(callees, use.Symbol)
		}
		ranges[use.Symbol] = append(ranges[use.Symbol], use.Range)
	}
	for _, callee := range callees {
		if item, ok := s.callItem(callee); ok {
			calls = append(calls, map[string]any{"to": item, "fromRanges": ranges[callee]})
		}
	}
	return calls
}

// indexProviders are the plugins that declare symbol-index and the
// adapters that implement IndexProvider.
func indexProviders(plugins []*wasm.Plugin, adapters []Adapter) []indexProvider {
	var providers []indexProvider
	for _, plugin := range plugins {
		if plugin.Manifest().Has(wasm.CapabilitySymbolIndex) {
			providers = append(providers, pluginIndexProvider(plugin.Name(), plugin))
		}
	}
	for _, adapter := range adapters {
		if provider, ok := adapter.(IndexProvider); ok {
			providers = append(providers, indexProvider{name: fmt.Sprintf("%T", adapter), index: provider.IndexDocuments})
		}
	}
	return providers
}

// changedURIs returns the documents a notification changes: the open,
// edited, or closed document, or the files the editor reports changed on
// disk.
func changedURIs(method string, params json.RawMessage) []string {
	switch method {
	case "textDocument/didOpen", "textDocument/didChange", "textDocument/didClose":
		var document struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		}
		if json.Unmarshal(params, &document) == nil && document.TextDocument.URI != "" {
			return []string{document.TextDocument.URI}
		}
	case "workspace/didChangeWatchedFiles":
		var watched struct {
			Changes []struct {
				URI string `json:"uri"`
			} `json:"changes"`
		}
		if json.Unmarshal(params, &watched) == nil {
			var uris []string
			for _, change := range watched.Changes {
				uris = append(uris, change.URI)
			}
			return uris
		}
	}
	return nil
}
