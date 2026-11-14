package compiler

import (
	"fmt"
	"sort"
	"strings"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/parser"
	"github.com/karl-tye/ktml/internal/scanner"
	"github.com/karl-tye/ktml/internal/token"
)

// This file holds editor-tooling helpers for Host Schema records. They work on
// the source being edited, which may not parse, and fall back to snapshots for
// the semantic model of every other document.

// HasFile reports whether a snapshot loaded a document.
func (s *Snapshot) HasFile(file string) bool {
	if s == nil {
		return false
	}
	path, err := canonicalPath(file)
	if err != nil {
		return false
	}
	_, ok := s.files[path]
	return ok
}

// ValidRecordName reports whether name may name a record.
func ValidRecordName(name string) bool {
	return validTypeName(name) && !contextualTypes[name]
}

// ValidFieldName reports whether name may name a record field.
func ValidFieldName(name string) bool {
	return validFieldName(name)
}

// SchemaOccurrence is a record or field occurrence under a cursor.
type SchemaOccurrence struct {
	Symbol Symbol
	Range  SourceRange
	// Alias marks an occurrence spelled with an import alias.
	Alias bool
}

// SchemaOccurrenceAt resolves the record or field occurrence at offset and
// reports whether it is spelled with an import alias.
func (s *Snapshot) SchemaOccurrenceAt(file string, offset int) (SchemaOccurrence, bool) {
	symbol, occurrence, ok := s.SchemaSymbolAt(file, offset)
	if !ok {
		return SchemaOccurrence{}, false
	}
	result := SchemaOccurrence{Symbol: symbol, Range: occurrence}
	for _, reference := range s.References(symbol.ID) {
		if reference.Range == occurrence && reference.Alias {
			result.Alias = true
		}
	}
	return result, true
}

// PathPartAt returns the typed path part at offset in a component document.
func (s *Snapshot) PathPartAt(file string, offset int) (ResolvedPart, bool) {
	for _, resolved := range s.Paths(file) {
		for _, part := range resolved.Parts {
			if offset >= part.Range.Start && offset <= part.Range.End {
				return part, true
			}
		}
	}
	return ResolvedPart{}, false
}

// PartSignature renders a typed path part for hover text: the input
// declaration for a root, and the field signature for a field part.
func (s *Snapshot) PartSignature(part ResolvedPart) string {
	narrowed := ""
	if part.Narrowed {
		// A presence condition proves the optional present here.
		narrowed = "\nnarrowed to " + part.Type.String()
	}
	if part.Input != "" {
		optional := ""
		if part.Optional || part.Narrowed {
			optional = "?"
		}
		return "input " + part.Name + optional + ": " + part.Type.String() + narrowed
	}
	if symbol, ok := s.Symbol(part.Field); ok {
		return symbol.Detail + narrowed
	}
	return part.Name + ": " + part.Type.String() + narrowed
}

// SchemaRename returns every range a rename of a record or field must edit:
// the declaration and each reference spelled with the declared name. Aliased
// references keep their alias. The new name is validated for the symbol kind
// and for collisions in every affected namespace.
func (s *Snapshot) SchemaRename(id SymbolID, newName string) ([]SourceRange, error) {
	symbol, ok := s.Symbol(id)
	if !ok {
		return nil, fmt.Errorf("unknown symbol %s", id)
	}
	switch symbol.Kind {
	case RecordSymbol, StateSymbol:
		if !ValidRecordName(newName) {
			return nil, fmt.Errorf("%s name %s must be an ASCII UpperCamel identifier", symbol.Kind, newName)
		}
		declared := symbol.Range.File
		for file, names := range s.types {
			for local, target := range names {
				if target != id || (file != declared && local != symbol.Name) {
					continue
				}
				if other, exists := names[newName]; exists && other != id {
					return nil, fmt.Errorf("type name %s is already in scope in %s", newName, file)
				}
			}
		}
	case VariantSymbol:
		if !validTypeName(newName) {
			return nil, fmt.Errorf("Variant name %s must be an ASCII UpperCamel identifier", newName)
		}
		state := s.states[symbol.Record]
		if _, exists := state.Variant(newName); exists {
			return nil, fmt.Errorf("state %s already has a Variant %s", state.Name, newName)
		}
	case FieldSymbol:
		if !ValidFieldName(newName) {
			return nil, fmt.Errorf("field name %s must be an ASCII lowerCamel identifier", newName)
		}
		owner, fields := s.fieldOwner(symbol.Record)
		for _, field := range fields {
			if field.ID == id {
				continue
			}
			if field.Name == newName || field.GoName == GoFieldName(newName) {
				return nil, fmt.Errorf("%s already has a field that generates %s", owner, GoFieldName(newName))
			}
		}
	}
	ranges := []SourceRange{symbol.Range}
	for _, reference := range s.References(id) {
		if !reference.Alias {
			ranges = append(ranges, reference.Range)
		}
	}
	return uniqueRanges(ranges), nil
}

// fieldOwner returns the record or Variant that declares fields.
func (s *Snapshot) fieldOwner(id SymbolID) (string, []Field) {
	if record, ok := s.records[id]; ok {
		return "record " + record.Name, record.Fields
	}
	for _, state := range s.states {
		for _, variant := range state.Variants {
			if variant.ID == id {
				return "Variant " + state.Name + "." + variant.Name, variant.Fields
			}
		}
	}
	return "", nil
}

func uniqueRanges(ranges []SourceRange) []SourceRange {
	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].File != ranges[j].File {
			return ranges[i].File < ranges[j].File
		}
		return ranges[i].Start < ranges[j].Start
	})
	unique := ranges[:0]
	for index, current := range ranges {
		if index > 0 && current == ranges[index-1] {
			continue
		}
		unique = append(unique, current)
	}
	return unique
}

// CompletionKind classifies a Host Schema completion item.
type CompletionKind string

const (
	FieldCompletion      CompletionKind = "field"
	InputCompletion      CompletionKind = "input"
	ScalarTypeCompletion CompletionKind = "scalar"
	RecordTypeCompletion CompletionKind = "record"
	StateTypeCompletion  CompletionKind = "state"
	ComponentCompletion  CompletionKind = "component"
	// VariantCompletion is a Pattern: a Variant, or true, false, Some, or None.
	VariantCompletion CompletionKind = "variant"
	// BindingCompletion is a Render Clause binding offered as a path root.
	BindingCompletion CompletionKind = "binding"
)

// Completion is one Host Schema completion item.
type Completion struct {
	Label  string
	Detail string
	Kind   CompletionKind
}

// CompletionList holds completions that replace source[Start:offset].
type CompletionList struct {
	Start int
	Items []Completion
}

var scalarTypeNames = []ValueType{StringType, BooleanType, NumberType, IntegerType}

// SchemaCompletions completes Host Schema positions in a source that may not
// parse: fields and inputs in quoted interpolation paths, types in input and
// field type positions, and exported names inside import braces. Records and
// components of other documents are taken from the first snapshot that has
// them. The boolean reports whether offset is in such a position.
func SchemaCompletions(file string, source []byte, offset int, snapshots ...*Snapshot) (CompletionList, bool) {
	path, err := canonicalPath(file)
	if err != nil || offset < 0 || offset > len(source) {
		return CompletionList{}, false
	}
	tokens := lexemes(source)
	cursor := scanCursor(tokens, offset)
	if cursor.str >= 0 {
		return pathCompletions(path, source, tokens, cursor, offset, snapshots)
	}
	prefixStart := offset
	before := cursor.before
	if cursor.partial >= 0 {
		prefixStart = tokens[cursor.partial].start
	} else if n := len(before); n > 0 && tokens[before[n-1]].end == offset && tokens[before[n-1]].tok == token.IDENT {
		prefixStart = tokens[before[n-1]].start
		before = before[:n-1]
	}
	if len(cursor.stack) == 0 {
		return CompletionList{}, false
	}
	owner := cursor.stack[0].owner
	typePosition := false
	switch owner.kind {
	case recordOwner, componentOwner:
		typePosition = allBrackets(cursor.stack)
	case stateOwner:
		typePosition = len(cursor.stack) >= 2 && cursor.stack[0].tok == token.LBRACE && allBrackets(cursor.stack[1:])
	}
	if typePosition {
		if owner.kind == componentOwner && len(cursor.stack) == 1 && isClauseHeader(tokens, cursor.stack[0].opener) {
			return clauseHeaderCompletions(path, source, tokens, before, cursor.stack[0], prefixStart, snapshots)
		}
		last := len(before) - 1
		for last >= 0 && tokens[before[last]].tok == token.LBRACK {
			last--
		}
		if last < 0 || tokens[before[last]].tok != token.COLON {
			return CompletionList{}, false
		}
		items := typeCompletions(path, source, tokens, snapshots)
		if owner.kind == componentOwner && len(cursor.stack) == 1 {
			items = append(items, Completion{Label: ContentTypeName, Detail: "children or a named slot", Kind: ContentTypeCompletion})
		}
		return CompletionList{Start: prefixStart, Items: items}, true
	}
	if len(cursor.stack) == 1 && cursor.stack[0].tok == token.LBRACE && owner.kind == importOwner {
		if len(before) == 0 {
			return CompletionList{}, false
		}
		if previous := tokens[before[len(before)-1]]; previous.tok != token.LBRACE && previous.tok != token.COMMA {
			return CompletionList{}, false
		}
		return CompletionList{Start: prefixStart, Items: importCompletions(path, tokens, cursor.stack[0].opener, prefixStart, snapshots)}, true
	}
	end := 0
	if len(before) > 0 {
		end = before[len(before)-1] + 1
	}
	if list, ok := slotCompletions(path, source, tokens, cursor.stack, end, prefixStart, snapshots); ok {
		return list, true
	}
	if list, ok := conditionCompletions(path, source, tokens, cursor.stack, end, prefixStart, snapshots); ok {
		return list, true
	}
	return repetitionHeaderCompletions(path, source, tokens, cursor.stack, end, prefixStart, snapshots)
}

func allBrackets(frames []bracketFrame) bool {
	for _, frame := range frames {
		if frame.tok != token.LBRACK {
			return false
		}
	}
	return true
}

type lexeme struct {
	start, end int
	tok        token.Token
	lit        string
}

func lexemes(source []byte) []lexeme {
	var s scanner.Scanner
	s.Init(source)
	var tokens []lexeme
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			return tokens
		}
		end := pos + len(lit)
		if lit == "" {
			end = pos + 1
		}
		tokens = append(tokens, lexeme{start: pos, end: end, tok: tok, lit: lit})
	}
}

type ownerKind int

const (
	noOwner ownerKind = iota
	importOwner
	recordOwner
	componentOwner
	stateOwner
)

type declarationOwner struct {
	kind ownerKind
	name string
	head int // token index of the declaration name or import keyword
}

type bracketFrame struct {
	tok    token.Token
	opener int
	owner  declarationOwner
}

type cursorScan struct {
	before  []int // token indexes that end at or before the cursor
	stack   []bracketFrame
	str     int // token index of a double-quoted string containing the cursor
	partial int // token index of an identifier the cursor splits
}

// scanCursor walks the tokens before offset, tracking bracket nesting and the
// top-level declaration that owns each bracket.
func scanCursor(tokens []lexeme, offset int) cursorScan {
	result := cursorScan{str: -1, partial: -1}
	var top declarationOwner
	for index, current := range tokens {
		if current.start >= offset {
			break
		}
		quoted := (current.tok == token.STRING || current.tok == token.ILLEGAL) && strings.HasPrefix(current.lit, `"`)
		if quoted {
			terminated := current.tok == token.STRING
			if offset < current.end || (!terminated && offset == current.end) {
				result.str = index
				return result
			}
		}
		if current.end > offset {
			result.partial = index
			return result
		}
		result.before = append(result.before, index)
		depth := len(result.stack)
		switch current.tok {
		case token.IMPORT:
			if depth == 0 {
				top = declarationOwner{kind: importOwner, head: index}
			}
		case token.IDENT:
			if depth != 0 {
				break
			}
			awaitingName := (top.kind == recordOwner || top.kind == stateOwner) && top.name == ""
			if current.lit == "record" && top.kind != recordOwner {
				top = declarationOwner{kind: recordOwner, head: index}
			} else if current.lit == "state" && !awaitingName && index+1 < len(tokens) && tokens[index+1].tok == token.IDENT {
				top = declarationOwner{kind: stateOwner, head: index}
			} else if awaitingName {
				top.name, top.head = current.lit, index
			} else if current.lit == "default" && top.kind == componentOwner && index > 0 && tokens[index-1].tok == token.RBRACK {
				// The default keyword continues its Component Signature.
			} else {
				top = declarationOwner{kind: componentOwner, name: current.lit, head: index}
			}
		case token.LBRACK, token.LBRACE:
			owner := top
			if depth > 0 {
				owner = result.stack[0].owner
			}
			result.stack = append(result.stack, bracketFrame{tok: current.tok, opener: index, owner: owner})
		case token.RBRACK, token.RBRACE:
			if depth > 0 {
				result.stack = result.stack[:depth-1]
			}
			if depth == 1 && (top.kind == recordOwner || top.kind == stateOwner) {
				top = declarationOwner{}
			}
		}
	}
	return result
}

func pathCompletions(path string, source []byte, tokens []lexeme, cursor cursorScan, offset int, snapshots []*Snapshot) (CompletionList, bool) {
	str := tokens[cursor.str]
	content := string(source[str.start+1 : offset])
	open := strings.LastIndexByte(content, '{')
	if open < 0 {
		return CompletionList{}, false
	}
	expression := content[open+1:]
	for index := 0; index < len(expression); index++ {
		current := expression[index]
		if !isWordByte(current) && current != '.' {
			return CompletionList{}, false
		}
	}
	if len(cursor.stack) == 0 || cursor.stack[0].tok != token.LBRACE || cursor.stack[0].owner.kind != componentOwner {
		return CompletionList{}, false
	}
	parts := strings.Split(expression, ".")
	prefix := parts[len(parts)-1]
	list := CompletionList{Start: offset - len(prefix)}
	visible := textualTypes(path, source, tokens, snapshots)
	roots := scopeRoots(path, source, tokens, cursor.stack, visible, snapshots)
	if len(parts) == 1 {
		list.Items = rootCompletions(roots)
	} else {
		list.Items = fieldCompletions(roots, parts[:len(parts)-1], snapshots)
	}
	return list, true
}

func isWordByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '_' || value == '-'
}

func lookupRecord(id SymbolID, snapshots []*Snapshot) (Record, bool) {
	for _, snapshot := range snapshots {
		if record, ok := snapshot.Record(id); ok {
			return record, true
		}
	}
	return Record{}, false
}

func lookupState(id SymbolID, snapshots []*Snapshot) (State, bool) {
	for _, snapshot := range snapshots {
		if state, ok := snapshot.State(id); ok {
			return state, true
		}
	}
	return State{}, false
}

// visibleType is a record or state name in scope in the edited source. The
// record or state is zero when its declaring document has no usable snapshot.
type visibleType struct {
	id      SymbolID
	record  Record
	state   State
	isState bool
	found   bool
}

// name returns the declared name of a visible type, or fallback when its
// declaration has no usable snapshot.
func (v visibleType) name(fallback string) string {
	switch {
	case !v.found:
		return fallback
	case v.isState:
		return v.state.Name
	default:
		return v.record.Name
	}
}

// textualTypes finds the records and states in scope in an edited source from
// its own declarations and imports, without requiring the source to parse.
func textualTypes(path string, source []byte, tokens []lexeme, snapshots []*Snapshot) map[string]visibleType {
	visible := make(map[string]visibleType)
	depth := 0
	for index := 0; index < len(tokens); index++ {
		current := tokens[index]
		switch current.tok {
		case token.LBRACK, token.LBRACE:
			depth++
			continue
		case token.RBRACK, token.RBRACE:
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth != 0 {
			continue
		}
		if current.tok == token.IDENT && (current.lit == "record" || current.lit == "state") && IsSchemaPath(path) && index+1 < len(tokens) && tokens[index+1].tok == token.IDENT {
			name := tokens[index+1].lit
			if _, exists := visible[name]; exists {
				continue
			}
			if current.lit == "state" {
				id := stateID(path, name)
				state, found := lookupState(id, snapshots)
				visible[name] = visibleType{id: id, state: state, isState: true, found: found}
				continue
			}
			id := recordID(path, name)
			record, found := lookupRecord(id, snapshots)
			visible[name] = visibleType{id: id, record: record, found: found}
			continue
		}
		if current.tok != token.IMPORT {
			continue
		}
		statement, ok := parseImportAt(source, tokens, index)
		if !ok {
			continue
		}
		dependency, err := resolveImportPath(path, statement.Path)
		if err != nil || !IsSchemaPath(dependency) {
			continue
		}
		for _, specifier := range statement.Specifiers {
			if _, exists := visible[specifier.LocalName()]; exists {
				continue
			}
			if record, found := lookupRecord(recordID(dependency, specifier.Name), snapshots); found {
				visible[specifier.LocalName()] = visibleType{id: record.ID, record: record, found: true}
			} else if state, found := lookupState(stateID(dependency, specifier.Name), snapshots); found {
				visible[specifier.LocalName()] = visibleType{id: state.ID, state: state, isState: true, found: true}
			}
		}
	}
	return visible
}

// parseImportAt parses the import statement that starts at token index.
func parseImportAt(source []byte, tokens []lexeme, index int) (*ast.ImportStmt, bool) {
	end := -1
	for next := index + 1; next < len(tokens); next++ {
		if tokens[next].tok == token.IMPORT {
			break
		}
		if tokens[next].tok == token.STRING && tokens[next-1].tok == token.FROM {
			end = tokens[next].end
			break
		}
	}
	if end < 0 {
		return nil, false
	}
	document := recoverParse(source[tokens[index].start:end])
	if document == nil || len(document.Nodes) == 0 {
		return nil, false
	}
	statement, ok := document.Nodes[0].(*ast.ImportStmt)
	return statement, ok && statement.Path != ""
}

func recoverParse(source []byte) (document *ast.Document) {
	defer func() {
		if recover() != nil {
			document = nil
		}
	}()
	return parser.New(source).ParseDocument()
}

// componentInputs types the inputs of the component that owns the cursor from
// its declaration header in the edited source.
func componentInputs(path string, source []byte, tokens []lexeme, owner declarationOwner, visible map[string]visibleType, snapshots []*Snapshot) []Input {
	head := owner.head
	if head+1 >= len(tokens) || tokens[head+1].tok != token.LBRACK {
		if component, ok := resolveComponent(path, owner.name, snapshots); ok {
			return component.Inputs
		}
		return nil
	}
	depth := 0
	end := -1
	for index := head + 1; index < len(tokens) && end < 0; index++ {
		switch tokens[index].tok {
		case token.LBRACK:
			depth++
		case token.RBRACK:
			depth--
			if depth == 0 {
				end = tokens[index].end
			}
		case token.LBRACE, token.RBRACE:
			end = tokens[index].start
		}
	}
	if end < 0 {
		end = len(source)
	}
	header := append(append([]byte(nil), source[tokens[head].start:end]...), " {}"...)
	document := recoverParse(header)
	if document == nil || len(document.Nodes) == 0 {
		return nil
	}
	declaration, ok := document.Nodes[0].(*ast.ElementDecl)
	if !ok {
		return nil
	}
	var inputs []Input
	for _, attribute := range declaration.Attributes {
		if attribute.Kind != ast.InputDeclaration || attribute.TypeExpr == nil {
			continue
		}
		resolved, ok := textualType(attribute.TypeExpr, visible)
		if !ok {
			continue
		}
		inputs = append(inputs, Input{Name: attribute.Name, Type: resolved, Mode: InputMode(attribute.Mode)})
	}
	return inputs
}

func resolveComponent(path, name string, snapshots []*Snapshot) (Component, bool) {
	for _, snapshot := range snapshots {
		if component, ok := snapshot.Resolve(path, name); ok {
			return component, true
		}
	}
	return Component{}, false
}

func textualType(expression *ast.TypeExpr, visible map[string]visibleType) (Type, bool) {
	if expression == nil {
		return Type{}, false
	}
	if expression.Kind == ast.CollectionTypeExpr {
		element, ok := textualType(expression.Element, visible)
		if !ok {
			return Type{}, false
		}
		return Type{Kind: CollectionTypeKind, Element: &element}, true
	}
	for _, scalar := range scalarTypeNames {
		if expression.Name == string(scalar) {
			return ScalarTypeOf(scalar), true
		}
	}
	if loweredContextual(expression.Name) {
		return Type{Kind: ContextualTypeKind, Name: expression.Name}, true
	}
	if expression.Name == ContentTypeName {
		return Type{Kind: ContentTypeKind}, true
	}
	entry, ok := visible[expression.Name]
	if !ok {
		return Type{}, false
	}
	name := entry.name(expression.Name)
	if entry.isState {
		return Type{Kind: StateTypeKind, State: entry.id, Name: name}, true
	}
	return Type{Kind: RecordTypeKind, Record: entry.id, Name: name}, true
}

func typeCompletions(path string, source []byte, tokens []lexeme, snapshots []*Snapshot) []Completion {
	var items []Completion
	for _, scalar := range scalarTypeNames {
		items = append(items, Completion{Label: string(scalar), Detail: "scalar", Kind: ScalarTypeCompletion})
	}
	visible := textualTypes(path, source, tokens, snapshots)
	names := make([]string, 0, len(visible))
	for name := range visible {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		entry := visible[name]
		switch {
		case entry.isState && entry.found:
			items = append(items, Completion{Label: name, Detail: StateSignature(entry.state), Kind: StateTypeCompletion})
		case entry.isState:
			items = append(items, Completion{Label: name, Detail: "state " + name, Kind: StateTypeCompletion})
		case entry.found:
			items = append(items, Completion{Label: name, Detail: RecordSignature(entry.record), Kind: RecordTypeCompletion})
		default:
			items = append(items, Completion{Label: name, Detail: "record " + name, Kind: RecordTypeCompletion})
		}
	}
	return items
}

func importCompletions(path string, tokens []lexeme, opener, prefixStart int, snapshots []*Snapshot) []Completion {
	listed := make(map[string]bool)
	closing := -1
	for index := opener + 1; index < len(tokens); index++ {
		current := tokens[index]
		if current.tok == token.RBRACE {
			closing = index
			break
		}
		if current.tok != token.IDENT && current.tok != token.ILLEGAL {
			if current.tok != token.COMMA {
				return nil
			}
			continue
		}
		if current.lit == "as" || tokens[index-1].lit == "as" || current.start == prefixStart {
			continue
		}
		listed[current.lit] = true
	}
	if closing < 0 || closing+2 >= len(tokens) || tokens[closing+1].tok != token.FROM || tokens[closing+2].tok != token.STRING {
		return nil
	}
	dependency, err := resolveImportPath(path, tokens[closing+2].lit)
	if err != nil {
		return nil
	}
	var items []Completion
	if IsSchemaPath(dependency) {
		for _, snapshot := range snapshots {
			type exported struct {
				start int
				item  Completion
			}
			var types []exported
			for _, record := range snapshot.RecordsIn(dependency) {
				types = append(types, exported{record.Range.Start, Completion{Label: record.Name, Detail: RecordSignature(record), Kind: RecordTypeCompletion}})
			}
			for _, state := range snapshot.StatesIn(dependency) {
				types = append(types, exported{state.Range.Start, Completion{Label: state.Name, Detail: StateSignature(state), Kind: StateTypeCompletion}})
			}
			if len(types) == 0 {
				continue
			}
			sort.Slice(types, func(i, j int) bool { return types[i].start < types[j].start })
			for _, current := range types {
				if !listed[current.item.Label] {
					items = append(items, current.item)
				}
			}
			return items
		}
		return nil
	}
	if IsSchemaPath(path) {
		return nil
	}
	for _, snapshot := range snapshots {
		var components []Component
		for _, component := range snapshot.Components() {
			if component.File == dependency {
				components = append(components, component)
			}
		}
		if len(components) == 0 {
			continue
		}
		sort.Slice(components, func(i, j int) bool { return components[i].Name < components[j].Name })
		for _, component := range components {
			if !listed[component.Name] {
				items = append(items, Completion{Label: component.Name, Detail: ComponentSignature(component), Kind: ComponentCompletion})
			}
		}
		return items
	}
	return nil
}

// ComponentSignature renders a component and its typed inputs for tooling.
func ComponentSignature(component Component) string {
	parts := make([]string, len(component.Inputs))
	for index, input := range component.Inputs {
		parts[index] = input.Name + ": " + input.Type.String()
	}
	return "component " + component.Name + "(" + strings.Join(parts, ", ") + ")"
}

// SemanticTokenType names a semantic token class. The order of
// SemanticTokenTypes is the LSP legend.
type SemanticTokenType string

const (
	KeywordToken   SemanticTokenType = "keyword"
	TypeToken      SemanticTokenType = "type"
	PropertyToken  SemanticTokenType = "property"
	ParameterToken SemanticTokenType = "parameter"
	// EnumMemberToken classifies Variant declarations and Pattern names.
	EnumMemberToken SemanticTokenType = "enumMember"
	// OperatorToken classifies condition operators.
	OperatorToken SemanticTokenType = "operator"
)

// SemanticTokenTypes and SemanticTokenModifiers are the semantic token legend
// shared by the language server capabilities and the token encoder.
var (
	SemanticTokenTypes     = []SemanticTokenType{KeywordToken, TypeToken, PropertyToken, ParameterToken, EnumMemberToken, OperatorToken}
	SemanticTokenModifiers = []string{"declaration"}
)

// SemanticToken is a classified identifier range in one document.
type SemanticToken struct {
	Start       int
	End         int
	Type        SemanticTokenType
	Declaration bool
}

// SemanticTokens classifies Host Schema syntax in a source: record, state,
// default, import, from, and as keywords; record, state, and type names;
// Variant declarations and Pattern names; field declarations and path field
// parts; input declarations, clause selectors, bindings, and path roots; and
// Keyed Repetition for, in, and key keywords, item declarations, and
// collection and key paths. Tokens are sorted and do
// not overlap. Recovered syntax is used, so an invalid source still yields the
// tokens of its valid declarations.
func SemanticTokens(source []byte) []SemanticToken {
	document := recoverParse(source)
	if document == nil {
		return nil
	}
	var tokens []SemanticToken
	add := func(start, end int, kind SemanticTokenType, declaration bool) {
		if start < 0 || end > len(source) || start >= end {
			return
		}
		for index := start; index < end; index++ {
			if !isWordByte(source[index]) {
				return
			}
		}
		tokens = append(tokens, SemanticToken{Start: start, End: end, Type: kind, Declaration: declaration})
	}
	addOperator := func(start, end int) {
		if start >= 0 && end <= len(source) && start < end {
			tokens = append(tokens, SemanticToken{Start: start, End: end, Type: OperatorToken})
		}
	}
	var addType func(*ast.TypeExpr)
	addType = func(expression *ast.TypeExpr) {
		if expression == nil {
			return
		}
		if expression.Kind == ast.CollectionTypeExpr {
			addType(expression.Element)
			return
		}
		if expression.NameRange != nil {
			add(expression.NameRange.Start, expression.NameRange.End, TypeToken, false)
		}
	}
	addPath := func(path *ast.PathExpression) {
		if path == nil {
			return
		}
		for index, part := range path.Parts {
			kind := PropertyToken
			if index == 0 {
				kind = ParameterToken
			}
			add(part.P, part.E, kind, false)
		}
	}
	addQuoted := func(quoted *ast.QuotedValue) {
		if quoted == nil {
			return
		}
		for _, segment := range quoted.Segments {
			if segment.Kind == ast.PathSegment {
				addPath(segment.Path)
			}
		}
	}
	var addCondition func(*ast.ConditionExpr)
	addCondition = func(condition *ast.ConditionExpr) {
		if condition == nil {
			return
		}
		addPath(condition.Path)
		if condition.OperatorRange != nil {
			addOperator(condition.OperatorRange.Start, condition.OperatorRange.End)
		}
		for _, operator := range condition.OperatorRanges {
			addOperator(operator.Start, operator.End)
		}
		for _, operand := range condition.Operands {
			addCondition(operand)
		}
	}
	var addAttributes func([]*ast.AttributeNode)
	addAttributes = func(entries []*ast.AttributeNode) {
		for _, attribute := range entries {
			switch {
			case attribute == nil:
			case attribute.Condition != nil:
				group := attribute.Condition
				add(group.KeywordRange.Start, group.KeywordRange.End, KeywordToken, false)
				addCondition(group.Condition)
				addAttributes(group.Then)
				if group.ElseKeywordRange != nil {
					add(group.ElseKeywordRange.Start, group.ElseKeywordRange.End, KeywordToken, false)
				}
				addAttributes(group.Else)
			case attribute.Spread != nil:
				addOperator(attribute.Spread.OperatorRange.Start, attribute.Spread.OperatorRange.End)
				addPath(attribute.Spread.Path)
			default:
				addQuoted(attribute.Quoted)
			}
		}
	}
	// outlets holds the content input names of the declaration being walked.
	var outlets map[string]bool
	var addNode func(ast.Node)
	addNode = func(node ast.Node) {
		switch value := node.(type) {
		case *ast.TextNode:
			addQuoted(value.Quoted)
		case *ast.DoctypeNode:
			add(value.KeywordRange.Start, value.KeywordRange.End, KeywordToken, false)
		case *ast.SlotNode:
			add(value.KeywordRange.Start, value.KeywordRange.End, KeywordToken, false)
			add(value.NameRange.Start, value.NameRange.End, ParameterToken, false)
			for _, child := range value.Body {
				addNode(child)
			}
		case *ast.ConditionNode:
			add(value.KeywordRange.Start, value.KeywordRange.End, KeywordToken, false)
			addCondition(value.Condition)
			for _, child := range value.Then {
				addNode(child)
			}
			if value.ElseKeywordRange != nil {
				add(value.ElseKeywordRange.Start, value.ElseKeywordRange.End, KeywordToken, false)
			}
			for _, child := range value.Else {
				addNode(child)
			}
		case *ast.ElementNode:
			if outlets[value.Name] && len(value.Attributes) == 0 && len(value.Children) == 0 {
				add(value.P, value.P+len(value.Name), ParameterToken, false)
			}
			addAttributes(value.Attributes)
			for _, child := range value.Children {
				addNode(child)
			}
		case *ast.RepetitionNode:
			add(value.KeywordRange.Start, value.KeywordRange.End, KeywordToken, false)
			add(value.ItemRange.Start, value.ItemRange.End, ParameterToken, true)
			add(value.InRange.Start, value.InRange.End, KeywordToken, false)
			addPath(value.Collection)
			add(value.KeyKeywordRange.Start, value.KeyKeywordRange.End, KeywordToken, false)
			addPath(value.Key)
			for _, child := range value.Body {
				addNode(child)
			}
		}
	}
	var signature *ast.ElementDecl
	for _, node := range document.Nodes {
		switch value := node.(type) {
		case *ast.ElementDecl:
			signature = value
			outlets = declaredContent(value)
		case *ast.RenderClause:
			if signature == nil || signature.Name != value.Name {
				outlets = nil
			}
		default:
			signature, outlets = nil, nil
		}
		switch value := node.(type) {
		case *ast.ImportStmt:
			add(value.P, value.P+len("import"), KeywordToken, false)
			rawPath := strings.Trim(value.Path, "\"`")
			schemaImport := IsSchemaPath(rawPath)
			searchFrom := value.P + len("import")
			for _, specifier := range value.Specifiers {
				if schemaImport {
					add(specifier.NameRange.Start, specifier.NameRange.End, TypeToken, false)
				}
				searchFrom = specifier.NameRange.End
				if specifier.AliasRange != nil {
					if keyword := strings.Index(string(source[specifier.NameRange.End:specifier.AliasRange.Start]), "as"); keyword >= 0 {
						add(specifier.NameRange.End+keyword, specifier.NameRange.End+keyword+2, KeywordToken, false)
					}
					if schemaImport {
						add(specifier.AliasRange.Start, specifier.AliasRange.End, TypeToken, false)
					}
					searchFrom = specifier.AliasRange.End
				}
			}
			if value.PathRange.Start > searchFrom && value.PathRange.Start <= len(source) {
				if keyword := strings.LastIndex(string(source[searchFrom:value.PathRange.Start]), "from"); keyword >= 0 {
					add(searchFrom+keyword, searchFrom+keyword+len("from"), KeywordToken, false)
				}
			}
		case *ast.RecordDecl:
			add(value.KeywordRange.Start, value.KeywordRange.End, KeywordToken, false)
			add(value.NameRange.Start, value.NameRange.End, TypeToken, true)
			for _, field := range value.Fields {
				add(field.NameRange.Start, field.NameRange.End, PropertyToken, true)
				addType(field.TypeExpr)
			}
		case *ast.StateDecl:
			add(value.KeywordRange.Start, value.KeywordRange.End, KeywordToken, false)
			add(value.NameRange.Start, value.NameRange.End, TypeToken, true)
			for _, variant := range value.Variants {
				add(variant.NameRange.Start, variant.NameRange.End, EnumMemberToken, true)
				for _, field := range variant.Fields {
					add(field.NameRange.Start, field.NameRange.End, PropertyToken, true)
					addType(field.TypeExpr)
				}
			}
		case *ast.ElementDecl:
			for _, attribute := range value.Attributes {
				if attribute == nil || attribute.Kind != ast.InputDeclaration {
					continue
				}
				add(attribute.NameRange.Start, attribute.NameRange.End, ParameterToken, true)
				addType(attribute.TypeExpr)
			}
			if value.DefaultRange != nil {
				add(value.DefaultRange.Start, value.DefaultRange.End, KeywordToken, false)
			}
			for _, child := range value.Body {
				addNode(child)
			}
		case *ast.RenderClause:
			add(value.SelectorRange.Start, value.SelectorRange.End, ParameterToken, false)
			if value.Pattern.QualifierRange != nil {
				add(value.Pattern.QualifierRange.Start, value.Pattern.QualifierRange.End, TypeToken, false)
			}
			add(value.Pattern.NameRange.Start, value.Pattern.NameRange.End, EnumMemberToken, false)
			for _, binding := range value.Bindings {
				add(binding.FieldRange.Start, binding.FieldRange.End, PropertyToken, binding.AliasRange == nil)
				if binding.AliasRange == nil {
					continue
				}
				if binding.AliasRange.Start > binding.FieldRange.End && binding.AliasRange.Start <= len(source) {
					if keyword := strings.Index(string(source[binding.FieldRange.End:binding.AliasRange.Start]), "as"); keyword >= 0 {
						add(binding.FieldRange.End+keyword, binding.FieldRange.End+keyword+len("as"), KeywordToken, false)
					}
				}
				add(binding.AliasRange.Start, binding.AliasRange.End, ParameterToken, true)
			}
			for _, child := range value.Body {
				addNode(child)
			}
		}
	}
	sort.SliceStable(tokens, func(i, j int) bool { return tokens[i].Start < tokens[j].Start })
	unique := tokens[:0]
	for _, current := range tokens {
		if len(unique) > 0 && current.Start < unique[len(unique)-1].End {
			continue
		}
		unique = append(unique, current)
	}
	return unique
}
