package compiler

import (
	"fmt"
	"strings"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/token"
)

// This file holds editor-tooling helpers for Keyed Repetitions. Completion
// scans a source that may not parse: a repetition header is recognized by its
// `for item in path key path` tokens, and an enclosing repetition by the
// header that precedes an open brace. Navigation and rename use the snapshot's
// repetition syntax, so they also cover item uses inside paths that did not
// type.

// ItemCompletion is a Keyed Repetition item offered as a path root.
const ItemCompletion CompletionKind = "item"

// readPathBack reads a contiguous bare dotted path that ends at token index
// last and returns its parts and the index of its first token.
func readPathBack(tokens []lexeme, last int) ([]string, int, bool) {
	if last < 0 || last >= len(tokens) || tokens[last].tok != token.IDENT {
		return nil, 0, false
	}
	parts := []string{tokens[last].lit}
	first := last
	for first >= 2 && tokens[first-1].tok == token.PERIOD && tokens[first-2].tok == token.IDENT &&
		tokens[first-1].end == tokens[first].start && tokens[first-2].end == tokens[first-1].start {
		parts = append([]string{tokens[first-2].lit}, parts...)
		first -= 2
	}
	return parts, first, true
}

// headerBeforeIn reports whether the token before index in is `for item in`
// and returns the item token index.
func headerBeforeIn(tokens []lexeme, in int) (int, bool) {
	if in < 2 || tokens[in].tok != token.IDENT || tokens[in].lit != "in" {
		return 0, false
	}
	if tokens[in-1].tok != token.IDENT || tokens[in-2].tok != token.IDENT || tokens[in-2].lit != "for" {
		return 0, false
	}
	return in - 1, true
}

// textualRepetition is a repetition header recognized in an edited source.
type textualRepetition struct {
	item       string
	collection []string
}

// repetitionHeaderBefore recognizes a complete `for item in path key path`
// header whose last token is at index last.
func repetitionHeaderBefore(tokens []lexeme, last int) (textualRepetition, bool) {
	_, keyStart, ok := readPathBack(tokens, last)
	if !ok || keyStart < 1 || tokens[keyStart-1].tok != token.IDENT || tokens[keyStart-1].lit != "key" {
		return textualRepetition{}, false
	}
	collection, collectionStart, ok := readPathBack(tokens, keyStart-2)
	if !ok {
		return textualRepetition{}, false
	}
	item, ok := headerBeforeIn(tokens, collectionStart-1)
	if !ok {
		return textualRepetition{}, false
	}
	return textualRepetition{item: tokens[item].lit, collection: collection}, true
}

// scopeRoots returns the path roots visible at a cursor inside a component or
// clause body: the body's inputs and bindings, plus the item of every
// enclosing repetition, outermost first.
func scopeRoots(path string, source []byte, tokens []lexeme, stack []bracketFrame, visible map[string]visibleType, snapshots []*Snapshot) []pathRoot {
	roots := bodyRoots(path, source, tokens, stack[0].owner, visible, snapshots)
	for _, frame := range stack[1:] {
		if frame.tok != token.LBRACE {
			continue
		}
		roots = narrowRoots(roots, branchNarrowing(source, tokens, frame.opener))
		header, ok := repetitionHeaderBefore(tokens, frame.opener-1)
		if !ok {
			continue
		}
		roots = append(roots, itemRoot(header, roots, snapshots))
	}
	return roots
}

// itemRoot types a repetition item from its collection path in the
// enclosing roots. An item whose collection does not type has no type.
func itemRoot(header textualRepetition, roots []pathRoot, snapshots []*Snapshot) pathRoot {
	root := pathRoot{name: header.item, kind: ItemCompletion}
	collection, optional, ok := textualPathType(roots, header.collection, snapshots)
	if ok && !optional && collection.Kind == CollectionTypeKind && collection.Element != nil {
		root.typ = *collection.Element
	}
	return root
}

// textualPathType types a path against textual roots and record snapshots.
func textualPathType(roots []pathRoot, parts []string, snapshots []*Snapshot) (Type, bool, bool) {
	var current Type
	var narrowed []string
	optional, found := false, false
	for _, root := range roots {
		if root.name == parts[0] {
			current, optional, found, narrowed = root.typ, root.optional, true, root.narrowed
		}
	}
	if !found {
		return Type{}, false, false
	}
	for index, part := range parts[1:] {
		if optional || current.Kind != RecordTypeKind {
			return Type{}, false, false
		}
		record, ok := lookupRecord(current.Record, snapshots)
		if !ok {
			return Type{}, false, false
		}
		field, ok := record.Field(part)
		if !ok {
			return Type{}, false, false
		}
		current, optional = field.Type, field.Optional
		if optional && containsPath(narrowed, strings.Join(parts[:index+2], ".")) {
			optional = false
		}
	}
	return current, optional, true
}

// rootCompletions completes the first part of a path.
func rootCompletions(roots []pathRoot) []Completion {
	items := make([]Completion, 0, len(roots))
	for _, root := range roots {
		items = append(items, Completion{Label: root.name, Detail: root.typ.String(), Kind: root.kind})
	}
	return items
}

// fieldCompletions completes the part after qualifier, a path whose value
// must be a present record.
func fieldCompletions(roots []pathRoot, qualifier []string, snapshots []*Snapshot) []Completion {
	current, optional, ok := textualPathType(roots, qualifier, snapshots)
	if !ok || optional || current.Kind != RecordTypeKind {
		return nil
	}
	record, ok := lookupRecord(current.Record, snapshots)
	if !ok {
		return nil
	}
	items := make([]Completion, 0, len(record.Fields))
	for _, field := range record.Fields {
		items = append(items, Completion{Label: field.Name, Detail: field.Type.String(), Kind: FieldCompletion})
	}
	return items
}

// repetitionHeaderCompletions completes the collection path after `in` and
// the key path after `key` of a repetition header in a body. end is the token
// index the completed prefix starts at.
func repetitionHeaderCompletions(path string, source []byte, tokens []lexeme, stack []bracketFrame, end, prefixStart int, snapshots []*Snapshot) (CompletionList, bool) {
	if len(stack) == 0 || stack[0].tok != token.LBRACE || stack[0].owner.kind != componentOwner || end < 1 {
		return CompletionList{}, false
	}
	var qualifier []string
	keyword := end - 1
	if tokens[keyword].tok == token.PERIOD {
		if tokens[keyword].end != prefixStart {
			return CompletionList{}, false
		}
		parts, first, ok := readPathBack(tokens, keyword-1)
		if !ok || tokens[keyword-1].end != tokens[keyword].start {
			return CompletionList{}, false
		}
		qualifier, keyword = parts, first-1
	}
	if keyword < 0 || tokens[keyword].tok != token.IDENT {
		return CompletionList{}, false
	}
	list := CompletionList{Start: prefixStart}
	visible := textualTypes(path, source, tokens, snapshots)
	if _, ok := headerBeforeIn(tokens, keyword); ok {
		roots := scopeRoots(path, source, tokens, stack, visible, snapshots)
		if qualifier == nil {
			list.Items = rootCompletions(roots)
		} else {
			list.Items = fieldCompletions(roots, qualifier, snapshots)
		}
		return list, true
	}
	if tokens[keyword].lit != "key" {
		return CompletionList{}, false
	}
	collection, collectionStart, ok := readPathBack(tokens, keyword-1)
	if !ok {
		return CompletionList{}, false
	}
	item, ok := headerBeforeIn(tokens, collectionStart-1)
	if !ok {
		return CompletionList{}, false
	}
	roots := scopeRoots(path, source, tokens, stack, visible, snapshots)
	itemRoots := []pathRoot{itemRoot(textualRepetition{item: tokens[item].lit, collection: collection}, roots, snapshots)}
	if qualifier == nil {
		list.Items = rootCompletions(itemRoots)
	} else {
		list.Items = fieldCompletions(itemRoots, qualifier, snapshots)
	}
	return list, true
}

// repetitionItemReferences returns the key root and every body path root that
// names a repetition's item, from its syntax. Like the compiler, an outer item
// keeps resolving inside a nested repetition that reuses its name (a
// shadowing-binding error).
func repetitionItemReferences(file string, syntax *ast.RepetitionNode) []SourceRange {
	var ranges []SourceRange
	add := func(expression *ast.PathExpression) {
		if expression != nil && len(expression.Parts) > 0 && expression.Parts[0].Name == syntax.Item {
			root := expression.Parts[0]
			ranges = append(ranges, SourceRange{File: file, Start: root.P, End: root.E})
		}
	}
	addQuoted := func(quoted *ast.QuotedValue) {
		if quoted == nil {
			return
		}
		for _, segment := range quoted.Segments {
			if segment.Kind == ast.PathSegment {
				add(segment.Path)
			}
		}
	}
	var walk func(ast.Node)
	walk = func(node ast.Node) {
		switch value := node.(type) {
		case *ast.TextNode:
			addQuoted(value.Quoted)
		case *ast.ElementNode:
			for _, attribute := range ast.NamedAttributes(value.Attributes) {
				addQuoted(attribute.Quoted)
			}
			for _, child := range value.Children {
				walk(child)
			}
		case *ast.RepetitionNode:
			add(value.Collection)
			add(value.Key)
			for _, child := range value.Body {
				walk(child)
			}
		}
	}
	add(syntax.Key)
	for _, child := range syntax.Body {
		walk(child)
	}
	return ranges
}

// repetitionOfItem returns the repetition that declares an item binding.
func (s *Snapshot) repetitionOfItem(binding Binding) (Repetition, bool) {
	if binding.Repetition == "" {
		return Repetition{}, false
	}
	repetition, ok := s.Repetition(binding.Repetition)
	return repetition, ok && repetition.syntax != nil
}

// itemOccurrenceAt returns the item binding whose use, in a path that may not
// have typed, is at offset.
func (s *Snapshot) itemOccurrenceAt(file string, offset int) (BindingOccurrence, bool) {
	for _, repetition := range s.Repetitions(file) {
		if repetition.syntax == nil {
			continue
		}
		for _, use := range repetitionItemReferences(repetition.File, repetition.syntax) {
			if offset >= use.Start && offset <= use.End {
				return BindingOccurrence{Binding: repetition.Item, Range: use, Root: true}, true
			}
		}
	}
	return BindingOccurrence{}, false
}

// itemSignature renders a repetition item for hover text, followed by its
// repetition header.
func (s *Snapshot) itemSignature(binding Binding) string {
	signature := "item " + binding.Name
	if binding.Type.Kind != "" {
		signature += ": " + binding.Type.String()
	}
	repetition, ok := s.repetitionOfItem(binding)
	if !ok || repetition.syntax.Collection == nil || repetition.syntax.Key == nil {
		return signature
	}
	return signature + "\nfor " + binding.Name + " in " + repetition.syntax.Collection.String() + " key " + repetition.syntax.Key.String()
}

// ValidItemName reports whether name may name a repetition item: an
// identifier that is not a keyword.
func ValidItemName(name string) bool {
	if name == "" || token.Lookup(name) != token.IDENT {
		return false
	}
	for index := 0; index < len(name); index++ {
		current := name[index]
		letter := current >= 'a' && current <= 'z' || current >= 'A' && current <= 'Z' || current == '_'
		if index == 0 && !letter {
			return false
		}
		if !letter && !(current >= '0' && current <= '9') && current != '-' {
			return false
		}
	}
	return true
}

// ItemRename returns every range a rename of a repetition item must edit: its
// declaration, its key root, and every body path root that names it. The new
// name must be an identifier and may not shadow, or be shadowed by, an input,
// a Render Clause binding, or another item in scope.
func (s *Snapshot) ItemRename(binding Binding, newName string) ([]SourceRange, error) {
	repetition, ok := s.repetitionOfItem(binding)
	if !ok {
		return nil, fmt.Errorf("%s is not a repetition item", binding.Name)
	}
	if !ValidItemName(newName) {
		return nil, fmt.Errorf("item name %s must be an identifier", newName)
	}
	if newName == binding.Name {
		return nil, fmt.Errorf("item is already named %s", newName)
	}
	if component, ok := s.components[repetition.Component]; ok {
		if _, exists := inputNamed(component, newName); exists {
			return nil, fmt.Errorf("item name %s is an input of %s", newName, component.Name)
		}
	}
	for _, group := range s.Groups(repetition.File) {
		for _, clause := range group.Clauses {
			if clause.Range.Start > repetition.Range.Start || clause.Range.End < repetition.Range.End {
				continue
			}
			for _, local := range clause.Bindings {
				if local.Name == newName {
					return nil, fmt.Errorf("item name %s is a binding of the enclosing clause", newName)
				}
			}
		}
	}
	for _, other := range s.Repetitions(repetition.File) {
		if other.ID == repetition.ID || other.Item.Name != newName {
			continue
		}
		encloses := other.Range.Start <= repetition.Range.Start && other.Range.End >= repetition.Range.End
		nested := repetition.Range.Start <= other.Range.Start && repetition.Range.End >= other.Range.End
		if encloses || nested {
			return nil, fmt.Errorf("item name %s is an item of a nested or enclosing repetition", newName)
		}
	}
	ranges := append([]SourceRange{binding.Range}, repetitionItemReferences(repetition.File, repetition.syntax)...)
	return uniqueRanges(ranges), nil
}
