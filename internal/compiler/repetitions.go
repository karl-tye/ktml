package compiler

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/karl-tye/ktml/internal/ast"
)

// Repetition is a validated Keyed Repetition. Source is the repetition's
// stable source identity within its component (r0, r1, ... in source
// pre-order), and Parent is the enclosing repetition in the same component.
type Repetition struct {
	ID         SymbolID
	Component  SymbolID
	File       string
	Source     string
	Range      SourceRange
	Item       Binding
	Collection ResolvedPath
	Key        ResolvedPath
	KeyType    ValueType
	Parent     SymbolID
	Body       []ast.Node
	syntax     *ast.RepetitionNode
}

func repetitionID(path string, start int) SymbolID {
	return SymbolID(path + "::repetition::" + strconv.Itoa(start))
}

// resolveRepetition types a Keyed Repetition header and resolves its body with
// the item binding in scope. The collection is resolved in the enclosing
// scope; the key only sees the item.
func (s *Snapshot) resolveRepetition(path string, source []byte, caller pathScope, syntax *ast.RepetitionNode) {
	id := repetitionID(path, syntax.P)
	ordinal := 0
	if caller.sources != nil {
		ordinal = caller.sources.repetitions
		caller.sources.repetitions++
	}
	itemRange := SourceRange{File: path, Start: syntax.ItemRange.Start, End: syntax.ItemRange.End}
	repetition := Repetition{
		ID: id, Component: caller.component.ID, File: path, Source: "r" + strconv.Itoa(ordinal),
		Range:  SourceRange{File: path, Start: syntax.Pos(), End: syntax.End()},
		Parent: caller.parent, Body: syntax.Body, syntax: syntax,
	}

	var element Type
	if syntax.Collection != nil && len(syntax.Collection.Parts) > 0 {
		collection, ok := s.resolvePath(path, caller, syntax.Collection)
		spelling := syntax.Collection.String()
		switch {
		case !ok:
		case collection.Optional:
			s.addDiagnostic(path, "optional-collection", fmt.Sprintf("optional collection %s must be narrowed before repetition; test it with if %s? { … }, bind it with a Some clause, or carry it in a Variant payload", spelling, spelling), syntax.Collection.P, syntax.Collection.E)
		case collection.Type.Kind != CollectionTypeKind || collection.Type.Element == nil:
			s.addDiagnostic(path, "repetition-not-collection", fmt.Sprintf("cannot repeat over %s of type %s; a repetition requires a [T] collection", spelling, collection.Type), syntax.Collection.P, syntax.Collection.E)
		default:
			element = *collection.Type.Element
			repetition.Collection = collection
		}
	}

	if _, shadows := inputNamed(caller.component, syntax.Item); shadows {
		s.addDiagnostic(path, "shadowing-binding", fmt.Sprintf("repetition item %s shadows input %s of %s", syntax.Item, syntax.Item, caller.component.Name), itemRange.Start, itemRange.End)
	} else if outer, shadows := caller.binding(syntax.Item); shadows {
		kind := "binding"
		if outer.Repetition != "" {
			kind = "repetition item"
		}
		s.addDiagnostic(path, "shadowing-binding", fmt.Sprintf("repetition item %s shadows the enclosing %s %s", syntax.Item, kind, syntax.Item), itemRange.Start, itemRange.End)
	}
	repetition.Item = Binding{Name: syntax.Item, Type: element, Range: itemRange, Repetition: id}

	inner := caller
	inner.locals = append(append([]Binding(nil), caller.locals...), repetition.Item)
	inner.parent = id

	if key := syntax.Key; key != nil && len(key.Parts) > 0 {
		spelling := key.String()
		if key.Parts[0].Name != syntax.Item {
			s.addDiagnostic(path, "key-not-rooted", fmt.Sprintf("key %s must be the item %s or a path rooted in it", spelling, syntax.Item), key.P, key.E)
		} else if element.Kind != "" {
			if resolved, ok := s.resolvePath(path, inner, key); ok {
				switch {
				case resolved.Optional:
					s.addDiagnostic(path, "optional-key", fmt.Sprintf("key %s is optional; a key must be a present string or integer", spelling), key.P, key.E)
				case resolved.Type.IsScalar(StringType), resolved.Type.IsScalar(IntegerType):
					repetition.Key, repetition.KeyType = resolved, resolved.Type.Scalar
				default:
					s.addDiagnostic(path, "invalid-key-type", fmt.Sprintf("key %s is %s; a key must be string or integer", spelling, resolved.Type), key.P, key.E)
				}
			}
		}
	}

	s.repetitions[id] = repetition
	s.repetitionSyntax[syntax] = id
	for _, child := range syntax.Body {
		s.resolveNode(path, source, inner, child)
	}
}

// RepetitionFor returns the validated repetition for a repetition syntax node.
func (s *Snapshot) RepetitionFor(syntax *ast.RepetitionNode) (Repetition, bool) {
	if s == nil {
		return Repetition{}, false
	}
	id, ok := s.repetitionSyntax[syntax]
	if !ok {
		return Repetition{}, false
	}
	return s.repetitions[id], true
}

// Repetition returns a repetition by symbol.
func (s *Snapshot) Repetition(id SymbolID) (Repetition, bool) {
	if s == nil {
		return Repetition{}, false
	}
	repetition, ok := s.repetitions[id]
	return repetition, ok
}

// Repetitions returns the repetitions of one component document in source
// order.
func (s *Snapshot) Repetitions(file string) []Repetition {
	if s == nil {
		return nil
	}
	path, err := canonicalPath(file)
	if err != nil {
		return nil
	}
	var repetitions []Repetition
	for _, repetition := range s.repetitions {
		if repetition.File == path {
			repetitions = append(repetitions, repetition)
		}
	}
	sort.Slice(repetitions, func(i, j int) bool { return repetitions[i].Range.Start < repetitions[j].Range.Start })
	return repetitions
}
