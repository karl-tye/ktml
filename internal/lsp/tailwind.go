package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/karl-tye/ktml/internal/config"
	"github.com/karl-tye/ktml/internal/tailwind"
	"github.com/karl-tye/ktml/internal/textposition"
)

// Adapter is a native integration served beside the wasm plugins, such as an
// external language server behind a native adapter. It answers each client
// message like a plugin and may publish notifications of its own, such as
// diagnostics that arrive later.
type Adapter interface {
	ExecuteLSP(ctx context.Context, method string, payload []byte) ([]byte, error)
	Published() <-chan []byte
	Close() error
}

// TailwindPinnedVersion is the @tailwindcss/language-server release KTML is
// tested with.
const TailwindPinnedVersion = "0.16.0"

const (
	tailwindSource          = "tailwind"
	tailwindCompletionLimit = 300
	tailwindSymbolLimit     = 1000
)

// TailwindAdapter runs the Tailwind CSS language server over virtual
// documents of the open KTML sources. A virtual document keeps only the
// class lists, at their original LSP positions (tailwind.VirtualDocument),
// under the KTML document's own URI, so requests and results pass through
// unmapped; the adapter only keeps them inside class lists. The class
// extractor also serves dynamic-class diagnostics, theme definitions, and
// class symbols, which work without the server.
type TailwindAdapter struct {
	root     string
	command  []string
	settings map[string]any

	mu        sync.Mutex
	documents map[string]*tailwindDocument
	server    *childServer
	startErr  error
	warned    bool
	// outbox keeps document notifications to the server in order without
	// blocking the host.
	outbox chan func(*childServer)

	published chan []byte
	stop      chan struct{}
	closeOnce sync.Once

	index *tailwindIndex
}

type tailwindDocument struct {
	uri        string
	source     []byte
	extraction tailwind.Extraction
	// external is the server's last diagnostics for the document.
	external []json.RawMessage
}

// NewTailwindAdapter returns the adapter for the workspace root. The server
// starts with the first KTML document.
func NewTailwindAdapter(root string, settings *config.Tailwind) *TailwindAdapter {
	adapter := &TailwindAdapter{
		root:      root,
		command:   settings.TailwindCommand(),
		documents: make(map[string]*tailwindDocument),
		outbox:    make(chan func(*childServer), 256),
		published: make(chan []byte, 16),
		stop:      make(chan struct{}),
		index:     newTailwindIndex(root),
	}
	if settings != nil {
		adapter.settings = settings.Settings
	}
	return adapter
}

// Published delivers the adapter's own notifications.
func (a *TailwindAdapter) Published() <-chan []byte { return a.published }

// Close stops the language server.
func (a *TailwindAdapter) Close() error {
	var err error
	a.closeOnce.Do(func() {
		close(a.stop)
		a.mu.Lock()
		server := a.server
		a.mu.Unlock()
		if server != nil {
			err = server.Close()
		}
	})
	return err
}

func (a *TailwindAdapter) publish(notification []byte) {
	select {
	case a.published <- notification:
	case <-a.stop:
	}
}

// ExecuteLSP answers one client message. Requests carry their id in the
// payload, as plugins receive them.
func (a *TailwindAdapter) ExecuteLSP(ctx context.Context, method string, payload []byte) ([]byte, error) {
	var params struct {
		ID           json.RawMessage `json:"id"`
		TextDocument struct {
			URI     string `json:"uri"`
			Text    string `json:"text"`
			Version int    `json:"version"`
		} `json:"textDocument"`
		ContentChanges []struct {
			Text string `json:"text"`
		} `json:"contentChanges"`
		Position struct {
			Line      int `json:"line"`
			Character int `json:"character"`
		} `json:"position"`
		Query string `json:"query"`
		Range struct {
			Start struct {
				Line      int `json:"line"`
				Character int `json:"character"`
			} `json:"start"`
		} `json:"range"`
	}
	if len(payload) > 0 && json.Unmarshal(payload, &params) != nil {
		return nil, nil
	}
	uri := params.TextDocument.URI
	switch method {
	case "textDocument/didOpen":
		if !strings.HasSuffix(uri, ".ktml") {
			return nil, nil
		}
		return a.update(uri, []byte(params.TextDocument.Text), params.TextDocument.Version, true), nil
	case "textDocument/didChange":
		if len(params.ContentChanges) == 0 || a.document(uri) == nil {
			return nil, nil
		}
		text := params.ContentChanges[len(params.ContentChanges)-1].Text
		return a.update(uri, []byte(text), params.TextDocument.Version, false), nil
	case "textDocument/didClose":
		return a.close(uri), nil
	case "textDocument/completion":
		return rpcResult(params.ID, a.completion(ctx, uri, payload, params.Position.Line, params.Position.Character)), nil
	case "textDocument/hover":
		return rpcResult(params.ID, a.forwardInList(ctx, method, uri, payload, params.Position.Line, params.Position.Character)), nil
	case "textDocument/codeAction":
		return rpcResult(params.ID, a.codeAction(ctx, uri, payload, params.Range.Start.Line, params.Range.Start.Character)), nil
	case "textDocument/definition":
		return rpcResult(params.ID, a.definition(ctx, uri, params.Position.Line, params.Position.Character)), nil
	case "workspace/symbol":
		return rpcResult(params.ID, a.symbols(params.Query)), nil
	}
	// Other messages are the plugins' alone.
	return nil, nil
}

func (a *TailwindAdapter) document(uri string) *tailwindDocument {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.documents[uri]
}

func (a *TailwindAdapter) update(uri string, source []byte, version int, open bool) []byte {
	extraction := tailwind.Extract(source)
	virtual := tailwind.VirtualDocument(source, extraction)
	a.mu.Lock()
	document := a.documents[uri]
	if document == nil {
		document = &tailwindDocument{uri: uri}
		a.documents[uri] = document
	}
	document.source, document.extraction = source, extraction
	a.mu.Unlock()

	server := a.ensureServer()
	if server != nil {
		if open {
			a.send(func(server *childServer) {
				_ = server.Notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "languageId": "html", "version": version, "text": virtual}})
			})
		} else {
			a.send(func(server *childServer) {
				_ = server.Notify("textDocument/didChange", map[string]any{"textDocument": map[string]any{"uri": uri, "version": version}, "contentChanges": []any{map[string]any{"text": virtual}}})
			})
		}
	}
	return a.diagnostics(uri)
}

func (a *TailwindAdapter) close(uri string) []byte {
	a.mu.Lock()
	_, tracked := a.documents[uri]
	delete(a.documents, uri)
	server := a.server
	a.mu.Unlock()
	if !tracked {
		return nil
	}
	if server != nil {
		a.send(func(server *childServer) {
			_ = server.Notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": uri}})
		})
	}
	notification, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": publishDiagnosticsMethod, "params": map[string]any{"uri": uri, "diagnostics": []any{}}})
	return notification
}

func (a *TailwindAdapter) send(message func(*childServer)) {
	select {
	case a.outbox <- message:
	case <-a.stop:
	}
}

// diagnostics returns the publishDiagnostics notification of a document:
// its dynamic class construction and the server's last diagnostics that
// still fall inside a class list.
func (a *TailwindAdapter) diagnostics(uri string) []byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	document := a.documents[uri]
	if document == nil {
		return nil
	}
	diagnostics := []any{}
	for _, dynamic := range document.extraction.Dynamic {
		severity := 3
		if dynamic.Partial {
			severity = 2
		}
		diagnostics = append(diagnostics, map[string]any{
			"range":    byteRange(document.source, dynamic.Start, dynamic.End),
			"severity": severity,
			"code":     tailwind.DynamicCode,
			"source":   tailwindSource,
			"message":  dynamic.Message(string(document.source[dynamic.Start:dynamic.End])),
		})
	}
	for _, diagnostic := range document.external {
		var ranged struct {
			Range lspRange `json:"range"`
		}
		if json.Unmarshal(diagnostic, &ranged) != nil {
			continue
		}
		offset := textposition.Offset(document.source, ranged.Range.Start.Line, ranged.Range.Start.Character)
		if _, ok := document.extraction.ListAt(offset); ok {
			diagnostics = append(diagnostics, diagnostic)
		}
	}
	notification, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": publishDiagnosticsMethod, "params": map[string]any{"uri": uri, "diagnostics": diagnostics}})
	return notification
}

type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

func byteRange(source []byte, start, end int) lspRange {
	var converted lspRange
	converted.Start.Line, converted.Start.Character = textposition.Position(source, start)
	converted.End.Line, converted.End.Character = textposition.Position(source, end)
	return converted
}

// ensureServer starts the language server once. A server that cannot start
// is reported once, as a warning message; KTML's own Tailwind features keep
// working.
func (a *TailwindAdapter) ensureServer() *childServer {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.server != nil || a.startErr != nil {
		if a.server != nil && a.server.Err() != nil {
			a.warnLocked(a.server.Err())
			return nil
		}
		return a.server
	}
	root := (&url.URL{Scheme: "file", Path: filepath.ToSlash(a.root)}).String()
	initialize := map[string]any{
		"processId": os.Getpid(),
		"rootUri":   root,
		"workspaceFolders": []any{
			map[string]any{"uri": root, "name": filepath.Base(a.root)},
		},
		"capabilities": map[string]any{
			"workspace": map[string]any{"configuration": true, "workspaceFolders": true},
			"textDocument": map[string]any{
				"completion":         map[string]any{"completionItem": map[string]any{"snippetSupport": true}},
				"hover":              map[string]any{"contentFormat": []string{"markdown", "plaintext"}},
				"codeAction":         map[string]any{"codeActionLiteralSupport": map[string]any{"codeActionKind": map[string]any{"valueSet": []string{"quickfix"}}}},
				"publishDiagnostics": map[string]any{"relatedInformation": true},
			},
		},
	}
	server, err := startChildServer("tailwindcss-language-server", a.command, a.root, initialize, a.serverRequest, a.serverNotification)
	if err != nil {
		a.startErr = err
		a.warnLocked(err)
		return nil
	}
	a.server = server
	go a.drain(server)
	go func() {
		select {
		case <-server.Done():
			if err := server.Err(); err != nil && err.Error() != "closed" {
				a.mu.Lock()
				a.warnLocked(err)
				a.mu.Unlock()
			}
		case <-a.stop:
		}
	}()
	return server
}

func (a *TailwindAdapter) drain(server *childServer) {
	for {
		select {
		case message := <-a.outbox:
			message(server)
		case <-a.stop:
			return
		}
	}
}

// warnLocked tells the user once that the Tailwind server is unavailable.
func (a *TailwindAdapter) warnLocked(err error) {
	if a.warned {
		return
	}
	a.warned = true
	message := fmt.Sprintf("Tailwind CSS language server unavailable (%v). Install it with `npm install -g @tailwindcss/language-server@%s` (it needs Node.js), or set tailwind.command in ktml.config.json. KTML class checks still run.", err, TailwindPinnedVersion)
	fmt.Fprintln(os.Stderr, "ktml lsp: "+message)
	notification, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "window/showMessage", "params": map[string]any{"type": 2, "message": message}})
	go a.publish(notification)
}

// serverRequest answers the server's requests: its configuration, and
// registrations, which the adapter accepts and ignores.
func (a *TailwindAdapter) serverRequest(method string, params json.RawMessage) any {
	if method != "workspace/configuration" {
		return nil
	}
	var request struct {
		Items []struct {
			Section string `json:"section"`
		} `json:"items"`
	}
	_ = json.Unmarshal(params, &request)
	results := make([]any, len(request.Items))
	for index, item := range request.Items {
		switch item.Section {
		case "tailwindCSS":
			results[index] = a.tailwindSettings()
		case "editor":
			results[index] = map[string]any{"tabSize": 2}
		}
	}
	return results
}

// tailwindSettings are the server's settings: KTML documents are HTML, and
// the project's settings override the rest.
func (a *TailwindAdapter) tailwindSettings() map[string]any {
	settings := map[string]any{
		"includeLanguages": map[string]any{"ktml": "html"},
		"emmetCompletions": false,
		"validate":         true,
	}
	maps.Copy(settings, a.settings)
	return settings
}

func (a *TailwindAdapter) serverNotification(method string, params json.RawMessage) {
	switch method {
	case publishDiagnosticsMethod:
		var published struct {
			URI         string            `json:"uri"`
			Diagnostics []json.RawMessage `json:"diagnostics"`
		}
		if json.Unmarshal(params, &published) != nil {
			return
		}
		a.mu.Lock()
		document := a.documents[published.URI]
		if document != nil {
			document.external = published.Diagnostics
		}
		a.mu.Unlock()
		if document != nil {
			if notification := a.diagnostics(published.URI); notification != nil {
				a.publish(notification)
			}
		}
	case "window/logMessage", "window/showMessage":
		var logged struct {
			Type    int    `json:"type"`
			Message string `json:"message"`
		}
		if json.Unmarshal(params, &logged) == nil && logged.Type <= 2 {
			fmt.Fprintln(os.Stderr, "tailwindcss-language-server: "+logged.Message)
		}
	}
}

// listAt returns a tracked document and whether the position is inside one
// of its class lists.
func (a *TailwindAdapter) listAt(uri string, line, character int) (*tailwindDocument, int, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	document := a.documents[uri]
	if document == nil {
		return nil, 0, false
	}
	offset := textposition.Offset(document.source, line, character)
	_, ok := document.extraction.ListAt(offset)
	return document, offset, ok
}

func (a *TailwindAdapter) running() *childServer {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.server == nil || a.server.Err() != nil {
		return nil
	}
	return a.server
}

// forwardInList forwards a positional request inside a class list and
// returns the server's result unchanged: positions are the same in both
// documents.
func (a *TailwindAdapter) forwardInList(ctx context.Context, method, uri string, payload []byte, line, character int) any {
	if _, _, ok := a.listAt(uri, line, character); !ok {
		return nil
	}
	server := a.running()
	if server == nil {
		return nil
	}
	result, err := server.Request(ctx, method, withoutID(payload), 5*time.Second)
	if err != nil || len(result) == 0 {
		return nil
	}
	return result
}

// withoutID removes the id the host adds to request params.
func withoutID(payload []byte) json.RawMessage {
	var members map[string]json.RawMessage
	if json.Unmarshal(payload, &members) != nil {
		return payload
	}
	delete(members, "id")
	result, _ := json.Marshal(members)
	return result
}

// completion forwards completion inside a class list. The server returns
// every class of the project, megabytes per request, so the adapter keeps
// the items that match the text being typed, at most
// tailwindCompletionLimit of them; a truncated list is incomplete, so the
// client asks again as the user types. VS Code commands are removed.
func (a *TailwindAdapter) completion(ctx context.Context, uri string, payload []byte, line, character int) any {
	document, offset, ok := a.listAt(uri, line, character)
	if !ok {
		return nil
	}
	server := a.running()
	if server == nil {
		return nil
	}
	raw, err := server.Request(ctx, "textDocument/completion", withoutID(payload), 10*time.Second)
	if err != nil || len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var list struct {
		IsIncomplete bool             `json:"isIncomplete"`
		Items        []map[string]any `json:"items"`
	}
	if json.Unmarshal(raw, &list) != nil {
		if json.Unmarshal(raw, &list.Items) != nil {
			return nil
		}
	}
	wordStart := offset
	for wordStart > 0 && !strings.ContainsRune(" \t\r\n\"'`", rune(document.source[wordStart-1])) {
		wordStart--
	}
	word := strings.ToLower(string(document.source[wordStart:offset]))
	var kept []map[string]any
	for _, item := range list.Items {
		typed := word
		if start, ok := completionEditStart(item); ok {
			if editStart := textposition.Offset(document.source, start.Line, start.Character); editStart <= offset {
				typed = strings.ToLower(string(document.source[editStart:offset]))
			}
		}
		filter, _ := item["filterText"].(string)
		if filter == "" {
			filter, _ = item["label"].(string)
		}
		if !strings.HasPrefix(strings.ToLower(filter), typed) {
			continue
		}
		delete(item, "command")
		kept = append(kept, item)
	}
	sort.SliceStable(kept, func(i, j int) bool {
		first, _ := kept[i]["sortText"].(string)
		second, _ := kept[j]["sortText"].(string)
		return first < second
	})
	incomplete := list.IsIncomplete
	if len(kept) > tailwindCompletionLimit {
		kept, incomplete = kept[:tailwindCompletionLimit], true
	}
	if kept == nil {
		kept = []map[string]any{}
	}
	return map[string]any{"isIncomplete": incomplete, "items": kept}
}

func completionEditStart(item map[string]any) (lspPosition, bool) {
	edit, ok := item["textEdit"].(map[string]any)
	if !ok {
		return lspPosition{}, false
	}
	editRange, ok := edit["range"].(map[string]any)
	if !ok {
		editRange, ok = edit["insert"].(map[string]any)
	}
	if !ok {
		return lspPosition{}, false
	}
	start, ok := editRange["start"].(map[string]any)
	if !ok {
		return lspPosition{}, false
	}
	line, _ := start["line"].(float64)
	character, _ := start["character"].(float64)
	return lspPosition{Line: int(line), Character: int(character)}, true
}

// codeAction forwards a code action request inside a class list and keeps
// the actions whose edits stay inside the document's class lists.
func (a *TailwindAdapter) codeAction(ctx context.Context, uri string, payload []byte, line, character int) any {
	document, _, ok := a.listAt(uri, line, character)
	if !ok {
		return nil
	}
	server := a.running()
	if server == nil {
		return nil
	}
	raw, err := server.Request(ctx, "textDocument/codeAction", withoutID(payload), 5*time.Second)
	if err != nil {
		return nil
	}
	var actions []struct {
		Edit *struct {
			Changes map[string][]struct {
				Range lspRange `json:"range"`
			} `json:"changes"`
		} `json:"edit"`
	}
	var rawActions []json.RawMessage
	if json.Unmarshal(raw, &actions) != nil || json.Unmarshal(raw, &rawActions) != nil {
		return nil
	}
	a.mu.Lock()
	extraction, source := document.extraction, document.source
	a.mu.Unlock()
	kept := []json.RawMessage{}
	for index, action := range actions {
		if action.Edit == nil {
			continue
		}
		inside := true
		for editURI, edits := range action.Edit.Changes {
			for _, edit := range edits {
				start := textposition.Offset(source, edit.Range.Start.Line, edit.Range.Start.Character)
				end := textposition.Offset(source, edit.Range.End.Line, edit.Range.End.Character)
				list, ok := extraction.ListAt(start)
				if editURI != uri || !ok || end > list.End {
					inside = false
				}
			}
		}
		if inside {
			kept = append(kept, rawActions[index])
		}
	}
	return kept
}

// definition navigates from a class token to its theme: the declarations of
// the theme variables its CSS uses, which the server's hover reports, and a
// custom @utility of the same name.
func (a *TailwindAdapter) definition(ctx context.Context, uri string, line, character int) any {
	a.mu.Lock()
	document := a.documents[uri]
	var token tailwind.Token
	found := false
	if document != nil {
		token, found = document.extraction.TokenAt(textposition.Offset(document.source, line, character))
	}
	a.mu.Unlock()
	if !found {
		return nil
	}
	var variables []string
	if server := a.running(); server != nil {
		hoverParams := map[string]any{"textDocument": map[string]any{"uri": uri}, "position": map[string]any{"line": line, "character": character}}
		if raw, err := server.Request(ctx, "textDocument/hover", hoverParams, 5*time.Second); err == nil {
			variables = themeVariables(raw)
		}
	}
	locations := findThemeDefinitions(a.root, utilityName(token.Class), variables)
	if len(locations) == 0 {
		return nil
	}
	return locations
}

// symbols returns the class tokens that contain the query as workspace
// symbols named like CSS selectors (.bg-blue), so class usage is searchable
// across the workspace. An empty query returns none: every class of a
// project would drown the other symbols.
func (a *TailwindAdapter) symbols(query string) any {
	query = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(query), "."))
	if query == "" {
		return nil
	}
	a.mu.Lock()
	open := make(map[string]*tailwindDocument, len(a.documents))
	maps.Copy(open, a.documents)
	a.mu.Unlock()
	symbols := []any{}
	add := func(uri string, source []byte, extraction tailwind.Extraction) {
		for _, token := range extraction.Tokens {
			if len(symbols) >= tailwindSymbolLimit {
				return
			}
			if strings.Contains(strings.ToLower(token.Class), query) {
				symbols = append(symbols, map[string]any{
					"name":          "." + token.Class,
					"kind":          5,
					"containerName": "Tailwind class",
					"location":      map[string]any{"uri": uri, "range": byteRange(source, token.Start, token.End)},
				})
			}
		}
	}
	uris := slices.Sorted(maps.Keys(open))
	for _, uri := range uris {
		add(uri, open[uri].source, open[uri].extraction)
	}
	for _, file := range a.index.files() {
		uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(file.Path)}).String()
		if open[uri] == nil {
			add(uri, file.Source, file.Extraction)
		}
	}
	return symbols
}
