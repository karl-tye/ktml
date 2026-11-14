package gohtml

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/karl-tye/ktml/internal/compiler"
)

// Host Schema language features. Each handler answers only when the request
// position is a record, field, typed path, or schema completion context, and
// otherwise reports unhandled so the component and textual fallbacks run.

type schemaRequest struct {
	ID           json.RawMessage `json:"id"`
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	} `json:"position"`
	Context struct {
		IncludeDeclaration bool `json:"includeDeclaration"`
	} `json:"context"`
	NewName string `json:"newName"`
	Range   struct {
		Start struct {
			Line      int `json:"line"`
			Character int `json:"character"`
		} `json:"start"`
		End struct {
			Line      int `json:"line"`
			Character int `json:"character"`
		} `json:"end"`
	} `json:"range"`
}

func handleSchemaLSP(method string, payload []byte) ([]byte, bool, error) {
	switch method {
	case "textDocument/hover", "textDocument/definition", "textDocument/references",
		"textDocument/prepareRename", "textDocument/rename", "textDocument/completion",
		"textDocument/semanticTokens/full", "textDocument/semanticTokens/range":
	default:
		return nil, false, nil
	}
	var request schemaRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		return nil, false, err
	}
	path := strings.TrimPrefix(request.TextDocument.URI, "file://")
	respond := func(result any) ([]byte, bool, error) {
		response, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		return response, true, err
	}
	switch method {
	case "textDocument/semanticTokens/full":
		source, ok := documentSource(request.TextDocument.URI)
		if !ok {
			return respond(map[string]any{"data": []int{}})
		}
		return respond(map[string]any{"data": encodeSemanticTokens(source, compiler.SemanticTokens(source))})
	case "textDocument/semanticTokens/range":
		source, ok := documentSource(request.TextDocument.URI)
		if !ok {
			return respond(map[string]any{"data": []int{}})
		}
		// The tokens that overlap the range, encoded as in a full result:
		// the first relative to the start of the document.
		start := positionToOffset(source, request.Range.Start.Line, request.Range.Start.Character)
		end := positionToOffset(source, request.Range.End.Line, request.Range.End.Character)
		var tokens []compiler.SemanticToken
		for _, token := range compiler.SemanticTokens(source) {
			if token.End > start && token.Start < end {
				tokens = append(tokens, token)
			}
		}
		return respond(map[string]any{"data": encodeSemanticTokens(source, tokens)})
	case "textDocument/completion":
		source, ok := documentSource(request.TextDocument.URI)
		if !ok {
			return nil, false, nil
		}
		offset := positionToOffset(source, request.Position.Line, request.Position.Character)
		// A story's header and value list complete from its component's
		// typed inputs, from the project index: the story being typed
		// often does not parse. Its content body completes like a call body.
		if compiler.IsStoryPath(path) {
			project, _ := projectWorkspace(path).Index()
			if list, ok := project.StoryCompletions(path, source, offset); ok {
				return respond(map[string]any{"isIncomplete": false, "items": completionItems(source, offset, list)})
			}
		}
		workspace := semanticWorkspace(path)
		indexed, _ := workspace.Index()
		list, ok := compiler.SchemaCompletions(path, source, offset, indexed, workspace.LastValid())
		if !ok {
			return nil, false, nil
		}
		return respond(map[string]any{"isIncomplete": false, "items": completionItems(source, offset, list)})
	}

	snapshot, err := semanticSnapshot(path)
	if err != nil {
		return nil, false, nil
	}
	source := snapshot.Source(path)
	if source == nil {
		return nil, false, nil
	}
	offset := positionToOffset(source, request.Position.Line, request.Position.Character)
	if content, ok := snapshot.ContentOccurrenceAt(path, offset); ok {
		return handleContentLSP(method, request, path, snapshot, source, offset, content, respond)
	}
	occurrence, found := snapshot.SchemaOccurrenceAt(path, offset)
	// A Render Clause binding or repetition item is a local: hover and
	// definition describe and locate the binding, and an aliased binding or
	// an item has no schema symbol.
	binding, onBinding := snapshot.BindingOccurrenceAt(path, offset)

	switch method {
	case "textDocument/hover":
		value := ""
		var hoverRange compiler.SourceRange
		if onBinding {
			value, hoverRange = snapshot.BindingSignature(binding.Binding), binding.Range
		} else if found {
			value, hoverRange = occurrence.Symbol.Detail, occurrence.Range
		} else if part, ok := snapshot.PathPartAt(path, offset); ok {
			value, hoverRange = snapshot.PartSignature(part), part.Range
		} else {
			return nil, false, nil
		}
		return respond(map[string]any{
			"contents": map[string]any{"kind": "markdown", "value": "```ktml\n" + value + "\n```"},
			"range":    rangeAt(source, hoverRange.Start, hoverRange.End),
		})
	case "textDocument/definition":
		if onBinding && (binding.Root || !found) {
			return respond(locationOf(snapshot, binding.Binding.Range))
		}
		if !found {
			return nil, false, nil
		}
		return respond(locationOf(snapshot, occurrence.Symbol.Range))
	case "textDocument/prepareRename":
		if !found && onBinding && binding.Binding.Repetition != "" {
			return respond(map[string]any{
				"range":       rangeAt(source, binding.Range.Start, binding.Range.End),
				"placeholder": binding.Binding.Name,
			})
		}
		if !found && onBinding {
			// Renaming a binding alias is not supported yet.
			return respond(nil)
		}
		if !found {
			return nil, false, nil
		}
		if occurrence.Alias {
			return respond(nil)
		}
		return respond(map[string]any{
			"range":       rangeAt(source, occurrence.Range.Start, occurrence.Range.End),
			"placeholder": occurrence.Symbol.Name,
		})
	}

	if !found && onBinding {
		if method == "textDocument/references" {
			locations := []any{}
			if request.Context.IncludeDeclaration {
				locations = append(locations, locationOf(snapshot, binding.Binding.Range))
			}
			for _, reference := range snapshot.BindingReferences(binding.Binding) {
				locations = append(locations, locationOf(snapshot, reference))
			}
			return respond(locations)
		}
		if method == "textDocument/rename" && binding.Binding.Repetition != "" {
			ranges, err := snapshot.ItemRename(binding.Binding, request.NewName)
			if err != nil {
				return respond(nil)
			}
			return respond(map[string]any{"changes": renameChanges(snapshot, ranges, request.NewName)})
		}
		return respond(nil)
	}
	if !found {
		return nil, false, nil
	}
	// References and rename must see every importer under the workspace root,
	// not only the requesting document's dependency closure.
	if indexed, err := semanticWorkspace(path).Index(); err == nil {
		if resolved, ok := indexed.SchemaOccurrenceAt(path, offset); ok {
			snapshot, occurrence = indexed, resolved
		}
	}
	switch method {
	case "textDocument/references":
		locations := []any{}
		if request.Context.IncludeDeclaration {
			locations = append(locations, locationOf(snapshot, occurrence.Symbol.Range))
		}
		for _, reference := range snapshot.References(occurrence.Symbol.ID) {
			locations = append(locations, locationOf(snapshot, reference.Range))
		}
		return respond(locations)
	case "textDocument/rename":
		if occurrence.Alias {
			return respond(nil)
		}
		ranges, err := snapshot.SchemaRename(occurrence.Symbol.ID, request.NewName)
		if err != nil {
			return respond(nil)
		}
		return respond(map[string]any{"changes": renameChanges(snapshot, ranges, request.NewName)})
	}
	return nil, false, nil
}

// handleContentLSP answers hover, definition, references, and rename for a
// content input at an outlet, a slot fill name, or its declaration.
// References and rename use the indexed workspace so that fills in other
// documents are included.
func handleContentLSP(method string, request schemaRequest, path string, snapshot *compiler.Snapshot, source []byte, offset int, content compiler.ContentOccurrence, respond func(any) ([]byte, bool, error)) ([]byte, bool, error) {
	switch method {
	case "textDocument/hover":
		return respond(map[string]any{
			"contents": map[string]any{"kind": "markdown", "value": "```ktml\n" + compiler.ContentSignature(content.Component, content.Input) + "\n```"},
			"range":    rangeAt(source, content.Range.Start, content.Range.End),
		})
	case "textDocument/definition":
		return respond(locationOf(snapshot, content.Input.NameRange))
	case "textDocument/prepareRename":
		if content.Input.Implicit || content.Input.Name == compiler.ChildrenInput {
			return respond(nil)
		}
		return respond(map[string]any{
			"range":       rangeAt(source, content.Range.Start, content.Range.End),
			"placeholder": content.Input.Name,
		})
	}
	if indexed, err := semanticWorkspace(path).Index(); err == nil {
		if resolved, ok := indexed.ContentOccurrenceAt(path, offset); ok {
			snapshot, content = indexed, resolved
		}
	}
	switch method {
	case "textDocument/references":
		locations := []any{}
		if request.Context.IncludeDeclaration {
			locations = append(locations, locationOf(snapshot, content.Input.NameRange))
		}
		for _, reference := range snapshot.ContentUsesOf(content.Component.ID, content.Input.Name) {
			if reference == content.Input.NameRange {
				continue
			}
			locations = append(locations, locationOf(snapshot, reference))
		}
		return respond(locations)
	case "textDocument/rename":
		ranges, err := snapshot.ContentRename(content, request.NewName)
		if err != nil {
			return respond(nil)
		}
		return respond(map[string]any{"changes": renameChanges(snapshot, ranges, request.NewName)})
	}
	return nil, false, nil
}

// renameChanges groups rename edits by document URI.
func renameChanges(snapshot *compiler.Snapshot, ranges []compiler.SourceRange, newName string) map[string][]map[string]any {
	changes := make(map[string][]map[string]any)
	for _, current := range ranges {
		uri := "file://" + current.File
		changes[uri] = append(changes[uri], map[string]any{
			"range":   rangeAt(snapshot.Source(current.File), current.Start, current.End),
			"newText": newName,
		})
	}
	return changes
}

// documentSource returns the open editor text of a document, or its saved
// content when it is not open.
func documentSource(uri string) ([]byte, bool) {
	openDocumentsMu.Lock()
	text, open := openDocuments[uri]
	openDocumentsMu.Unlock()
	if open {
		return []byte(text), true
	}
	content, err := os.ReadFile(strings.TrimPrefix(uri, "file://"))
	if err != nil {
		return nil, false
	}
	return content, true
}

func locationOf(snapshot *compiler.Snapshot, location compiler.SourceRange) map[string]any {
	return map[string]any{
		"uri":   "file://" + location.File,
		"range": rangeAt(snapshot.Source(location.File), location.Start, location.End),
	}
}

var completionItemKinds = map[compiler.CompletionKind]int{
	compiler.FieldCompletion:       5,
	compiler.InputCompletion:       6,
	compiler.ComponentCompletion:   7,
	compiler.BindingCompletion:     6,
	compiler.ItemCompletion:        6,
	compiler.StateTypeCompletion:   13,
	compiler.VariantCompletion:     20,
	compiler.RecordTypeCompletion:  22,
	compiler.ScalarTypeCompletion:  25,
	compiler.SlotCompletion:        10,
	compiler.ContentTypeCompletion: 25,
	compiler.FixtureCompletion:     21,
	compiler.ValueCompletion:       12,
}

func completionItems(source []byte, offset int, list compiler.CompletionList) []map[string]any {
	items := make([]map[string]any, 0, len(list.Items))
	for index, item := range list.Items {
		items = append(items, map[string]any{
			"label":    item.Label,
			"kind":     completionItemKinds[item.Kind],
			"detail":   item.Detail,
			"sortText": sortKey(index),
			"textEdit": map[string]any{
				"range":   rangeAt(source, list.Start, offset),
				"newText": item.Label,
			},
		})
	}
	return items
}

func sortKey(index int) string {
	const digits = "0123456789"
	key := []byte{'0', '0', '0', '0'}
	for position := len(key) - 1; position >= 0 && index > 0; position-- {
		key[position] = digits[index%10]
		index /= 10
	}
	return string(key)
}

// encodeSemanticTokens converts sorted byte-range tokens into the LSP
// relative integer encoding over UTF-16 positions.
func encodeSemanticTokens(source []byte, tokens []compiler.SemanticToken) []int {
	typeIndex := make(map[compiler.SemanticTokenType]int, len(compiler.SemanticTokenTypes))
	for index, kind := range compiler.SemanticTokenTypes {
		typeIndex[kind] = index
	}
	sort.SliceStable(tokens, func(i, j int) bool { return tokens[i].Start < tokens[j].Start })
	data := make([]int, 0, len(tokens)*5)
	previousLine, previousCharacter := 0, 0
	positions := newLineTable(source)
	for _, current := range tokens {
		start, end := positions.position(current.Start), positions.position(current.End)
		line, character := start.Line, start.Character
		endLine, endCharacter := end.Line, end.Character
		if endLine != line || endCharacter <= character {
			continue
		}
		deltaCharacter := character
		if line == previousLine {
			deltaCharacter = character - previousCharacter
		}
		modifiers := 0
		if current.Declaration {
			modifiers = 1
		}
		data = append(data, line-previousLine, deltaCharacter, endCharacter-character, typeIndex[current.Type], modifiers)
		previousLine, previousCharacter = line, character
	}
	return data
}

// propagateOverlay mirrors an editor overlay into every other workspace,
// so requests on dependents in other directories, and the project index, see
// the unsaved text. They record it without analyzing: each analyzes again
// when next asked.
func propagateOverlay(primary *compiler.Workspace, path string, version int, text []byte, closed bool) {
	semanticWorkspacesMu.Lock()
	others := make([]*compiler.Workspace, 0, len(semanticWorkspaces))
	for _, candidate := range semanticWorkspaces {
		if candidate != primary {
			others = append(others, candidate)
		}
	}
	semanticWorkspacesMu.Unlock()
	for _, other := range others {
		if closed {
			_ = other.DropOverlay(path)
		} else {
			_ = other.Overlay(path, version, text)
		}
	}
}

// dependentDiagnostics republishes diagnostics for every other open document
// that transitively imports path, since an edit to path can change them.
func dependentDiagnostics(path string) []json.RawMessage {
	changed, err := filepath.Abs(path)
	if err != nil {
		return nil
	}
	documents := getOpenDocuments()
	uris := make([]string, 0, len(documents))
	for uri := range documents {
		uris = append(uris, uri)
	}
	sort.Strings(uris)
	var notifications []json.RawMessage
	for _, uri := range uris {
		other := strings.TrimPrefix(uri, "file://")
		if absolute, err := filepath.Abs(other); err != nil || absolute == changed {
			continue
		}
		snapshot, err := semanticSnapshot(other)
		if err != nil || !dependsOn(snapshot, other, changed) {
			continue
		}
		notification, err := json.Marshal(publishDiagnostics(uri, snapshot.Source(other), documentDiagnostics(snapshot, other)))
		if err == nil {
			notifications = append(notifications, notification)
		}
	}
	return notifications
}

func dependsOn(snapshot *compiler.Snapshot, file, target string) bool {
	visited := make(map[string]bool)
	pending := snapshot.Dependencies(file)
	for len(pending) > 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if current == target {
			return true
		}
		if visited[current] {
			continue
		}
		visited[current] = true
		pending = append(pending, snapshot.Dependencies(current)...)
	}
	return false
}

func publishDiagnostics(uri string, source []byte, diagnostics []compiler.Diagnostic) map[string]any {
	published := make([]map[string]any, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		published = append(published, map[string]any{
			"range":    rangeAt(source, diagnostic.Start, diagnostic.End),
			"severity": lspSeverity(diagnostic),
			"code":     diagnostic.Code,
			"source":   "ktml",
			"message":  diagnostic.Message,
		})
	}
	return map[string]any{
		"jsonrpc": "2.0",
		"method":  "textDocument/publishDiagnostics",
		"params":  map[string]any{"uri": uri, "diagnostics": published},
	}
}

// lspSeverity maps a compiler severity to the LSP DiagnosticSeverity: 1 for
// errors and 2 for warnings.
func lspSeverity(diagnostic compiler.Diagnostic) int {
	if diagnostic.Severity == compiler.SeverityWarning {
		return 2
	}
	return 1
}

// withDependentDiagnostics returns the primary notification alone, or a JSON
// array of notifications when open dependents need republished diagnostics.
func withDependentDiagnostics(primary []byte, path string) ([]byte, error) {
	dependents := dependentDiagnostics(path)
	if len(dependents) == 0 {
		return primary, nil
	}
	return json.Marshal(append([]json.RawMessage{primary}, dependents...))
}
