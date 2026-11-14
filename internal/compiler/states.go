package compiler

import (
	"fmt"
	"sort"
	"strings"

	"github.com/karl-tye/ktml/internal/ast"
)

// State is a closed nominal Presentation State. Range is the name range.
type State struct {
	ID               SymbolID
	Name             string
	File             string
	Range            SourceRange
	DeclarationRange SourceRange
	Variants         []Variant
}

// Variant is one case of a state. Payload fields reuse the record field model,
// with Field.Record set to the Variant symbol.
type Variant struct {
	ID               SymbolID
	State            SymbolID
	Name             string
	Range            SourceRange
	DeclarationRange SourceRange
	Payload          bool
	Fields           []Field
}

// Variant returns the named Variant of a state.
func (s State) Variant(name string) (Variant, bool) {
	for _, variant := range s.Variants {
		if variant.Name == name {
			return variant, true
		}
	}
	return Variant{}, false
}

// Field returns the named payload field of a Variant.
func (v Variant) Field(name string) (Field, bool) {
	for _, field := range v.Fields {
		if field.Name == name {
			return field, true
		}
	}
	return Field{}, false
}

// SelectorKind classifies the input a clause group selects on.
type SelectorKind string

const (
	StateSelector    SelectorKind = "state"
	BooleanSelector  SelectorKind = "boolean"
	OptionalSelector SelectorKind = "optional"
)

// Built-in Pattern cases for boolean and optional selectors.
const (
	TrueCase  = "true"
	FalseCase = "false"
	SomeCase  = "Some"
	NoneCase  = "None"
)

// ClauseGroup is a Component Signature with its Render Clauses. Cases lists
// every case of the selector in canonical order; Residual lists the cases only
// the default body handles.
type ClauseGroup struct {
	Component    SymbolID
	File         string
	Selector     Input
	Kind         SelectorKind
	Cases        []string
	Clauses      []Clause
	HasDefault   bool
	DefaultRange SourceRange
	DefaultBody  []ast.Node
	Residual     []string
}

// Clause is one validated Render Clause. Case is the Variant name or built-in
// case it matches; Variant is set for state selectors.
type Clause struct {
	Range        SourceRange
	PatternRange SourceRange
	Case         string
	Variant      SymbolID
	Bindings     []Binding
	Body         []ast.Node
	syntax       *ast.RenderClause
}

// Binding is an immutable payload binding introduced by a clause header, or
// the item binding of a Keyed Repetition when Repetition is set.
type Binding struct {
	Name       string
	Field      string
	Alias      string
	FieldID    SymbolID
	Type       Type
	Optional   bool
	Range      SourceRange
	FieldRange SourceRange
	Repetition SymbolID
}

// pathScope is the set of path roots visible in a body: the component inputs
// plus any clause bindings and enclosing repetition items. parent is the
// innermost enclosing repetition, and sources numbers the component's
// repetitions, component calls, and content outlets in source order.
type pathScope struct {
	component Component
	locals    []Binding
	parent    SymbolID
	sources   *sourceCounters
	// narrowed holds the path spellings a presence condition proves present.
	narrowed []string
}

// sourceCounters numbers the Render Identity sources of one component across
// its default and clause bodies, in source pre-order: r<n> repetitions, c<n>
// component calls, and o<n> content outlets.
type sourceCounters struct {
	repetitions int
	calls       int
	outlets     int
}

// narrow reads an optional part as its value type when spelling is narrowed.
func (scope pathScope) narrow(part ResolvedPart, spelling string) ResolvedPart {
	if !part.Optional {
		return part
	}
	for _, narrowed := range scope.narrowed {
		if narrowed == spelling {
			part.Optional, part.Narrowed = false, true
			return part
		}
	}
	return part
}

// withNarrowed returns the scope with additional narrowed paths.
func (scope pathScope) withNarrowed(paths []string) pathScope {
	if len(paths) == 0 {
		return scope
	}
	scope.narrowed = append(append([]string(nil), scope.narrowed...), paths...)
	return scope
}

func (scope pathScope) binding(name string) (Binding, bool) {
	for _, local := range scope.locals {
		if local.Name == name {
			return local, true
		}
	}
	return Binding{}, false
}

const (
	VariantSymbol SymbolKind = "variant"
	StateSymbol   SymbolKind = "state"

	PatternReference ReferenceKind = "pattern"
)

func stateID(path, name string) SymbolID {
	return SymbolID(path + "::state::" + name)
}

func variantID(state SymbolID, name string) SymbolID {
	return SymbolID(string(state) + "::variant::" + name)
}

func sortedStateIDs(states map[SymbolID]State) []SymbolID {
	ids := make([]SymbolID, 0, len(states))
	for id := range states {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// exportedType returns the record or state a schema Document declares.
func (s *Snapshot) exportedType(path, name string) (SymbolID, bool) {
	if id := recordID(path, name); s.records[id].ID != "" {
		return id, true
	}
	if id := stateID(path, name); s.states[id].ID != "" {
		return id, true
	}
	return "", false
}

// StateGoNames returns every exported Go name the generator declares for a
// state: the interface, the discriminator type and constants, payload structs,
// constructors, and payload accessors.
func StateGoNames(state State) []string {
	names := []string{state.Name, state.Name + "Variant"}
	for _, variant := range state.Variants {
		names = append(names, state.Name+"Variant"+variant.Name, "New"+state.Name+variant.Name)
		if variant.Payload {
			names = append(names, state.Name+variant.Name, "As"+state.Name+variant.Name)
		}
	}
	return names
}

func (s *Snapshot) indexStates() {
	for _, path := range sortedFilePaths(s.files) {
		file := s.files[path]
		if file.doc == nil {
			continue
		}
		if s.types[path] == nil {
			s.types[path] = make(map[string]SymbolID)
		}
		for _, node := range file.doc.Nodes {
			declaration, ok := node.(*ast.StateDecl)
			if !ok {
				continue
			}
			if !IsSchemaPath(path) {
				s.addDiagnostic(path, "state-outside-schema", fmt.Sprintf("state %s must be declared in a .ktmlschema document", declaration.Name), declaration.KeywordRange.Start, declaration.NameRange.End)
				continue
			}
			nameRange := declaration.NameRange
			switch {
			case contextualTypes[declaration.Name] || isScalarName(declaration.Name):
				s.addDiagnostic(path, "reserved-type-name", fmt.Sprintf("%s is a reserved type name", declaration.Name), nameRange.Start, nameRange.End)
				continue
			case !validTypeName(declaration.Name):
				s.addDiagnostic(path, "invalid-type-name", fmt.Sprintf("state name %s must be an ASCII UpperCamel identifier", declaration.Name), nameRange.Start, nameRange.End)
				continue
			}
			if _, duplicate := s.types[path][declaration.Name]; duplicate {
				s.addDiagnostic(path, "duplicate-record", fmt.Sprintf("type %s is declared more than once", declaration.Name), nameRange.Start, nameRange.End)
				continue
			}
			id := stateID(path, declaration.Name)
			state := State{
				ID: id, Name: declaration.Name, File: path,
				Range:            SourceRange{File: path, Start: nameRange.Start, End: nameRange.End},
				DeclarationRange: SourceRange{File: path, Start: declaration.Pos(), End: declaration.End()},
			}
			if len(declaration.Variants) == 0 {
				s.addDiagnostic(path, "empty-state", fmt.Sprintf("state %s must declare at least one Variant", declaration.Name), nameRange.Start, nameRange.End)
			}
			seen := make(map[string]bool)
			for _, variantDeclaration := range declaration.Variants {
				variantRange := variantDeclaration.NameRange
				if !validTypeName(variantDeclaration.Name) {
					s.addDiagnostic(path, "invalid-variant-name", fmt.Sprintf("Variant name %s must be an ASCII UpperCamel identifier", variantDeclaration.Name), variantRange.Start, variantRange.End)
					continue
				}
				if seen[variantDeclaration.Name] {
					s.addDiagnostic(path, "duplicate-variant", fmt.Sprintf("state %s declares Variant %s more than once", declaration.Name, variantDeclaration.Name), variantRange.Start, variantRange.End)
					continue
				}
				seen[variantDeclaration.Name] = true
				if variantDeclaration.Payload && len(variantDeclaration.Fields) == 0 {
					payload := variantRange
					if variantDeclaration.PayloadRange != nil {
						payload = *variantDeclaration.PayloadRange
					}
					s.addDiagnostic(path, "empty-variant-payload", fmt.Sprintf("Variant %s has an empty payload; write a nullary Variant without brackets", variantDeclaration.Name), payload.Start, payload.End)
				}
				vid := variantID(id, variantDeclaration.Name)
				variant := Variant{
					ID: vid, State: id, Name: variantDeclaration.Name, Payload: variantDeclaration.Payload,
					Range:            SourceRange{File: path, Start: variantRange.Start, End: variantRange.End},
					DeclarationRange: SourceRange{File: path, Start: variantDeclaration.Pos(), End: variantDeclaration.End()},
				}
				variant.Fields = s.indexFields(path, declaration.Name+"."+variantDeclaration.Name, vid, variantDeclaration.Fields)
				state.Variants = append(state.Variants, variant)
			}
			s.states[id] = state
			s.types[path][declaration.Name] = id
		}
	}
}

func isScalarName(name string) bool {
	for _, scalar := range scalarTypeNames {
		if name == string(scalar) {
			return true
		}
	}
	return false
}

// indexFields validates record or payload field declarations.
func (s *Snapshot) indexFields(path, owner string, ownerID SymbolID, declarations []*ast.FieldDecl) []Field {
	var fields []Field
	names := make(map[string]bool)
	goNames := make(map[string]string)
	for _, fieldDeclaration := range declarations {
		fieldNameRange := fieldDeclaration.NameRange
		if !validFieldName(fieldDeclaration.Name) {
			s.addDiagnostic(path, "invalid-field-name", fmt.Sprintf("field name %s must be an ASCII lowerCamel identifier", fieldDeclaration.Name), fieldNameRange.Start, fieldNameRange.End)
			continue
		}
		if names[fieldDeclaration.Name] {
			s.addDiagnostic(path, "duplicate-field", fmt.Sprintf("%s declares field %s more than once", owner, fieldDeclaration.Name), fieldNameRange.Start, fieldNameRange.End)
			continue
		}
		names[fieldDeclaration.Name] = true
		goName := GoFieldName(fieldDeclaration.Name)
		if first, collision := goNames[goName]; collision {
			s.addDiagnostic(path, "generated-field-collision", fmt.Sprintf("fields %s and %s both generate Go field %s", first, fieldDeclaration.Name, goName), fieldNameRange.Start, fieldNameRange.End)
			continue
		}
		goNames[goName] = fieldDeclaration.Name
		field := Field{
			ID: fieldID(ownerID, fieldDeclaration.Name), Record: ownerID, Name: fieldDeclaration.Name, GoName: goName,
			Optional:  fieldDeclaration.Optional,
			Range:     SourceRange{File: path, Start: fieldDeclaration.Pos(), End: fieldDeclaration.End()},
			NameRange: SourceRange{File: path, Start: fieldNameRange.Start, End: fieldNameRange.End},
			TypeRange: SourceRange{File: path, Start: fieldDeclaration.TypeRange.Start, End: fieldDeclaration.TypeRange.End},
			typeExpr:  typeExpression(fieldDeclaration.TypeExpr, fieldDeclaration.TypeDecl, fieldDeclaration.TypeRange),
		}
		if fieldDeclaration.OptionalMarker != nil {
			field.OptionalMarkerRange = &SourceRange{File: path, Start: fieldDeclaration.OptionalMarker.Start, End: fieldDeclaration.OptionalMarker.End}
		}
		fields = append(fields, field)
	}
	return fields
}

func (s *Snapshot) resolveStateFields() {
	for _, id := range sortedStateIDs(s.states) {
		state := s.states[id]
		for index, variant := range state.Variants {
			fields := variant.Fields[:0]
			for _, field := range variant.Fields {
				resolved, ok := s.resolveType(state.File, field.typeExpr)
				if !ok {
					continue
				}
				field.Type = resolved
				fields = append(fields, field)
			}
			state.Variants[index].Fields = fields
		}
		s.states[id] = state
	}
}

// resolveClauseGroups groups each component Document's signatures and Render
// Clauses in source order and validates selectors, Patterns, bindings, and
// exhaustiveness.
func (s *Snapshot) resolveClauseGroups() {
	for _, path := range sortedFilePaths(s.files) {
		file := s.files[path]
		if file.doc == nil || IsSchemaPath(path) {
			continue
		}
		var current *ast.ElementDecl
		var members []*ast.RenderClause
		var previous ast.Node
		seenGroups := make(map[string]bool)
		flush := func() {
			if current != nil {
				s.resolveClauseGroup(path, current, members)
			}
			current, members = nil, nil
		}
		for _, node := range file.doc.Nodes {
			switch declaration := node.(type) {
			case *ast.RenderClause:
				if current != nil && current.Name == declaration.Name {
					members = append(members, declaration)
					break
				}
				nameRange := declaration.NameRange
				switch {
				case seenGroups[declaration.Name] || current != nil && current.Name == declaration.Name:
					s.addDiagnostic(path, "noncontiguous-clause", fmt.Sprintf("render clause for %s must directly follow its signature group; another declaration ended the group", declaration.Name), nameRange.Start, nameRange.End)
				case isOrdinaryDeclaration(previous, declaration.Name):
					s.addDiagnostic(path, "clause-after-component", fmt.Sprintf("component %s has a body, so it is not a Component Signature for render clauses", declaration.Name), nameRange.Start, nameRange.End)
				default:
					s.addDiagnostic(path, "clause-without-signature", fmt.Sprintf("render clause for %s must follow a Component Signature for %s in the same document", declaration.Name, declaration.Name), nameRange.Start, nameRange.End)
				}
			case *ast.ElementDecl:
				flush()
				if declaration.IsSignature() {
					current = declaration
					seenGroups[declaration.Name] = true
				}
			default:
				flush()
			}
			previous = node
		}
		flush()
	}
}

func isOrdinaryDeclaration(node ast.Node, name string) bool {
	declaration, ok := node.(*ast.ElementDecl)
	return ok && declaration.Name == name && !declaration.IsSignature()
}

func (s *Snapshot) resolveClauseGroup(path string, signature *ast.ElementDecl, members []*ast.RenderClause) {
	nameRange := SourceRange{File: path, Start: signature.Pos(), End: signature.Pos() + len(signature.Name)}
	id, ok := s.visible[path][signature.Name]
	if !ok || s.components[id].File != path {
		return
	}
	component := s.components[id]
	if len(members) == 0 {
		s.addDiagnostic(path, "missing-clauses", fmt.Sprintf("Component Signature %s must be followed by at least one render clause", signature.Name), nameRange.Start, nameRange.End)
		return
	}
	first := members[0]
	selector, found := inputNamed(component, first.Selector)
	if !found {
		s.addDiagnostic(path, "unknown-selector", fmt.Sprintf("component %s has no input %s to select on", signature.Name, first.Selector), first.SelectorRange.Start, first.SelectorRange.End)
		return
	}
	group := ClauseGroup{Component: id, File: path, Selector: selector}
	switch {
	case selector.Type.Kind == ContentTypeKind:
		s.addDiagnostic(path, "invalid-selector-type", fmt.Sprintf("selector %s is content; render clauses select on a Presentation State, boolean, or optional value input", selector.Name), first.SelectorRange.Start, first.SelectorRange.End)
		return
	case selector.Mode == OptionalInput:
		group.Kind, group.Cases = OptionalSelector, []string{SomeCase, NoneCase}
	case selector.Type.Kind == StateTypeKind:
		group.Kind = StateSelector
		for _, variant := range s.states[selector.Type.State].Variants {
			group.Cases = append(group.Cases, variant.Name)
		}
	case selector.Type.IsScalar(BooleanType):
		group.Kind, group.Cases = BooleanSelector, []string{TrueCase, FalseCase}
	default:
		s.addDiagnostic(path, "invalid-selector-type", fmt.Sprintf("selector %s is %s; render clauses select on a Presentation State, boolean, or optional input", selector.Name, selector.Type), first.SelectorRange.Start, first.SelectorRange.End)
		return
	}
	valid := true
	covered := make(map[string]bool)
	for _, member := range members {
		if member.Selector != selector.Name {
			s.addDiagnostic(path, "mixed-selector", fmt.Sprintf("every render clause of %s must match input %s, not %s", signature.Name, selector.Name, member.Selector), member.SelectorRange.Start, member.SelectorRange.End)
			valid = false
			continue
		}
		clause, ok := s.resolveClause(path, component, group, member)
		if !ok {
			valid = false
			continue
		}
		if covered[clause.Case] {
			s.addDiagnostic(path, "overlapping-clause", fmt.Sprintf("%s already has a render clause for %s", signature.Name, clause.Case), clause.PatternRange.Start, clause.PatternRange.End)
			valid = false
			continue
		}
		covered[clause.Case] = true
		group.Clauses = append(group.Clauses, clause)
	}
	var residual []string
	for _, name := range group.Cases {
		if !covered[name] {
			residual = append(residual, name)
		}
	}
	if signature.DefaultRange == nil {
		if valid && len(residual) > 0 {
			s.addDiagnostic(path, "non-exhaustive-clauses", fmt.Sprintf("render clauses of %s do not cover %s; add clauses or an explicit default body", signature.Name, strings.Join(residual, ", ")), nameRange.Start, nameRange.End)
			valid = false
		}
	} else {
		group.HasDefault = true
		group.DefaultRange = SourceRange{File: path, Start: signature.DefaultRange.Start, End: signature.DefaultRange.End}
		group.DefaultBody = signature.Body
		group.Residual = residual
		if valid {
			if len(residual) > 0 {
				s.addWarning(path, "default-fallthrough", fmt.Sprintf("default body of %s handles %s", signature.Name, strings.Join(residual, ", ")), group.DefaultRange.Start, group.DefaultRange.End)
			} else {
				s.addWarning(path, "unreachable-default", fmt.Sprintf("default body of %s is unreachable; every case has a render clause", signature.Name), group.DefaultRange.Start, group.DefaultRange.End)
			}
		}
	}
	s.groups[id] = group
}

// resolveClause resolves one clause's Pattern and bindings against the group
// selector, recording Variant and payload-field references for tooling.
func (s *Snapshot) resolveClause(path string, component Component, group ClauseGroup, member *ast.RenderClause) (Clause, bool) {
	pattern := member.Pattern
	patternRange := SourceRange{File: path, Start: pattern.P, End: pattern.E}
	nameRange := SourceRange{File: path, Start: pattern.NameRange.Start, End: pattern.NameRange.End}
	clause := Clause{
		Range:        SourceRange{File: path, Start: member.Pos(), End: member.End()},
		PatternRange: patternRange, Body: member.Body, syntax: member,
	}
	mismatch := func(expected string) (Clause, bool) {
		s.addDiagnostic(path, "pattern-type-mismatch", fmt.Sprintf("Pattern %s does not match selector %s of type %s; expected %s", pattern.String(), group.Selector.Name, selectorTypeName(group.Selector), expected), patternRange.Start, patternRange.End)
		return Clause{}, false
	}
	var payload []Field
	switch group.Kind {
	case StateSelector:
		state := s.states[group.Selector.Type.State]
		if pattern.Qualifier != "" {
			if qualified, ok := s.types[path][pattern.Qualifier]; !ok || qualified != state.ID {
				return mismatch("a Variant of " + state.Name)
			}
			qualifierRange := SourceRange{File: path, Start: pattern.QualifierRange.Start, End: pattern.QualifierRange.End}
			s.references[state.ID] = append(s.references[state.ID], Reference{Symbol: state.ID, Kind: TypeReference, Range: qualifierRange, Alias: pattern.Qualifier != state.Name})
		}
		variant, ok := state.Variant(pattern.Name)
		if !ok {
			if pattern.Qualifier == "" && isBuiltinPattern(pattern.Name) {
				return mismatch("a Variant of " + state.Name)
			}
			s.addDiagnostic(path, "unknown-variant", fmt.Sprintf("state %s has no Variant %s", state.Name, pattern.Name), nameRange.Start, nameRange.End)
			return Clause{}, false
		}
		s.references[variant.ID] = append(s.references[variant.ID], Reference{Symbol: variant.ID, Kind: PatternReference, Range: nameRange})
		clause.Case, clause.Variant, payload = variant.Name, variant.ID, variant.Fields
	case BooleanSelector:
		if pattern.Qualifier != "" || (pattern.Name != TrueCase && pattern.Name != FalseCase) {
			return mismatch("true or false")
		}
		clause.Case = pattern.Name
	case OptionalSelector:
		if pattern.Qualifier != "" || (pattern.Name != SomeCase && pattern.Name != NoneCase) {
			return mismatch("Some or None")
		}
		clause.Case = pattern.Name
		if pattern.Name == SomeCase {
			payload = []Field{{Name: "value", Type: group.Selector.Type}}
		}
	}
	valid := true
	fields := make(map[string]bool)
	locals := make(map[string]bool)
	for _, syntax := range member.Bindings {
		fieldRange := SourceRange{File: path, Start: syntax.FieldRange.Start, End: syntax.FieldRange.End}
		localRange := fieldRange
		if syntax.AliasRange != nil {
			localRange = SourceRange{File: path, Start: syntax.AliasRange.Start, End: syntax.AliasRange.End}
		}
		if payload == nil {
			s.addDiagnostic(path, "unexpected-binding", fmt.Sprintf("%s has no payload to bind", pattern.String()), fieldRange.Start, fieldRange.End)
			valid = false
			continue
		}
		var field Field
		found := false
		for _, candidate := range payload {
			if candidate.Name == syntax.Field {
				field, found = candidate, true
			}
		}
		if !found {
			s.addDiagnostic(path, "unknown-binding", fmt.Sprintf("%s has no payload field %s", pattern.String(), syntax.Field), fieldRange.Start, fieldRange.End)
			valid = false
			continue
		}
		local := syntax.LocalName()
		if fields[syntax.Field] || locals[local] {
			s.addDiagnostic(path, "duplicate-binding", fmt.Sprintf("payload field %s or local name %s is bound more than once", syntax.Field, local), fieldRange.Start, localRange.End)
			valid = false
			continue
		}
		if _, shadows := inputNamed(component, local); shadows {
			s.addDiagnostic(path, "shadowing-binding", fmt.Sprintf("binding %s shadows input %s of %s", local, local, component.Name), localRange.Start, localRange.End)
			valid = false
			continue
		}
		fields[syntax.Field], locals[local] = true, true
		if field.ID != "" {
			s.references[field.ID] = append(s.references[field.ID], Reference{Symbol: field.ID, Kind: FieldReference, Range: fieldRange})
		}
		clause.Bindings = append(clause.Bindings, Binding{
			Name: local, Field: syntax.Field, Alias: syntax.Alias, FieldID: field.ID,
			Type: field.Type, Optional: field.Optional, Range: localRange, FieldRange: fieldRange,
		})
	}
	return clause, valid
}

func isBuiltinPattern(name string) bool {
	return name == TrueCase || name == FalseCase || name == SomeCase || name == NoneCase
}

func selectorTypeName(input Input) string {
	if input.Mode == OptionalInput {
		return input.Type.String() + "?"
	}
	return input.Type.String()
}

// clauseAt returns the validated clause for a clause syntax node.
func (s *Snapshot) clauseAt(path string, syntax *ast.RenderClause) (Clause, bool) {
	id, ok := s.visible[path][syntax.Name]
	if !ok {
		return Clause{}, false
	}
	for _, clause := range s.groups[id].Clauses {
		if clause.syntax == syntax {
			return clause, true
		}
	}
	return Clause{}, false
}

// ClauseGroup returns the validated clause group of a component, if it has one.
func (s *Snapshot) ClauseGroup(id SymbolID) (ClauseGroup, bool) {
	if s == nil {
		return ClauseGroup{}, false
	}
	group, ok := s.groups[id]
	group.Clauses = append([]Clause(nil), group.Clauses...)
	return group, ok
}

// State returns a Presentation State by symbol.
func (s *Snapshot) State(id SymbolID) (State, bool) {
	if s == nil {
		return State{}, false
	}
	state, ok := s.states[id]
	state.Variants = append([]Variant(nil), state.Variants...)
	return state, ok
}

// StatesIn returns the states declared by one schema document in source order.
func (s *Snapshot) StatesIn(file string) []State {
	if s == nil {
		return nil
	}
	path, err := canonicalPath(file)
	if err != nil {
		return nil
	}
	var states []State
	for _, id := range sortedStateIDs(s.states) {
		if state, _ := s.State(id); state.File == path {
			states = append(states, state)
		}
	}
	sort.Slice(states, func(i, j int) bool { return states[i].Range.Start < states[j].Range.Start })
	return states
}

// GeneratedSchemaNames returns every exported Go name a schema document
// generates, for collision checks against handwritten Go.
func (s *Snapshot) GeneratedSchemaNames(file string) []string {
	var names []string
	for _, record := range s.RecordsIn(file) {
		names = append(names, record.Name)
	}
	for _, state := range s.StatesIn(file) {
		names = append(names, StateGoNames(state)...)
	}
	return names
}

// StateSignature renders a state declaration for hover text.
func StateSignature(state State) string {
	parts := make([]string, len(state.Variants))
	for index, variant := range state.Variants {
		parts[index] = variantSpelling(variant)
	}
	return "state " + state.Name + " { " + strings.Join(parts, ", ") + " }"
}

// VariantSignature renders a Variant with its owning state for hover text.
func VariantSignature(state State, variant Variant) string {
	return state.Name + "." + variantSpelling(variant)
}

func variantSpelling(variant Variant) string {
	if !variant.Payload {
		return variant.Name
	}
	fields := make([]string, len(variant.Fields))
	for index, field := range variant.Fields {
		fields[index] = fieldSpelling(field)
	}
	return variant.Name + " [" + strings.Join(fields, ", ") + "]"
}

// stateSymbol resolves a state, Variant, or payload field symbol.
func (s *Snapshot) stateSymbol(id SymbolID) (Symbol, bool) {
	if state, ok := s.states[id]; ok {
		return Symbol{ID: id, Kind: StateSymbol, Name: state.Name, Range: state.Range, Detail: StateSignature(state)}, true
	}
	for _, state := range s.states {
		for _, variant := range state.Variants {
			if variant.ID == id {
				return Symbol{ID: id, Kind: VariantSymbol, Name: variant.Name, Range: variant.Range, Record: state.ID, Detail: VariantSignature(state, variant)}, true
			}
			for _, field := range variant.Fields {
				if field.ID == id {
					return Symbol{ID: id, Kind: FieldSymbol, Name: field.Name, Range: field.NameRange, Record: variant.ID, Detail: state.Name + "." + variant.Name + "." + fieldSpelling(field)}, true
				}
			}
		}
	}
	return Symbol{}, false
}

// stateDeclarationAt finds a state, Variant, or payload field declaration name
// range containing offset.
func (s *Snapshot) stateDeclarationAt(within func(SourceRange) bool) (SymbolID, SourceRange, bool) {
	for _, id := range sortedStateIDs(s.states) {
		state := s.states[id]
		if within(state.Range) {
			return id, state.Range, true
		}
		for _, variant := range state.Variants {
			if within(variant.Range) {
				return variant.ID, variant.Range, true
			}
			for _, field := range variant.Fields {
				if within(field.NameRange) {
					return field.ID, field.NameRange, true
				}
			}
		}
	}
	return "", SourceRange{}, false
}

// Groups returns the clause groups of one component document.
func (s *Snapshot) Groups(file string) []ClauseGroup {
	if s == nil {
		return nil
	}
	path, err := canonicalPath(file)
	if err != nil {
		return nil
	}
	var groups []ClauseGroup
	for _, group := range s.groups {
		if group.File == path {
			groups = append(groups, group)
		}
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Component < groups[j].Component })
	return groups
}

// BindingAt returns the clause binding or repetition item declared at offset.
func (s *Snapshot) BindingAt(file string, offset int) (Binding, bool) {
	for _, repetition := range s.Repetitions(file) {
		if offset >= repetition.Item.Range.Start && offset <= repetition.Item.Range.End {
			return repetition.Item, true
		}
	}
	for _, group := range s.Groups(file) {
		for _, clause := range group.Clauses {
			for _, binding := range clause.Bindings {
				if offset >= binding.Range.Start && offset <= binding.Range.End {
					return binding, true
				}
			}
		}
	}
	return Binding{}, false
}
