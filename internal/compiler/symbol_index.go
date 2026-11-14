package compiler

import (
	"sort"
	"strconv"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/symbolindex"
)

// IndexedSymbol is a declaration the workspace symbol index holds, with
// byte ranges.
type IndexedSymbol struct {
	ID        SymbolID
	Kind      symbolindex.Kind
	Name      string
	Range     SourceRange
	Detail    string
	Container SymbolID
}

// IndexedReference is a use of a symbol. From is the component or story the
// use is made in: a call graph edge when the use is a call.
type IndexedReference struct {
	Symbol SymbolID
	Kind   symbolindex.ReferenceKind
	Range  SourceRange
	From   SymbolID
	Alias  bool
}

// FileSymbols is everything the symbol index holds for one document.
type FileSymbols struct {
	Symbols    []IndexedSymbol
	References []IndexedReference
}

// InputID is the symbol of a component input.
func InputID(component SymbolID, name string) SymbolID {
	return SymbolID(string(component) + "::input::" + name)
}

// BindingID is the symbol of a clause binding or repetition item, by its
// declaration.
func BindingID(binding Binding) SymbolID {
	return SymbolID(binding.Range.File + "::binding::" + strconv.Itoa(binding.Range.Start))
}

// SymbolIndex returns the declarations and references of each of files that
// the snapshot loaded: components, inputs and slots, clause bindings and
// repetition items, import aliases, records, states, and their fields,
// stories and fixtures, and every reference to them, with the component or
// story each is made in.
func (s *Snapshot) SymbolIndex(files []string) map[string]FileSymbols {
	result := make(map[string]FileSymbols)
	if s == nil {
		return result
	}
	wanted := make(map[string]bool)
	for _, file := range files {
		if path, err := canonicalPath(file); err == nil {
			if _, loaded := s.files[path]; loaded {
				wanted[path] = true
				result[path] = FileSymbols{}
			}
		}
	}
	addSymbol := func(symbol IndexedSymbol) {
		if file := symbol.Range.File; wanted[file] {
			current := result[file]
			current.Symbols = append(current.Symbols, symbol)
			result[file] = current
		}
	}
	addReference := func(reference IndexedReference) {
		if file := reference.Range.File; wanted[file] {
			current := result[file]
			current.References = append(current.References, reference)
			result[file] = current
		}
	}

	for _, component := range s.components {
		addSymbol(IndexedSymbol{ID: component.ID, Kind: symbolindex.Component, Name: component.Name, Range: component.Range, Detail: componentSignature(component)})
		for _, input := range component.Inputs {
			if input.Implicit {
				continue
			}
			kind := symbolindex.Input
			if input.IsContent() {
				kind = symbolindex.Slot
			}
			addSymbol(IndexedSymbol{ID: InputID(component.ID, input.Name), Kind: kind, Name: input.Name, Range: input.NameRange, Detail: inputSignature(input), Container: component.ID})
		}
	}
	for _, record := range s.records {
		addSymbol(IndexedSymbol{ID: record.ID, Kind: symbolindex.Record, Name: record.Name, Range: record.Range, Detail: RecordSignature(record)})
		for _, field := range record.Fields {
			addSymbol(IndexedSymbol{ID: field.ID, Kind: symbolindex.Field, Name: field.Name, Range: field.NameRange, Detail: FieldSignature(record, field), Container: record.ID})
		}
	}
	for _, state := range s.states {
		addSymbol(IndexedSymbol{ID: state.ID, Kind: symbolindex.State, Name: state.Name, Range: state.Range, Detail: StateSignature(state)})
		for _, variant := range state.Variants {
			addSymbol(IndexedSymbol{ID: variant.ID, Kind: symbolindex.Variant, Name: variant.Name, Range: variant.Range, Detail: VariantSignature(state, variant), Container: state.ID})
			for _, field := range variant.Fields {
				addSymbol(IndexedSymbol{ID: field.ID, Kind: symbolindex.Field, Name: field.Name, Range: field.NameRange, Detail: state.Name + "." + variant.Name + "." + fieldSpelling(field), Container: variant.ID})
			}
		}
	}
	for _, story := range s.stories {
		addSymbol(IndexedSymbol{ID: story.ID, Kind: symbolindex.Story, Name: story.ComponentName + "." + story.Name, Range: story.NameRange, Container: story.Component})
		addReference(IndexedReference{Symbol: story.Component, Kind: symbolindex.CallReference, Range: story.ComponentRange, From: story.ID})
	}
	for _, fixture := range s.fixtures {
		addSymbol(IndexedSymbol{ID: fixture.ID, Kind: symbolindex.Fixture, Name: fixture.Name, Range: fixture.NameRange, Detail: "fixture " + fixture.Name + ": " + fixture.Type.String()})
	}
	for id, references := range s.references {
		for _, reference := range references {
			addReference(IndexedReference{Symbol: id, Kind: indexedReferenceKind(reference.Kind), Range: reference.Range, Alias: reference.Alias})
		}
	}

	for path := range wanted {
		s.indexDocument(path, addSymbol, addReference)
	}
	for path, symbols := range result {
		symbols.attribute(s.enclosing(path))
		sort.Slice(symbols.Symbols, func(i, j int) bool { return symbols.Symbols[i].Range.Start < symbols.Symbols[j].Range.Start })
		sort.Slice(symbols.References, func(i, j int) bool { return symbols.References[i].Range.Start < symbols.References[j].Range.Start })
		result[path] = symbols
	}
	return result
}

// indexDocument adds what only a document's syntax and per-file tooling
// know: call arguments and slot fills naming inputs, path roots, bindings,
// clause headers, overrides, aliases, and story arguments.
func (s *Snapshot) indexDocument(path string, addSymbol func(IndexedSymbol), addReference func(IndexedReference)) {
	file := s.files[path]
	if file.doc == nil {
		return
	}
	for _, resolved := range s.Paths(path) {
		if len(resolved.Parts) == 0 {
			continue
		}
		if root := resolved.Parts[0]; root.Input != "" && resolved.Component != "" {
			addReference(IndexedReference{Symbol: InputID(resolved.Component, root.Input), Kind: symbolindex.ReadReference, Range: root.Range})
		}
	}
	for _, use := range s.ContentUses(path) {
		addReference(IndexedReference{Symbol: InputID(use.Component, use.Input), Kind: symbolindex.ReadReference, Range: use.Range})
	}
	var bindings []Binding
	for _, repetition := range s.Repetitions(path) {
		bindings = append(bindings, repetition.Item)
	}
	for _, group := range s.Groups(path) {
		for _, clause := range group.Clauses {
			bindings = append(bindings, clause.Bindings...)
		}
	}
	for _, binding := range bindings {
		kind := symbolindex.Binding
		if binding.Repetition != "" {
			kind = symbolindex.LoopBinding
		}
		id := BindingID(binding)
		addSymbol(IndexedSymbol{ID: id, Kind: kind, Name: binding.Name, Range: binding.Range, Detail: s.BindingSignature(binding)})
		for _, use := range s.BindingReferences(binding) {
			addReference(IndexedReference{Symbol: id, Kind: symbolindex.ReadReference, Range: use})
		}
	}

	visible := s.visible[path]
	for _, node := range file.doc.Nodes {
		switch declaration := node.(type) {
		case *ast.ImportStmt:
			for _, specifier := range importSpecifiers(file.source, declaration) {
				if specifier.AliasRange == nil {
					continue
				}
				aliasRange := SourceRange{File: path, Start: specifier.AliasRange.Start, End: specifier.AliasRange.End}
				addSymbol(IndexedSymbol{ID: SymbolID(path + "::alias::" + specifier.Alias), Kind: symbolindex.Alias, Name: specifier.Alias, Range: aliasRange, Detail: "import " + specifier.Name + " as " + specifier.Alias})
			}
		case *ast.ElementDecl:
			if id, ok := visible[declaration.Name]; ok {
				if component := s.components[id]; component.Range.Start != declaration.Pos() || component.File != path {
					// An override declaration names its base component.
					addReference(IndexedReference{Symbol: id, Kind: symbolindex.WriteReference, Range: SourceRange{File: path, Start: declaration.Pos(), End: declaration.Pos() + len(declaration.Name)}})
				}
			}
			s.indexCalls(path, declaration.Body, addReference)
		case *ast.RenderClause:
			if id, ok := visible[declaration.Name]; ok {
				addReference(IndexedReference{Symbol: id, Kind: symbolindex.WriteReference, Range: SourceRange{File: path, Start: declaration.NameRange.Start, End: declaration.NameRange.End}})
			}
			s.indexCalls(path, declaration.Body, addReference)
		case *ast.StoryDecl:
			if id, ok := visible[declaration.Component]; ok {
				for _, argument := range declaration.Arguments {
					addReference(IndexedReference{Symbol: InputID(id, argument.Name), Kind: symbolindex.WriteReference, Range: SourceRange{File: path, Start: argument.NameRange.Start, End: argument.NameRange.End}})
				}
			}
			s.indexCalls(path, declaration.Body, addReference)
		}
	}
}

// indexCalls adds a reference to the target input for each named argument
// of each component call in body.
func (s *Snapshot) indexCalls(path string, body []ast.Node, addReference func(IndexedReference)) {
	visible := s.visible[path]
	var walk func(ast.Node)
	walk = func(node ast.Node) {
		switch value := node.(type) {
		case *ast.ElementNode:
			if id, call := visible[value.Name]; call {
				component := s.components[id]
				for _, attribute := range ast.NamedAttributes(value.Attributes) {
					if _, ok := inputNamed(component, attribute.Name); ok {
						addReference(IndexedReference{Symbol: InputID(id, attribute.Name), Kind: symbolindex.WriteReference, Range: SourceRange{File: path, Start: attribute.Pos(), End: attribute.Pos() + len(attribute.Name)}})
					}
				}
			}
			for _, child := range value.Children {
				walk(child)
			}
		case *ast.SlotNode:
			for _, child := range value.Body {
				walk(child)
			}
		case *ast.RepetitionNode:
			for _, child := range value.Body {
				walk(child)
			}
		case *ast.ConditionNode:
			for _, child := range value.Then {
				walk(child)
			}
			for _, child := range value.Else {
				walk(child)
			}
		}
	}
	for _, node := range body {
		walk(node)
	}
}

// enclosing returns the top-level declarations of a document with the
// symbol each declares or belongs to, for call graph edges.
func (s *Snapshot) enclosing(path string) []enclosingDeclaration {
	file := s.files[path]
	if file.doc == nil {
		return nil
	}
	var declarations []enclosingDeclaration
	visible := s.visible[path]
	for _, node := range file.doc.Nodes {
		var owner SymbolID
		switch declaration := node.(type) {
		case *ast.ElementDecl:
			owner = visible[declaration.Name]
		case *ast.RenderClause:
			owner = visible[declaration.Name]
		case *ast.StoryDecl:
			for _, story := range s.stories {
				if story.File == path && story.Range.Start == declaration.Pos() {
					owner = story.ID
				}
			}
		}
		if owner != "" {
			declarations = append(declarations, enclosingDeclaration{start: node.Pos(), end: node.End(), owner: owner})
		}
	}
	return declarations
}

type enclosingDeclaration struct {
	start, end int
	owner      SymbolID
}

// attribute sets each reference's From to the declaration it is made in,
// unless the reference names that declaration itself.
func (symbols *FileSymbols) attribute(declarations []enclosingDeclaration) {
	for i, reference := range symbols.References {
		if reference.From != "" {
			continue
		}
		for _, declaration := range declarations {
			if reference.Range.Start >= declaration.start && reference.Range.End <= declaration.end && declaration.owner != reference.Symbol {
				symbols.References[i].From = declaration.owner
				break
			}
		}
	}
}

func indexedReferenceKind(kind ReferenceKind) symbolindex.ReferenceKind {
	switch kind {
	case CallReference:
		return symbolindex.CallReference
	case ImportReference:
		return symbolindex.ImportReference
	case TypeReference, PatternReference:
		return symbolindex.TypeReference
	}
	return symbolindex.ReadReference
}

func componentSignature(component Component) string {
	parts := ""
	for i, input := range component.Inputs {
		if i > 0 {
			parts += ", "
		}
		parts += input.Name + ": " + input.Type.String()
	}
	return "component " + component.Name + "(" + parts + ")"
}

func inputSignature(input Input) string {
	optional := ""
	if input.Mode == OptionalInput {
		optional = "?"
	}
	return "input " + input.Name + optional + ": " + input.Type.String()
}
