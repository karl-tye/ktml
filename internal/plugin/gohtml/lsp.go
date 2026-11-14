package gohtml

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/htmlcheck"
	"github.com/karl-tye/ktml/internal/parser"
	"github.com/karl-tye/ktml/internal/symbolindex"
	"github.com/karl-tye/ktml/internal/textposition"
)

var (
	openDocuments    = make(map[string]string)
	openDocumentsMu  sync.Mutex
	htmxRoutePattern = regexp.MustCompile(`(?:^|[\s(])(?:data-)?hx-(get|post|put|patch|delete)\s*=\s*"[^"]*"`)

	semanticWorkspaces   = make(map[string]*compiler.Workspace)
	semanticWorkspacesMu sync.Mutex
)

// semanticSnapshot is the analysis of path: the indexed project's when it
// holds path, since every edit updates it, and otherwise its directory
// workspace's.
func semanticSnapshot(path string) (*compiler.Snapshot, error) {
	if project := projectWorkspace(path); project.Indexed() {
		if snapshot, err := project.Current(); err == nil && snapshot.HasFile(path) {
			return snapshot, nil
		}
	}
	return semanticWorkspace(path).Load(path)
}

// editingWorkspace is the workspace an edit of path is analyzed in: the
// indexed project, which holds every document, or the directory's.
func editingWorkspace(path string) *compiler.Workspace {
	if project := projectWorkspace(path); project.Indexed() {
		return project
	}
	return semanticWorkspace(path)
}

func semanticWorkspace(path string) *compiler.Workspace {
	return workspaceAt(filepath.Dir(path))
}

// workspaceAt returns the workspace rooted at root. A new one starts with
// the open documents' text, since edits reach existing workspaces only. Its
// story Documents join when opened, or for a rename (loadStories): in a
// large application, generated stories doubled the initial index and slowed
// every edit.
func workspaceAt(root string) *compiler.Workspace {
	semanticWorkspacesMu.Lock()
	workspace := semanticWorkspaces[root]
	created := workspace == nil
	if created {
		workspace = compiler.NewWorkspace(root)
		workspace.DeferStories()
		semanticWorkspaces[root] = workspace
	}
	semanticWorkspacesMu.Unlock()
	if created {
		for uri, text := range getOpenDocuments() {
			if open := strings.TrimPrefix(uri, "file://"); strings.HasSuffix(open, ".ktml") || compiler.IsSchemaPath(open) {
				_ = workspace.Overlay(open, 0, []byte(text))
			}
		}
	}
	return workspace
}

// loadStories brings the deferred story Documents into the workspaces a
// rename of the request's document reads, since a rename changes story
// headers, inputs, and fields too.
func loadStories(payload []byte) {
	var request struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	if json.Unmarshal(payload, &request) != nil || request.TextDocument.URI == "" {
		return
	}
	path := strings.TrimPrefix(request.TextDocument.URI, "file://")
	_, _ = projectWorkspace(path).LoadStories()
	_, _ = semanticWorkspace(path).LoadStories()
}

// projectWorkspace is the workspace rooted at the project of path: the
// nearest directory up from it with ktml.config.json or go.mod, or its own
// directory.
func projectWorkspace(path string) *compiler.Workspace {
	dir := filepath.Dir(path)
	for candidate := dir; ; {
		for _, marker := range []string{"ktml.config.json", "go.mod"} {
			if _, err := os.Stat(filepath.Join(candidate, marker)); err == nil {
				return workspaceAt(candidate)
			}
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return workspaceAt(dir)
		}
		candidate = parent
	}
}

var (
	seededProjects   = make(map[*compiler.Workspace]bool)
	seededProjectsMu sync.Mutex
)

// projectSnapshot is the project-wide snapshot that component references
// and rename use, so they reach callers in every directory, not only the
// files path imports. Open documents seed it once with their unsaved text;
// later edits reach it through propagateOverlay, and changes on disk through
// the host's symbol index requests, so a request reuses the latest analysis
// rather than reading the project again.
func projectSnapshot(path string) (*compiler.Snapshot, error) {
	return seededProject(projectWorkspace(path), false)
}

// componentSnapshot returns the project snapshot when it resolves a
// component at offset in path, and the file's own snapshot otherwise.
func componentSnapshot(path string, line, character int) (*compiler.Snapshot, error) {
	if snapshot, err := projectSnapshot(path); err == nil {
		if source := snapshot.Source(path); source != nil {
			if _, _, ok := snapshot.SymbolAt(path, positionToOffset(source, line, character)); ok {
				return snapshot, nil
			}
		}
	}
	return semanticSnapshot(path)
}

func getOpenDocuments() map[string]string {
	openDocumentsMu.Lock()
	defer openDocumentsMu.Unlock()
	m := make(map[string]string)
	for k, v := range openDocuments {
		m[k] = v
	}
	return m
}

func offsetToPosition(src []byte, offset int) (line int, col int) {
	return textposition.Position(src, offset)
}

func positionToOffset(src []byte, line, character int) int {
	return textposition.Offset(src, line, character)
}

func isWordChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-'
}

func HandleLSP(method string, payload []byte) ([]byte, error) {
	type Position struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	}
	type DiagnosticRange struct {
		Start Position `json:"start"`
		End   Position `json:"end"`
	}
	type Diagnostic struct {
		Range    DiagnosticRange `json:"range"`
		Severity int             `json:"severity"`
		Code     string          `json:"code,omitempty"`
		Source   string          `json:"source,omitempty"`
		Message  string          `json:"message"`
	}
	type PublishDiagnosticsParams struct {
		URI         string       `json:"uri"`
		Diagnostics []Diagnostic `json:"diagnostics"`
	}
	type PublishDiagnosticsNotification struct {
		JsonRPC string                   `json:"jsonrpc"`
		Method  string                   `json:"method"`
		Params  PublishDiagnosticsParams `json:"params"`
	}

	if method == symbolindex.Method {
		return handleIndex(payload)
	}
	if method == "textDocument/rename" {
		loadStories(payload)
	}
	if response, handled, err := handleSchemaLSP(method, payload); handled || err != nil {
		return response, err
	}

	if method == "textDocument/didOpen" || method == "textDocument/didChange" || method == "textDocument/didClose" {
		var req struct {
			TextDocument struct {
				URI     string `json:"uri"`
				Text    string `json:"text"`
				Version int    `json:"version"`
			} `json:"textDocument"`
			ContentChanges []struct {
				Text string `json:"text"`
			} `json:"contentChanges"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		text := req.TextDocument.Text
		if text == "" && len(req.ContentChanges) > 0 {
			text = req.ContentChanges[0].Text
		}

		openDocumentsMu.Lock()
		if method == "textDocument/didClose" {
			delete(openDocuments, req.TextDocument.URI)
		} else {
			openDocuments[req.TextDocument.URI] = text
		}
		openDocumentsMu.Unlock()

		path := strings.TrimPrefix(req.TextDocument.URI, "file://")
		semantic := editingWorkspace(path)

		if method == "textDocument/didClose" {
			_, _ = semantic.Close(path)
			propagateOverlay(semantic, path, 0, nil, true)
			notification := PublishDiagnosticsNotification{
				JsonRPC: "2.0",
				Method:  "textDocument/publishDiagnostics",
				Params: PublishDiagnosticsParams{
					URI:         req.TextDocument.URI,
					Diagnostics: []Diagnostic{},
				},
			}
			primary, err := json.Marshal(notification)
			if err != nil {
				return nil, err
			}
			return withDependentDiagnostics(primary, path)
		}

		snapshot, semanticErr := semantic.Update(path, req.TextDocument.Version, []byte(text))
		propagateOverlay(semantic, path, req.TextDocument.Version, []byte(text), false)
		diagnostics := make([]Diagnostic, 0)
		if semanticErr != nil {
			diagnostics = append(diagnostics, Diagnostic{
				Range: DiagnosticRange{Start: Position{}, End: Position{Character: 1}}, Severity: 1,
				Code: "workspace", Source: "ktml", Message: semanticErr.Error(),
			})
		} else {
			for _, semanticDiagnostic := range documentDiagnostics(snapshot, path) {
				startLine, startCharacter := offsetToPosition([]byte(text), semanticDiagnostic.Start)
				endLine, endCharacter := offsetToPosition([]byte(text), semanticDiagnostic.End)
				diagnostics = append(diagnostics, Diagnostic{
					Range: DiagnosticRange{
						Start: Position{Line: startLine, Character: startCharacter},
						End:   Position{Line: endLine, Character: endCharacter},
					},
					Severity: lspSeverity(semanticDiagnostic),
					Code:     semanticDiagnostic.Code,
					Source:   "ktml",
					Message:  semanticDiagnostic.Message,
				})
			}
		}

		notification := PublishDiagnosticsNotification{
			JsonRPC: "2.0",
			Method:  "textDocument/publishDiagnostics",
			Params: PublishDiagnosticsParams{
				URI:         req.TextDocument.URI,
				Diagnostics: diagnostics,
			},
		}

		primary, err := json.Marshal(notification)
		if err != nil {
			return nil, err
		}
		return withDependentDiagnostics(primary, path)
	}

	if method == "textDocument/definition" {
		var req struct {
			ID           json.RawMessage `json:"id"`
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			Position Position `json:"position"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		uriPath := strings.TrimPrefix(req.TextDocument.URI, "file://")
		if snapshot, semanticErr := semanticSnapshot(uriPath); semanticErr == nil {
			if source := snapshot.Source(uriPath); source != nil {
				targetOffset := positionToOffset(source, req.Position.Line, req.Position.Character)
				if component, _, ok := snapshot.SymbolAt(uriPath, targetOffset); ok {
					declarationSource := snapshot.Source(component.Range.File)
					return json.Marshal(map[string]any{
						"jsonrpc": "2.0",
						"id":      req.ID,
						"result": map[string]any{
							"uri":   "file://" + component.Range.File,
							"range": rangeAt(declarationSource, component.Range.Start, component.Range.End),
						},
					})
				}
			}
		}
		// Anything else is another analyzer's, or nothing: a word that
		// merely spells a declaration's name is not a reference to it.
		return json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": nil})
	}

	if method == "textDocument/references" {
		var req struct {
			ID           json.RawMessage `json:"id"`
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			Position Position `json:"position"`
			Context  struct {
				IncludeDeclaration bool `json:"includeDeclaration"`
			} `json:"context"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}

		uriPath := strings.TrimPrefix(req.TextDocument.URI, "file://")
		if snapshot, semanticErr := componentSnapshot(uriPath, req.Position.Line, req.Position.Character); semanticErr == nil {
			if source := snapshot.Source(uriPath); source != nil {
				targetOffset := positionToOffset(source, req.Position.Line, req.Position.Character)
				if component, _, ok := snapshot.SymbolAt(uriPath, targetOffset); ok {
					var locations []any
					if req.Context.IncludeDeclaration {
						declarationSource := snapshot.Source(component.Range.File)
						locations = append(locations, map[string]any{
							"uri":   "file://" + component.Range.File,
							"range": rangeAt(declarationSource, component.Range.Start, component.Range.End),
						})
					}
					for _, reference := range snapshot.References(component.ID) {
						referenceSource := snapshot.Source(reference.Range.File)
						locations = append(locations, map[string]any{
							"uri":   "file://" + reference.Range.File,
							"range": rangeAt(referenceSource, reference.Range.Start, reference.Range.End),
						})
					}
					return json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": locations})
				}
			}
		}
		return json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": []any{}})
	}

	if method == "textDocument/documentSymbol" {
		var req struct {
			ID           json.RawMessage `json:"id"`
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}

		content, ok := documentSource(req.TextDocument.URI)
		if !ok {
			return json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": []any{}})
		}

		var doc *ast.Document
		func() {
			defer func() { recover() }()
			doc = parser.New(content).ParseDocument()
		}()

		var symbols []map[string]any
		if doc != nil {
			for _, node := range doc.Nodes {
				if decl, ok := node.(*ast.ElementDecl); ok {
					line, col := offsetToPosition(content, decl.Pos())
					endLine, endCol := offsetToPosition(content, decl.End())
					symbols = append(symbols, map[string]any{
						"name": decl.Name,
						"kind": 25,
						"location": map[string]any{
							"uri": req.TextDocument.URI,
							"range": map[string]any{
								"start": map[string]any{"line": line, "character": col},
								"end":   map[string]any{"line": endLine, "character": endCol},
							},
						},
					})
				}
				if symbol, ok := storySymbol(req.TextDocument.URI, content, node); ok {
					symbols = append(symbols, symbol)
				}
				if imp, ok := node.(*ast.ImportStmt); ok {
					line, col := offsetToPosition(content, imp.Pos())
					endLine, endCol := offsetToPosition(content, imp.End())
					symbols = append(symbols, map[string]any{
						"name": imp.Path,
						"kind": 18,
						"location": map[string]any{
							"uri": req.TextDocument.URI,
							"range": map[string]any{
								"start": map[string]any{"line": line, "character": col},
								"end":   map[string]any{"line": endLine, "character": endCol},
							},
						},
					})
				}
			}
		}

		return json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": symbols})
	}

	if method == "textDocument/codeLens" {
		var req struct {
			ID           json.RawMessage `json:"id"`
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": referenceLenses(strings.TrimPrefix(req.TextDocument.URI, "file://"))})
	}

	if method == "textDocument/foldingRange" {
		var req struct {
			ID           json.RawMessage `json:"id"`
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		content, ok := documentSource(req.TextDocument.URI)
		if !ok {
			return json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": []any{}})
		}
		return json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": foldingRanges(content)})
	}

	if method == "textDocument/hover" {
		var req struct {
			ID           json.RawMessage `json:"id"`
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			Position Position `json:"position"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}

		uriPath := strings.TrimPrefix(req.TextDocument.URI, "file://")
		if snapshot, semanticErr := semanticSnapshot(uriPath); semanticErr == nil {
			if source := snapshot.Source(uriPath); source != nil {
				targetOffset := positionToOffset(source, req.Position.Line, req.Position.Character)
				if component, _, ok := snapshot.SymbolAt(uriPath, targetOffset); ok {
					parts := make([]string, len(component.Inputs))
					for i, input := range component.Inputs {
						parts[i] = input.Name + ": " + input.Type.String()
					}
					value := "component " + component.Name + "(" + strings.Join(parts, ", ") + ")"
					return json.Marshal(map[string]any{
						"jsonrpc": "2.0", "id": req.ID,
						"result": map[string]any{"contents": map[string]any{"kind": "markdown", "value": value}},
					})
				}
				if value, start, end, ok := htmlcheck.Hover(snapshot, uriPath, targetOffset); ok {
					return json.Marshal(map[string]any{
						"jsonrpc": "2.0", "id": req.ID,
						"result": map[string]any{"contents": map[string]any{"kind": "markdown", "value": value}, "range": rangeAt(source, start, end)},
					})
				}
			}
		}
		return json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": nil})
	}

	if method == "textDocument/formatting" {
		var req struct {
			ID           json.RawMessage `json:"id"`
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}

		content, ok := documentSource(req.TextDocument.URI)
		if !ok {
			return json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": []any{}})
		}

		formatted, ok := formatSource(content)
		if !ok || formatted == string(content) {
			return json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": []any{}})
		}
		return json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result":  []map[string]any{{"range": rangeAt(content, 0, len(content)), "newText": formatted}},
		})
	}

	if method == "textDocument/rangeFormatting" || method == "textDocument/onTypeFormatting" {
		return formatPart(method, payload)
	}

	if method == "textDocument/completion" {
		var req struct {
			ID           json.RawMessage `json:"id"`
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			Position Position `json:"position"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}

		uriPath := strings.TrimPrefix(req.TextDocument.URI, "file://")

		openDocumentsMu.Lock()
		text, ok := openDocuments[req.TextDocument.URI]
		openDocumentsMu.Unlock()

		if !ok {
			content, err := os.ReadFile(uriPath)
			if err != nil {
				return json.Marshal(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]any{"isIncomplete": true, "items": []any{}},
				})
			}
			text = string(content)
		}

		content := []byte(text)
		dir := filepath.Dir(uriPath)

		cursor := positionToOffset(content, req.Position.Line, req.Position.Character)
		htmlSnapshot, _ := semanticSnapshot(uriPath)
		htmlItems, htmlKind := htmlCompletionItems(htmlSnapshot, uriPath, content, cursor)
		// Inside a quoted value only the value's own completions apply:
		// keyword values here, paths above, and class names or framework
		// expressions from the Tailwind adapter and the plugins.
		if htmlKind == htmlcheck.AttributeValue || len(htmlItems) > 0 && htmlKind == htmlcheck.AttributeName {
			return json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{"isIncomplete": false, "items": htmlItems},
			})
		}
		if attrItems := completeAttributes(content, cursor, uriPath); attrItems != nil {
			return json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"isIncomplete": false,
					"items":        attrItems,
				},
			})
		}

		// Components are offered from the whole project, not only the
		// file's package, with the import that reaches them.
		typedSnapshot, _ := projectWorkspace(uriPath).Index()

		var doc *ast.Document
		func() {
			defer func() { recover() }()
			doc = parser.New(content).ParseDocument()
		}()

		items := []map[string]any{
			{
				"label":  "import",
				"kind":   14,
				"detail": "import { ... } from \"...\"",
			},
		}

		seen := make(map[string]bool)
		var existingImports []*ast.ImportStmt

		if doc != nil {
			for _, node := range doc.Nodes {
				if decl, ok := node.(*ast.ElementDecl); ok {
					if !seen[decl.Name] {
						seen[decl.Name] = true
						detail := ""
						if len(decl.Attributes) > 0 {
							parts := make([]string, len(decl.Attributes))
							for i, a := range decl.Attributes {
								parts[i] = a.Name + ": " + a.TypeDecl
							}
							detail = "[" + strings.Join(parts, ", ") + "]"
						}
						items = append(items, map[string]any{
							"label":  decl.Name,
							"kind":   1,
							"detail": detail,
						})
					}
				}
				if imp, ok := node.(*ast.ImportStmt); ok {
					existingImports = append(existingImports, imp)
					for _, item := range imp.Items {
						if !seen[item] {
							seen[item] = true
							items = append(items, map[string]any{
								"label":  item,
								"kind":   1,
								"detail": "imported from " + imp.Path,
							})
						}
					}
				}
			}
		}

		insertLine := 0
		if len(existingImports) > 0 {
			lastImport := existingImports[len(existingImports)-1]
			insertLine, _ = offsetToPosition(content, lastImport.End())
			insertLine++
		}

		componentPaths := make(map[string][]string)
		if typedSnapshot != nil {
			for _, component := range typedSnapshot.Components() {
				componentPaths[component.Name] = append(componentPaths[component.Name], component.File)
			}
		}
		componentNames := make([]string, 0, len(componentPaths))
		for name := range componentPaths {
			componentNames = append(componentNames, name)
		}
		sort.Strings(componentNames)
		for _, name := range componentNames {
			compPaths := componentPaths[name]
			if seen[name] {
				continue
			}
			for _, compPath := range compPaths {
				relPath, _ := filepath.Rel(dir, compPath)
				importPath := strings.TrimSuffix(relPath, ".ktml")
				if importPath == "" {
					importPath = "./" + filepath.Base(strings.TrimSuffix(uriPath, ".ktml"))
				}

				importEdit := fmt.Sprintf("import { %s } from %s\n", name, strconv.Quote(importPath))

				label := name
				if len(compPaths) > 1 {
					label = name + " (" + importPath + ")"
				}

				items = append(items, map[string]any{
					"label":  label,
					"kind":   1,
					"detail": compPath,
					"additionalTextEdits": []map[string]any{
						{
							"range": map[string]any{
								"start": map[string]any{"line": insertLine, "character": 0},
								"end":   map[string]any{"line": insertLine, "character": 0},
							},
							"newText": importEdit,
						},
					},
					"sortText": fmt.Sprintf("z%04d%s", len(items), name),
				})
			}
		}

		if htmlKind == htmlcheck.ElementName {
			items = append(items, htmlItems...)
		}

		return json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result": map[string]any{
				"isIncomplete": true,
				"items":        items,
			},
		})
	}

	if method == "textDocument/signatureHelp" {
		var req struct {
			ID           json.RawMessage `json:"id"`
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			Position Position `json:"position"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		content, ok := documentSource(req.TextDocument.URI)
		if !ok {
			return json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": nil})
		}
		uriPath := strings.TrimPrefix(req.TextDocument.URI, "file://")
		if help, ok := callSignature(uriPath, content, positionToOffset(content, req.Position.Line, req.Position.Character)); ok {
			return json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": help})
		}
		return json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": nil})
	}

	if method == "workspace/symbol" {
		var req struct {
			ID    json.RawMessage `json:"id"`
			Query string          `json:"query"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}

		// The project is the guest's working directory, which the host sets
		// to its own, so every location is an absolute file URI.
		query := strings.ToLower(req.Query)
		symbols := []map[string]any{}
		root, err := os.Getwd()
		if err != nil {
			return json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": symbols})
		}
		snapshot, err := seededProject(projectWorkspace(filepath.Join(root, "ktml.config.json")), false)
		if err != nil {
			return json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": symbols})
		}
		matches := func(name string) bool { return query == "" || strings.Contains(strings.ToLower(name), query) }
		for _, file := range snapshot.Files() {
			document := snapshot.Document(file)
			if document == nil {
				continue
			}
			content := snapshot.Source(file)
			for _, node := range document.Nodes {
				if symbol, ok := storySymbol("file://"+file, content, node); ok && matches(symbol["name"].(string)) {
					symbols = append(symbols, symbol)
				}
			}
		}
		for _, component := range snapshot.Components() {
			if !matches(component.Name) {
				continue
			}
			symbols = append(symbols, map[string]any{
				"name": component.Name,
				"kind": 25,
				"location": map[string]any{
					"uri":   "file://" + component.File,
					"range": rangeAt(snapshot.Source(component.File), component.Range.Start, component.Range.End),
				},
			})
		}

		return json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result":  symbols,
		})
	}

	if method == "textDocument/codeAction" {
		var req struct {
			ID           json.RawMessage `json:"id"`
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			Range struct {
				Start Position `json:"start"`
				End   Position `json:"end"`
			} `json:"range"`
			Context struct {
				Diagnostics []struct {
					Range struct {
						Start Position `json:"start"`
						End   Position `json:"end"`
					} `json:"range"`
					Message string `json:"message"`
				} `json:"diagnostics"`
			} `json:"context"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}

		uriPath := strings.TrimPrefix(req.TextDocument.URI, "file://")

		openDocumentsMu.Lock()
		text, ok := openDocuments[req.TextDocument.URI]
		openDocumentsMu.Unlock()

		if !ok {
			content, err := os.ReadFile(uriPath)
			if err != nil {
				return json.Marshal(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  []any{},
				})
			}
			text = string(content)
		}

		content := []byte(text)

		var doc *ast.Document
		func() {
			defer func() { recover() }()
			doc = parser.New(content).ParseDocument()
		}()

		var actions []map[string]any

		for _, diag := range req.Context.Diagnostics {
			msg := diag.Message
			word := extractIdentifierFromError(msg)
			if word == "" {
				continue
			}

			known := false
			if doc != nil {
				for _, node := range doc.Nodes {
					if decl, ok := node.(*ast.ElementDecl); ok {
						if decl.Name == word {
							known = true
							break
						}
					}
					if imp, ok := node.(*ast.ImportStmt); ok {
						for _, item := range imp.Items {
							if item == word {
								known = true
								break
							}
						}
					}
				}
			}

			if !known {
				dir := filepath.Dir(uriPath)
				var foundPaths []string
				if project, err := projectWorkspace(uriPath).Current(); err == nil {
					for _, component := range project.Components() {
						if component.Name == word && component.File != uriPath {
							foundPaths = append(foundPaths, component.File)
						}
					}
				}
				if len(foundPaths) == 0 {
					continue
				}

				for _, foundPath := range foundPaths {
					relPath, _ := filepath.Rel(dir, foundPath)
					importText := "import { " + word + " } from \"" + strings.TrimSuffix(relPath, ".ktml") + "\"\n"

					title := "Import " + word + " from \"" + relPath + "\""
					if len(foundPaths) > 1 {
						title += " (" + filepath.Base(foundPath) + ")"
					}

					actions = append(actions, map[string]any{
						"title": title,
						"kind":  "quickfix",
						"edit": map[string]any{
							"changes": map[string]any{
								req.TextDocument.URI: []map[string]any{
									{
										"range": map[string]any{
											"start": map[string]any{"line": 0, "character": 0},
											"end":   map[string]any{"line": 0, "character": 0},
										},
										"newText": importText,
									},
								},
							},
						},
						"diagnostics": []map[string]any{
							{
								"range": map[string]any{
									"start": map[string]any{
										"line":      diag.Range.Start.Line,
										"character": diag.Range.Start.Character,
									},
									"end": map[string]any{
										"line":      diag.Range.End.Line,
										"character": diag.Range.End.Character,
									},
								},
								"message": diag.Message,
							},
						},
					})
				}
			}
		}

		if snapshot, err := semanticSnapshot(uriPath); err == nil {
			start := positionToOffset(content, req.Range.Start.Line, req.Range.Start.Character)
			end := positionToOffset(content, req.Range.End.Line, req.Range.End.Character)
			actions = append(actions, htmlCodeActions(snapshot, uriPath, req.TextDocument.URI, start, end)...)
		}

		if doc != nil {
			hasImports := false
			for _, node := range doc.Nodes {
				if _, ok := node.(*ast.ImportStmt); ok {
					hasImports = true
					break
				}
			}
			if hasImports {
				actions = append(actions, map[string]any{
					"title": "Organize imports",
					"kind":  "source.organizeImports",
				})
			}
		}

		return json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result":  actions,
		})
	}

	if method == "textDocument/prepareRename" {
		var req struct {
			ID           json.RawMessage `json:"id"`
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			Position Position `json:"position"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		path := strings.TrimPrefix(req.TextDocument.URI, "file://")
		snapshot, err := semanticSnapshot(path)
		if err != nil {
			return json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": nil})
		}
		content := snapshot.Source(path)
		if content == nil {
			return json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": nil})
		}
		offset := positionToOffset(content, req.Position.Line, req.Position.Character)
		if isHTMXRouteAttributeAt(string(content), offset) {
			return json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": nil})
		}
		component, symbolRange, ok := snapshot.SymbolAt(path, offset)
		if !ok {
			return json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": nil})
		}
		return json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result": map[string]any{
				"range":       rangeAt(content, symbolRange.Start, symbolRange.End),
				"placeholder": component.Name,
			},
		})
	}

	if method == "textDocument/rename" {
		var req struct {
			ID           json.RawMessage `json:"id"`
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			Position Position `json:"position"`
			NewName  string   `json:"newName"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		if !validIdentifier(req.NewName) {
			return json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": nil})
		}

		uriPath := strings.TrimPrefix(req.TextDocument.URI, "file://")
		snapshot, err := componentSnapshot(uriPath, req.Position.Line, req.Position.Character)
		if err != nil {
			return json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": nil})
		}
		content := snapshot.Source(uriPath)
		if content == nil {
			return json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": nil})
		}
		targetOffset := positionToOffset(content, req.Position.Line, req.Position.Character)
		if isHTMXRouteAttributeAt(string(content), targetOffset) {
			return json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": nil})
		}
		component, _, ok := snapshot.SymbolAt(uriPath, targetOffset)
		if !ok {
			return json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": nil})
		}
		if existing, collision := snapshot.Resolve(uriPath, req.NewName); collision && existing.ID != component.ID {
			return json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": nil})
		}
		changes := make(map[string][]map[string]any)
		ranges := []compiler.SourceRange{component.Range}
		for _, reference := range snapshot.References(component.ID) {
			ranges = append(ranges, reference.Range)
		}
		for _, symbolRange := range ranges {
			source := snapshot.Source(symbolRange.File)
			if source == nil {
				continue
			}
			uri := "file://" + symbolRange.File
			changes[uri] = append(changes[uri], map[string]any{
				"range":   rangeAt(source, symbolRange.Start, symbolRange.End),
				"newText": req.NewName,
			})
		}

		return json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result": map[string]any{
				"changes": changes,
			},
		})
	}

	return nil, nil
}

func validIdentifier(name string) bool {
	if name == "" || !((name[0] >= 'a' && name[0] <= 'z') || (name[0] >= 'A' && name[0] <= 'Z') || name[0] == '_') {
		return false
	}
	for i := 1; i < len(name); i++ {
		character := name[i]
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func isHTMXRouteAttributeAt(source string, offset int) bool {
	for _, match := range htmxRoutePattern.FindAllStringIndex(source, -1) {
		if offset >= match[0] && offset <= match[1] {
			return true
		}
	}
	return false
}

func extractIdentifierFromError(msg string) string {
	for _, prefix := range []string{
		"expected identifier, got ",
		"undefined: ",
		"could not find ",
	} {
		if after, found := strings.CutPrefix(msg, prefix); found {
			return strings.TrimSpace(after)
		}
	}
	return ""
}

func rangeAt(content []byte, start, end int) map[string]any {
	sLine, sCol := offsetToPosition(content, start)
	eLine, eCol := offsetToPosition(content, end)
	return map[string]any{
		"start": map[string]any{"line": sLine, "character": sCol},
		"end":   map[string]any{"line": eLine, "character": eCol},
	}
}

func splitAttrSegments(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	})
}

func completeAttributes(content []byte, cursor int, uriPath string) []map[string]any {
	text := string(content)
	lines := strings.Split(text, "\n")

	currLine, _ := offsetToPosition(content, cursor)

	var compName string
	var colonOffset int

	for l := currLine; l >= 0; l-- {
		if l >= len(lines) {
			continue
		}
		trimmed := strings.TrimSpace(lines[l])
		colonIdx := strings.LastIndex(trimmed, ":")
		if colonIdx < 0 {
			continue
		}
		if l == currLine {
			lineCol := cursor
			for p := cursor - 1; p >= 0 && content[p] != '\n'; p-- {
				lineCol--
			}
			colonInLine := strings.LastIndex(lines[l], ":")
			if colonInLine >= 0 && colonInLine > lineCol {
				colonIdx = strings.LastIndex(lines[l][:colonInLine], ":")
				if colonIdx < 0 {
					continue
				}
				trimmed = strings.TrimSpace(lines[l][:colonInLine])
			}
		}
		afterColon := strings.TrimSpace(trimmed[colonIdx+1:])
		if afterColon != "" && !strings.HasPrefix(afterColon, "\"") {
			continue
		}
		compName = strings.TrimSpace(trimmed[:colonIdx])
		if compName == "" {
			continue
		}
		if strings.Contains(compName, " ") {
			continue
		}
		pos := 0
		for i := 0; i < l; i++ {
			pos += len(lines[i]) + 1
		}
		colonOffset = pos + strings.Index(lines[l], ":")
		break
	}

	if compName == "" {
		return nil
	}

	var attrs []*ast.AttributeDecl

	doc := parseContent(content)
	if doc != nil {
		for _, node := range doc.Nodes {
			if imp, ok := node.(*ast.ImportStmt); ok {
				for _, item := range imp.Items {
					if item == compName {
						importPath := imp.Path
						if u, err := strconv.Unquote(importPath); err == nil {
							importPath = u
						}
						if !strings.HasSuffix(importPath, ".ktml") {
							importPath += ".ktml"
						}
						fullPath := filepath.Join(filepath.Dir(uriPath), importPath)
						if fc, err := os.ReadFile(fullPath); err == nil {
							if idoc := parseContent(fc); idoc != nil {
								for _, inode := range idoc.Nodes {
									if decl, ok := inode.(*ast.ElementDecl); ok && decl.Name == compName {
										attrs = decl.Attributes
										break
									}
								}
							} else {
								attrs = findTextAttributes(fc, compName)
							}
						}
						break
					}
				}
			}
			if len(attrs) > 0 {
				break
			}
			if decl, ok := node.(*ast.ElementDecl); ok && decl.Name == compName {
				attrs = decl.Attributes
				break
			}
		}
	}

	if len(attrs) == 0 {
		attrs = findTextAttributes(content, compName)
		if len(attrs) == 0 {
			return nil
		}
	}

	usedNames := make(map[string]bool)
	if cursor > colonOffset {
		restOfLine := strings.TrimSpace(text[colonOffset+1 : cursor])
		for _, part := range splitAttrSegments(restOfLine) {
			part = strings.TrimSpace(part)
			if i := strings.IndexByte(part, ':'); i > 0 {
				usedNames[strings.TrimSpace(part[:i])] = true
			}
		}
	}

	var items []map[string]any
	for _, a := range attrs {
		if usedNames[a.Name] {
			continue
		}
		items = append(items, map[string]any{
			"label":      a.Name,
			"kind":       6,
			"detail":     a.TypeDecl,
			"insertText": a.Name + ": ",
		})
	}
	if len(items) == 0 {
		return nil
	}
	return items
}

func parseContent(src []byte) *ast.Document {
	var doc *ast.Document
	func() {
		defer func() { recover() }()
		doc = parser.New(src).ParseDocument()
	}()
	return doc
}

func findTextAttributes(src []byte, name string) []*ast.AttributeDecl {
	text := string(src)
	for i := 0; i < len(text); {
		idx := strings.Index(text[i:], name)
		if idx == -1 {
			break
		}
		abs := i + idx
		before := abs > 0 && isWordChar(text[abs-1])
		after := abs+len(name) < len(text) && isWordChar(text[abs+len(name)])
		if before || after {
			i = abs + len(name)
			continue
		}
		rest := src[abs+len(name):]
		bStart := -1
		for j := 0; j < len(rest); j++ {
			if rest[j] == ':' || rest[j] == '#' || rest[j] == '{' {
				break
			}
			if rest[j] == '[' {
				bStart = abs + len(name) + j
				break
			}
			if rest[j] != ' ' && rest[j] != '\t' && rest[j] != '\n' && rest[j] != '\r' {
				break
			}
		}
		if bStart == -1 {
			i = abs + len(name)
			continue
		}
		bEnd := -1
		for j := bStart + 1; j < len(src); j++ {
			if src[j] == ']' {
				bEnd = j
				break
			}
		}
		if bEnd == -1 {
			i = abs + len(name)
			continue
		}
		content := strings.TrimSpace(string(src[bStart+1 : bEnd]))
		var attrs []*ast.AttributeDecl
		for _, seg := range splitAttrSegments(content) {
			seg = strings.TrimSpace(seg)
			if ci := strings.IndexByte(seg, ':'); ci > 0 {
				p := bStart + 1 + strings.Index(string(src[bStart+1:bEnd]), seg)
				attrs = append(attrs, &ast.AttributeDecl{
					Name:     strings.TrimSpace(seg[:ci]),
					TypeDecl: strings.TrimSpace(seg[ci+1:]),
					P:        p,
					E:        p + len(seg),
				})
			}
		}
		if len(attrs) > 0 {
			return attrs
		}
		i = abs + len(name)
	}
	return nil
}

// storySymbol is the symbol of a story (Component.Name, a function) or a
// fixture (a constant) declaration.
func storySymbol(uri string, content []byte, node ast.Node) (map[string]any, bool) {
	var name string
	var kind int
	switch declaration := node.(type) {
	case *ast.StoryDecl:
		name, kind = declaration.Component+"."+declaration.Name, 12
	case *ast.FixtureDecl:
		name, kind = declaration.Name, 14
	default:
		return nil, false
	}
	line, col := offsetToPosition(content, node.Pos())
	endLine, endCol := offsetToPosition(content, node.End())
	return map[string]any{
		"name": name,
		"kind": kind,
		"location": map[string]any{
			"uri": uri,
			"range": map[string]any{
				"start": map[string]any{"line": line, "character": col},
				"end":   map[string]any{"line": endLine, "character": endCol},
			},
		},
	}, true
}
