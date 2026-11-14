// Package alpine is the first-party Alpine.js plugin of the KTML language
// server. It serves the internal/alpine analyzer over LSP: diagnostics,
// completion, hover, definitions, references, scoped rename, and Alpine
// scopes in workspace symbols.
package alpine

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/karl-tye/ktml/internal/alpine"
	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/symbolindex"
	"github.com/karl-tye/ktml/internal/textposition"
)

var state = struct {
	sync.Mutex
	workspaces map[string]*compiler.Workspace
	versions   map[string]int
	// open holds the paths of open documents, which exist without a file.
	open map[string]bool
}{workspaces: make(map[string]*compiler.Workspace), versions: make(map[string]int), open: make(map[string]bool)}

// HandleLSP answers one LSP message, or returns nil for methods the plugin
// does not serve.
func HandleLSP(method string, payload []byte) ([]byte, error) {
	switch method {
	case "textDocument/didOpen", "textDocument/didChange", "textDocument/didClose":
		return updateDocument(method, payload)
	case "textDocument/completion":
		return completion(payload)
	case "textDocument/hover":
		return hover(payload)
	case "textDocument/definition":
		return definition(payload)
	case "textDocument/references":
		return references(payload)
	case "textDocument/prepareRename":
		return prepareRename(payload)
	case "textDocument/rename":
		return rename(payload)
	case "workspace/symbol":
		return workspaceSymbols(payload)
	case symbolindex.Method:
		return indexDocuments(payload)
	}
	return nil, nil
}

type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type positionParams struct {
	ID           json.RawMessage `json:"id"`
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position position `json:"position"`
	NewName  string   `json:"newName"`
	Context  struct {
		IncludeDeclaration bool `json:"includeDeclaration"`
	} `json:"context"`
}

func uriPath(uri string) string {
	if parsed, err := url.Parse(uri); err == nil && parsed.Scheme == "file" {
		return parsed.Path
	}
	return strings.TrimPrefix(uri, "file://")
}

func pathURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

// workspaceRoot is the nearest directory above path with ktml.config.json,
// or the file's directory.
func workspaceRoot(path string) string {
	dir := filepath.Dir(path)
	for current := dir; ; {
		if _, err := os.Stat(filepath.Join(current, "ktml.config.json")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return dir
		}
		current = parent
	}
}

func workspaceFor(path string) *compiler.Workspace {
	root := workspaceRoot(path)
	state.Lock()
	defer state.Unlock()
	workspace := state.workspaces[root]
	if workspace == nil {
		workspace = compiler.NewWorkspace(root)
		state.workspaces[root] = workspace
	}
	return workspace
}

// analysis is one document's snapshot and Alpine model.
type analysis struct {
	path     string
	snapshot *compiler.Snapshot
	source   []byte
	document *alpine.Document
}

func analyze(path string) (*analysis, bool) {
	snapshot, err := workspaceFor(path).Load(path)
	if err != nil || snapshot == nil {
		return nil, false
	}
	return analyzeIn(snapshot, path)
}

func analyzeIn(snapshot *compiler.Snapshot, path string) (*analysis, bool) {
	source := snapshot.Source(path)
	document := snapshot.Document(path)
	if source == nil || document == nil {
		return nil, false
	}
	return &analysis{path: path, snapshot: snapshot, source: source, document: alpine.Analyze(source, document, alpine.SnapshotEnv(snapshot, path))}, true
}

func (a *analysis) rangeAt(start, end int) map[string]any {
	startLine, startCharacter := textposition.Position(a.source, start)
	endLine, endCharacter := textposition.Position(a.source, end)
	return map[string]any{
		"start": map[string]any{"line": startLine, "character": startCharacter},
		"end":   map[string]any{"line": endLine, "character": endCharacter},
	}
}

func (a *analysis) offset(p position) int {
	return textposition.Offset(a.source, p.Line, p.Character)
}

func result(id json.RawMessage, value any) ([]byte, error) {
	return json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": value})
}

func rpcError(id json.RawMessage, message string) ([]byte, error) {
	return json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32803, "message": message}})
}

func updateDocument(method string, payload []byte) ([]byte, error) {
	var params struct {
		TextDocument struct {
			URI     string `json:"uri"`
			Text    string `json:"text"`
			Version int    `json:"version"`
		} `json:"textDocument"`
		ContentChanges []struct {
			Text string `json:"text"`
		} `json:"contentChanges"`
	}
	if err := json.Unmarshal(payload, &params); err != nil {
		return nil, err
	}
	uri := params.TextDocument.URI
	path := uriPath(uri)
	if !strings.HasSuffix(path, ".ktml") {
		return nil, nil
	}
	workspace := workspaceFor(path)
	diagnostics := make([]map[string]any, 0)
	state.Lock()
	state.open[path] = method != "textDocument/didClose"
	state.Unlock()
	if method == "textDocument/didClose" {
		_, _ = workspace.Close(path)
		return publish(uri, diagnostics)
	}
	text := params.TextDocument.Text
	if method == "textDocument/didChange" && len(params.ContentChanges) > 0 {
		text = params.ContentChanges[len(params.ContentChanges)-1].Text
	}
	state.Lock()
	version := params.TextDocument.Version
	if version == 0 {
		state.versions[path]++
		version = state.versions[path]
	}
	state.Unlock()
	snapshot, err := workspace.Update(path, version, []byte(text))
	if err != nil || snapshot == nil {
		return publish(uri, diagnostics)
	}
	if current, ok := analyzeIn(snapshot, path); ok {
		for _, diagnostic := range current.document.Diagnostics {
			diagnostics = append(diagnostics, map[string]any{
				"range": current.rangeAt(diagnostic.Start, diagnostic.End), "severity": int(diagnostic.Severity),
				"code": diagnostic.Code, "source": "alpine", "message": diagnostic.Message,
			})
		}
	}
	return publish(uri, diagnostics)
}

func publish(uri string, diagnostics []map[string]any) ([]byte, error) {
	return json.Marshal(map[string]any{
		"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics",
		"params": map[string]any{"uri": uri, "diagnostics": diagnostics},
	})
}

func completion(payload []byte) ([]byte, error) {
	var params positionParams
	if err := json.Unmarshal(payload, &params); err != nil {
		return nil, err
	}
	empty := map[string]any{"isIncomplete": false, "items": []any{}}
	current, ok := analyze(uriPath(params.TextDocument.URI))
	if !ok {
		return result(params.ID, empty)
	}
	offset := current.offset(params.Position)
	items := make([]map[string]any, 0)
	for _, item := range current.document.Complete(current.source, offset) {
		entry := map[string]any{
			"label": item.Label, "kind": item.Kind, "detail": item.Detail,
			"textEdit": map[string]any{"range": current.rangeAt(item.Start, item.End), "newText": item.Text},
		}
		if item.Documentation != "" {
			entry["documentation"] = map[string]any{"kind": "markdown", "value": item.Documentation}
		}
		items = append(items, entry)
	}
	return result(params.ID, map[string]any{"isIncomplete": false, "items": items})
}

func hover(payload []byte) ([]byte, error) {
	var params positionParams
	if err := json.Unmarshal(payload, &params); err != nil {
		return nil, err
	}
	current, ok := analyze(uriPath(params.TextDocument.URI))
	if !ok {
		return result(params.ID, nil)
	}
	value, start, end, found := current.document.Hover(current.source, current.offset(params.Position))
	if !found {
		return result(params.ID, nil)
	}
	return result(params.ID, map[string]any{"contents": map[string]any{"kind": "markdown", "value": value}, "range": current.rangeAt(start, end)})
}

func definition(payload []byte) ([]byte, error) {
	var params positionParams
	if err := json.Unmarshal(payload, &params); err != nil {
		return nil, err
	}
	current, ok := analyze(uriPath(params.TextDocument.URI))
	if !ok {
		return result(params.ID, nil)
	}
	reference := current.document.ReferenceAt(current.offset(params.Position))
	if reference == nil || reference.Binding == nil {
		return result(params.ID, nil)
	}
	binding := reference.Binding
	return result(params.ID, map[string]any{"uri": params.TextDocument.URI, "range": current.rangeAt(binding.Start, binding.End)})
}

func references(payload []byte) ([]byte, error) {
	var params positionParams
	if err := json.Unmarshal(payload, &params); err != nil {
		return nil, err
	}
	current, ok := analyze(uriPath(params.TextDocument.URI))
	if !ok {
		return result(params.ID, nil)
	}
	binding := current.document.SymbolAt(current.offset(params.Position))
	if binding == nil {
		return result(params.ID, nil)
	}
	locations := make([]map[string]any, 0)
	if params.Context.IncludeDeclaration {
		locations = append(locations, map[string]any{"uri": params.TextDocument.URI, "range": current.rangeAt(binding.Start, binding.End)})
	}
	for _, reference := range current.document.ReferencesTo(binding) {
		locations = append(locations, map[string]any{"uri": params.TextDocument.URI, "range": current.rangeAt(reference.Start, reference.End)})
	}
	return result(params.ID, locations)
}

func prepareRename(payload []byte) ([]byte, error) {
	var params positionParams
	if err := json.Unmarshal(payload, &params); err != nil {
		return nil, err
	}
	current, ok := analyze(uriPath(params.TextDocument.URI))
	if !ok {
		return result(params.ID, nil)
	}
	offset := current.offset(params.Position)
	binding := current.document.SymbolAt(offset)
	if binding == nil {
		return result(params.ID, nil)
	}
	if reason := current.renameConflict(binding, ""); reason != "" {
		return rpcError(params.ID, reason)
	}
	start, end := binding.Start, binding.End
	if reference := current.document.ReferenceAt(offset); reference != nil && reference.Binding == binding {
		start, end = reference.Start, reference.End
	}
	return result(params.ID, map[string]any{"range": current.rangeAt(start, end), "placeholder": binding.Name})
}

func rename(payload []byte) ([]byte, error) {
	var params positionParams
	if err := json.Unmarshal(payload, &params); err != nil {
		return nil, err
	}
	current, ok := analyze(uriPath(params.TextDocument.URI))
	if !ok {
		return result(params.ID, nil)
	}
	binding := current.document.SymbolAt(current.offset(params.Position))
	if binding == nil {
		return result(params.ID, nil)
	}
	if !validName(params.NewName, binding.Kind) {
		return rpcError(params.ID, fmt.Sprintf("%q is not a valid name for an Alpine %s", params.NewName, binding.Kind))
	}
	if reason := current.renameConflict(binding, params.NewName); reason != "" {
		return rpcError(params.ID, reason)
	}
	edits := []map[string]any{{"range": current.rangeAt(binding.Start, binding.End), "newText": params.NewName}}
	for _, reference := range current.document.ReferencesTo(binding) {
		edits = append(edits, map[string]any{"range": current.rangeAt(reference.Start, reference.End), "newText": params.NewName})
	}
	return result(params.ID, map[string]any{"changes": map[string]any{params.TextDocument.URI: edits}})
}

var reservedWords = map[string]bool{
	"break": true, "case": true, "catch": true, "class": true, "const": true, "continue": true, "debugger": true,
	"default": true, "delete": true, "do": true, "else": true, "export": true, "extends": true, "false": true,
	"finally": true, "for": true, "function": true, "if": true, "import": true, "in": true, "instanceof": true,
	"new": true, "null": true, "return": true, "super": true, "switch": true, "this": true, "throw": true,
	"true": true, "try": true, "typeof": true, "var": true, "void": true, "while": true, "with": true,
	"let": true, "yield": true, "await": true, "of": true,
}

func validName(name string, kind alpine.BindingKind) bool {
	if name == "" || reservedWords[name] || strings.HasPrefix(name, "$") {
		return false
	}
	for index := 0; index < len(name); index++ {
		c := name[index]
		letter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
		if !letter && (index == 0 || c < '0' || c > '9') && !(kind == alpine.Ref && c == '-' && index > 0) {
			return false
		}
	}
	return true
}

// renameConflict explains why renaming binding is not safe, or returns "".
// Code that static analysis cannot bind may use the name: content of a
// component call or slot under the binding's scope, components called
// there, and callers' content rendered at an outlet there.
func (a *analysis) renameConflict(binding *alpine.Binding, newName string) string {
	if binding.Kind == alpine.Local {
		return ""
	}
	scope := binding.Scope
	if scope == nil || scope.Element == nil {
		return ""
	}
	line := func(offset int) int { return strings.Count(string(a.source[:offset]), "\n") + 1 }
	if newName != "" {
		for _, other := range a.document.Bindings {
			if other.Name == newName && other.Scope != nil && encloses(scope, other.Scope) {
				return fmt.Sprintf("%s is already declared at line %d in this scope; renaming would capture its references", newName, line(other.Start))
			}
		}
	}
	for _, reference := range a.document.References {
		if reference.Binding == nil && reference.Open && reference.Name == binding.Name && reference.Start >= scope.Start && reference.End <= scope.End && reference.Kind != alpine.ThisReference {
			if binding.Kind == alpine.Ref && reference.Kind != alpine.RefReference {
				continue
			}
			return fmt.Sprintf("%s at line %d may also mean %s, but it is inside content another component renders; rename it by hand", reference.Name, line(reference.Start), binding.Name)
		}
	}
	if !scope.Unknown || binding.Kind == alpine.Ref {
		return ""
	}
	var calls []string
	var outlet bool
	walkElements(scope.Element.Children, func(element *ast.ElementNode) bool {
		if component, ok := a.snapshot.Resolve(a.path, element.Name); ok {
			calls = append(calls, string(component.ID))
			return false
		}
		if len(element.Attributes) == 0 && len(element.Children) == 0 && isOutlet(a.snapshot, a.path, binding.Component, element.Name) {
			outlet = true
		}
		return true
	})
	visited := make(map[string]bool)
	for len(calls) > 0 {
		id := calls[len(calls)-1]
		calls = calls[:len(calls)-1]
		if visited[id] {
			continue
		}
		visited[id] = true
		component, ok := a.snapshot.Component(compiler.SymbolID(id))
		if !ok {
			continue
		}
		callee, ok := analyzeIn(a.snapshot, component.File)
		if !ok {
			continue
		}
		for _, reference := range callee.document.References {
			expression := callee.document.ExpressionAt(reference.Start)
			if reference.Binding == nil && reference.Open && reference.Name == binding.Name && expression != nil && expression.Component == component.Name {
				return fmt.Sprintf("%s at %s:%d, in component %s rendered inside this scope, may use %s; rename it there too", reference.Name, filepath.Base(component.File), callee.line(reference.Start), component.Name, binding.Name)
			}
		}
	}
	if outlet {
		owner, ok := a.snapshot.Resolve(a.path, binding.Component)
		if !ok {
			return ""
		}
		for _, call := range a.snapshot.References(owner.ID) {
			if call.Kind != compiler.CallReference {
				continue
			}
			caller, ok := analyzeIn(a.snapshot, call.Range.File)
			if !ok {
				continue
			}
			for _, reference := range caller.document.References {
				if reference.Binding == nil && reference.Open && reference.Name == binding.Name && reference.Start > call.Range.Start {
					if element := caller.callAt(call.Range.Start); element != nil && reference.End <= element.E {
						return fmt.Sprintf("%s at %s:%d is content passed to %s, which renders it inside this scope; rename it there too", reference.Name, filepath.Base(call.Range.File), caller.line(reference.Start), binding.Component)
					}
				}
			}
		}
	}
	return ""
}

func (a *analysis) line(offset int) int {
	return strings.Count(string(a.source[:offset]), "\n") + 1
}

// callAt returns the component call element whose name starts at offset.
func (a *analysis) callAt(offset int) *ast.ElementNode {
	var found *ast.ElementNode
	document := a.snapshot.Document(a.path)
	for _, node := range document.Nodes {
		var body []ast.Node
		switch declaration := node.(type) {
		case *ast.ElementDecl:
			body = declaration.Body
		case *ast.RenderClause:
			body = declaration.Body
		}
		walkElements(body, func(element *ast.ElementNode) bool {
			if element.P == offset {
				found = element
			}
			return found == nil
		})
	}
	return found
}

func isOutlet(snapshot *compiler.Snapshot, path, component, name string) bool {
	if name == compiler.ChildrenInput {
		return true
	}
	resolved, ok := snapshot.Resolve(path, component)
	if !ok {
		return false
	}
	for _, input := range resolved.Inputs {
		if input.Name == name && input.IsContent() {
			return true
		}
	}
	return false
}

func encloses(outer, inner *alpine.Scope) bool {
	for scope := inner; scope != nil; scope = scope.Parent {
		if scope == outer {
			return true
		}
	}
	return false
}

// walkElements visits elements in pre-order; visit returns false to skip
// an element's children.
func walkElements(nodes []ast.Node, visit func(*ast.ElementNode) bool) {
	for _, node := range nodes {
		switch value := node.(type) {
		case *ast.ElementNode:
			if visit(value) {
				walkElements(value.Children, visit)
			}
		case *ast.ConditionNode:
			walkElements(value.Then, visit)
			walkElements(value.Else, visit)
		case *ast.RepetitionNode:
			walkElements(value.Body, visit)
		case *ast.SlotNode:
			walkElements(value.Body, visit)
		}
	}
}

// workspaceSymbols lists the Alpine data, methods, and refs of every KTML
// document in the workspaces the editor has opened, or under the working
// directory.
func workspaceSymbols(payload []byte) ([]byte, error) {
	var params struct {
		ID    json.RawMessage `json:"id"`
		Query string          `json:"query"`
	}
	if err := json.Unmarshal(payload, &params); err != nil {
		return nil, err
	}
	state.Lock()
	workspaces := make([]*compiler.Workspace, 0, len(state.workspaces))
	roots := make([]string, 0, len(state.workspaces))
	for root := range state.workspaces {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	for _, root := range roots {
		workspaces = append(workspaces, state.workspaces[root])
	}
	state.Unlock()
	if len(workspaces) == 0 {
		if dir, err := os.Getwd(); err == nil {
			workspaces = append(workspaces, workspaceFor(filepath.Join(dir, "x.ktml")))
		}
	}
	query := strings.ToLower(params.Query)
	symbols := make([]map[string]any, 0)
	for _, workspace := range workspaces {
		snapshot, err := workspace.Index()
		if err != nil || snapshot == nil {
			continue
		}
		for _, document := range indexedFiles(snapshot) {
			current, ok := analyzeIn(snapshot, document)
			if !ok {
				continue
			}
			for _, binding := range current.document.Bindings {
				kind := 0
				switch binding.Kind {
				case alpine.DataProperty, alpine.DataGetter:
					kind = 7
				case alpine.DataMethod:
					kind = 6
				case alpine.Ref:
					kind = 8
				}
				if kind == 0 || query != "" && !strings.Contains(strings.ToLower(binding.Name), query) {
					continue
				}
				container := "x-data in " + binding.Component
				if binding.Kind == alpine.Ref {
					container = "x-ref in " + binding.Component
				}
				symbols = append(symbols, map[string]any{
					"name": binding.Name, "kind": kind, "containerName": container,
					"location": map[string]any{"uri": pathURI(document), "range": current.rangeAt(binding.Start, binding.End)},
				})
			}
		}
	}
	return result(params.ID, symbols)
}

// indexedFiles returns the KTML documents of a snapshot in order.
func indexedFiles(snapshot *compiler.Snapshot) []string {
	seen := make(map[string]bool)
	var files []string
	for _, component := range snapshot.Components() {
		if !seen[component.File] && strings.HasSuffix(component.File, ".ktml") {
			seen[component.File] = true
			files = append(files, component.File)
		}
	}
	sort.Strings(files)
	return files
}
