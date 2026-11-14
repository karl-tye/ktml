package ast

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Node interface {
	Pos() int
	End() int
	Type() string
}

type TextNode struct {
	P, E   int
	Value  string       `json:"value"`
	Quoted *QuotedValue `json:"quoted,omitempty"`
}

func (n *TextNode) Pos() int     { return n.P }
func (n *TextNode) End() int     { return n.E }
func (n *TextNode) Type() string { return "TextNode" }

// ElementNode is an element or component call. Its header (name, attributes,
// and the opening token of its child) is on one line unless Multiline reports
// the parenthesized continuation group, whose range is AttributeGroup. E is
// the exact end of the element's last token.
type ElementNode struct {
	P, E           int
	Name           string           `json:"name"`
	Attributes     []*AttributeNode `json:"attributes"`
	Children       []Node           `json:"children"`
	Multiline      bool             `json:"multiline"`
	AttributeGroup *SourceRange     `json:"attribute_group,omitempty"`
}

func (n *ElementNode) Pos() int     { return n.P }
func (n *ElementNode) End() int     { return n.E }
func (n *ElementNode) Type() string { return "ElementNode" }

// AttributeNode is one entry of an attribute list: a named attribute, or,
// with an empty Name, a conditional attribute group (Condition) or an
// attribute spread (Spread).
type AttributeNode struct {
	P, E      int
	Name      string              `json:"name"`
	Value     string              `json:"value"`
	Quoted    *QuotedValue        `json:"quoted,omitempty"`
	Condition *AttributeCondition `json:"condition,omitempty"`
	Spread    *AttributeSpread    `json:"spread,omitempty"`
	// Bag is the attribute bag literal name=( entries ) of a component call
	// input of type Attributes; BagRange is its parenthesized range.
	Bag      []*AttributeNode `json:"bag,omitempty"`
	BagRange *SourceRange     `json:"bag_range,omitempty"`
}

// IsNamed reports whether the entry is a named attribute rather than a
// conditional group or a spread.
func (n *AttributeNode) IsNamed() bool {
	return n.Condition == nil && n.Spread == nil
}

// AttributeCondition is if condition { attributes } with an optional else
// branch, inside a parenthesized attribute list. An else if keeps its nested
// conditional entry as the only Else entry and sets ElseIf.
type AttributeCondition struct {
	KeywordRange     SourceRange      `json:"keyword_range"`
	Condition        *ConditionExpr   `json:"condition"`
	Then             []*AttributeNode `json:"then"`
	ElseKeywordRange *SourceRange     `json:"else_keyword_range,omitempty"`
	ElseIf           bool             `json:"else_if,omitempty"`
	Else             []*AttributeNode `json:"else,omitempty"`
}

// AttributeSpread is ...path: it forwards an Attributes value.
type AttributeSpread struct {
	OperatorRange SourceRange     `json:"operator_range"`
	Path          *PathExpression `json:"path"`
}

// NamedAttributes returns every named attribute of entries, in source order,
// including those inside every branch of conditional groups.
func NamedAttributes(entries []*AttributeNode) []*AttributeNode {
	var named []*AttributeNode
	var walk func([]*AttributeNode)
	walk = func(entries []*AttributeNode) {
		for _, entry := range entries {
			switch {
			case entry.Condition != nil:
				walk(entry.Condition.Then)
				walk(entry.Condition.Else)
			case entry.Spread == nil:
				named = append(named, entry)
			}
		}
	}
	walk(entries)
	return named
}

func (n *AttributeNode) Pos() int     { return n.P }
func (n *AttributeNode) End() int     { return n.E }
func (n *AttributeNode) Type() string { return "AttributeNode" }

type SegmentKind string

const (
	LiteralSegment SegmentKind = "literal"
	PathSegment    SegmentKind = "path"
)

// QuotedValue is the canonical structure of a quoted text or attribute value.
// Its ranges and all nested ranges are zero-based and half-open source bytes.
type QuotedValue struct {
	P        int            `json:"start"`
	E        int            `json:"end"`
	Segments []ValueSegment `json:"segments"`
}

type ValueSegment struct {
	P       int             `json:"start"`
	E       int             `json:"end"`
	Kind    SegmentKind     `json:"kind"`
	Literal string          `json:"literal,omitempty"`
	Path    *PathExpression `json:"path,omitempty"`
}

type PathExpression struct {
	P     int        `json:"start"`
	E     int        `json:"end"`
	Parts []PathPart `json:"parts"`
}

func (p *PathExpression) String() string {
	if p == nil {
		return ""
	}
	parts := make([]string, len(p.Parts))
	for i, part := range p.Parts {
		parts[i] = part.Name
	}
	return strings.Join(parts, ".")
}

type PathPart struct {
	P    int    `json:"start"`
	E    int    `json:"end"`
	Name string `json:"name"`
}

type SourceRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type InputMode string

const (
	RequiredInput  InputMode = "required"
	OptionalInput  InputMode = "optional"
	DefaultedInput InputMode = "defaulted"
)

type DeclarationKind string

const (
	InputDeclaration    DeclarationKind = "input"
	OverrideDeclaration DeclarationKind = "override"
)

type ScalarLiteralKind string

const (
	StringLiteral  ScalarLiteralKind = "string"
	BooleanLiteral ScalarLiteralKind = "boolean"
	NumberLiteral  ScalarLiteralKind = "number"
)

// ScalarLiteral retains the source spelling and typed value. The containing
// pointer records presence, so false, zero, and the empty string stay distinct
// from an omitted default.
type ScalarLiteral struct {
	P       int               `json:"start"`
	E       int               `json:"end"`
	Kind    ScalarLiteralKind `json:"kind"`
	Raw     string            `json:"raw"`
	String  string            `json:"string,omitempty"`
	Boolean bool              `json:"boolean,omitempty"`
	Number  float64           `json:"number,omitempty"`
}

type TypeExprKind string

const (
	NamedTypeExpr      TypeExprKind = "named"
	CollectionTypeExpr TypeExprKind = "collection"
)

// TypeExpr is a structured input or field type. Named types are resolved by the
// semantic model; collections wrap one element type.
type TypeExpr struct {
	P         int          `json:"start"`
	E         int          `json:"end"`
	Kind      TypeExprKind `json:"kind"`
	Name      string       `json:"name,omitempty"`
	NameRange *SourceRange `json:"name_range,omitempty"`
	Element   *TypeExpr    `json:"element,omitempty"`
}

// String returns the canonical type spelling.
func (t *TypeExpr) String() string {
	if t == nil {
		return ""
	}
	if t.Kind == CollectionTypeExpr {
		return "[" + t.Element.String() + "]"
	}
	return t.Name
}

// RecordDecl is a Host Schema record. E is the end of its closing bracket.
type RecordDecl struct {
	P, E         int
	KeywordRange SourceRange  `json:"keyword_range"`
	Name         string       `json:"name"`
	NameRange    SourceRange  `json:"name_range"`
	Fields       []*FieldDecl `json:"fields"`
}

func (n *RecordDecl) Pos() int     { return n.P }
func (n *RecordDecl) End() int     { return n.E }
func (n *RecordDecl) Type() string { return "RecordDecl" }

type FieldDecl struct {
	P, E           int
	Name           string       `json:"name"`
	NameRange      SourceRange  `json:"name_range"`
	Optional       bool         `json:"optional,omitempty"`
	OptionalMarker *SourceRange `json:"optional_marker,omitempty"`
	TypeDecl       string       `json:"type"`
	TypeRange      SourceRange  `json:"type_range"`
	TypeExpr       *TypeExpr    `json:"type_expr"`
}

func (n *FieldDecl) Pos() int     { return n.P }
func (n *FieldDecl) End() int     { return n.E }
func (n *FieldDecl) Type() string { return "FieldDecl" }

// StateDecl is a closed Host Schema Presentation State. E is the end of its
// closing brace.
type StateDecl struct {
	P, E         int
	KeywordRange SourceRange    `json:"keyword_range"`
	Name         string         `json:"name"`
	NameRange    SourceRange    `json:"name_range"`
	Variants     []*VariantDecl `json:"variants"`
}

func (n *StateDecl) Pos() int     { return n.P }
func (n *StateDecl) End() int     { return n.E }
func (n *StateDecl) Type() string { return "StateDecl" }

// VariantDecl is one case of a Presentation State. Payload reports whether a
// bracketed field list is present, even when it is empty.
type VariantDecl struct {
	P, E         int
	Name         string       `json:"name"`
	NameRange    SourceRange  `json:"name_range"`
	Payload      bool         `json:"payload,omitempty"`
	PayloadRange *SourceRange `json:"payload_range,omitempty"`
	Fields       []*FieldDecl `json:"fields,omitempty"`
}

func (n *VariantDecl) Pos() int     { return n.P }
func (n *VariantDecl) End() int     { return n.E }
func (n *VariantDecl) Type() string { return "VariantDecl" }

// Pattern is a Render Clause selector Pattern: a Variant, optionally qualified
// by its state, or one of true, false, Some, and None.
type Pattern struct {
	P              int          `json:"start"`
	E              int          `json:"end"`
	Qualifier      string       `json:"qualifier,omitempty"`
	QualifierRange *SourceRange `json:"qualifier_range,omitempty"`
	Name           string       `json:"name"`
	NameRange      SourceRange  `json:"name_range"`
}

// String returns the source spelling of the Pattern.
func (p Pattern) String() string {
	if p.Qualifier != "" {
		return p.Qualifier + "." + p.Name
	}
	return p.Name
}

// PayloadBinding binds one payload field, optionally under a local alias.
type PayloadBinding struct {
	P          int          `json:"start"`
	E          int          `json:"end"`
	Field      string       `json:"field"`
	FieldRange SourceRange  `json:"field_range"`
	Alias      string       `json:"alias,omitempty"`
	AliasRange *SourceRange `json:"alias_range,omitempty"`
}

// LocalName returns the name the binding introduces into the clause body.
func (b PayloadBinding) LocalName() string {
	if b.Alias != "" {
		return b.Alias
	}
	return b.Field
}

// RenderClause is one body of a component selected by matching Pattern
// against the signature input named Selector.
type RenderClause struct {
	P, E          int
	Name          string           `json:"name"`
	NameRange     SourceRange      `json:"name_range"`
	Selector      string           `json:"selector"`
	SelectorRange SourceRange      `json:"selector_range"`
	Pattern       Pattern          `json:"pattern"`
	Bindings      []PayloadBinding `json:"bindings,omitempty"`
	Body          []Node           `json:"body"`
}

func (n *RenderClause) Pos() int     { return n.P }
func (n *RenderClause) End() int     { return n.E }
func (n *RenderClause) Type() string { return "RenderClause" }

// RepetitionNode is a Keyed Repetition body node: for item in collection key
// key { body }. E is the end of its closing brace.
type RepetitionNode struct {
	P, E            int
	KeywordRange    SourceRange     `json:"keyword_range"`
	Item            string          `json:"item"`
	ItemRange       SourceRange     `json:"item_range"`
	InRange         SourceRange     `json:"in_range"`
	Collection      *PathExpression `json:"collection"`
	KeyKeywordRange SourceRange     `json:"key_keyword_range"`
	Key             *PathExpression `json:"key"`
	Body            []Node          `json:"body"`
}

func (n *RepetitionNode) Pos() int     { return n.P }
func (n *RepetitionNode) End() int     { return n.E }
func (n *RepetitionNode) Type() string { return "RepetitionNode" }

// SlotNode fills a named content slot of the enclosing component call:
// slot name { body }. E is the end of its closing brace.
type SlotNode struct {
	P, E         int
	KeywordRange SourceRange `json:"keyword_range"`
	Name         string      `json:"name"`
	NameRange    SourceRange `json:"name_range"`
	Body         []Node      `json:"body"`
}

func (n *SlotNode) Pos() int     { return n.P }
func (n *SlotNode) End() int     { return n.E }
func (n *SlotNode) Type() string { return "SlotNode" }

// DoctypeNode is doctype name, rendered as <!doctype html>. E is the end of
// its name.
type DoctypeNode struct {
	P, E         int
	Name         string      `json:"name"`
	KeywordRange SourceRange `json:"keyword_range"`
	NameRange    SourceRange `json:"name_range"`
}

func (n *DoctypeNode) Pos() int     { return n.P }
func (n *DoctypeNode) End() int     { return n.E }
func (n *DoctypeNode) Type() string { return "DoctypeNode" }

// HTMLCommentNode is an output comment, <!--text-->. Text is the static
// source between the delimiters; E is the end of -->.
type HTMLCommentNode struct {
	P, E int
	Text string `json:"text"`
}

func (n *HTMLCommentNode) Pos() int     { return n.P }
func (n *HTMLCommentNode) End() int     { return n.E }
func (n *HTMLCommentNode) Type() string { return "HTMLCommentNode" }

// RawTextNode is a raw-text literal, ``` text ```: the static content of a
// script or style element, written byte for byte. Text is the source between
// the fences; E is the end of the closing fence.
type RawTextNode struct {
	P, E int
	Text string `json:"text"`
}

func (n *RawTextNode) Pos() int     { return n.P }
func (n *RawTextNode) End() int     { return n.E }
func (n *RawTextNode) Type() string { return "RawTextNode" }

type ConditionKind string

const (
	PathCondition     ConditionKind = "path"
	PresenceCondition ConditionKind = "presence"
	NotCondition      ConditionKind = "not"
	GroupCondition    ConditionKind = "group"
	AndCondition      ConditionKind = "and"
	OrCondition       ConditionKind = "or"
)

// ConditionExpr is one node of the closed boolean/presence condition grammar.
// Path is set for path and presence operands; OperatorRange is the ! or ?;
// Operands holds the operand of not and group, and the two or more operands of
// and and or, whose operators are OperatorRanges.
type ConditionExpr struct {
	P              int              `json:"start"`
	E              int              `json:"end"`
	Kind           ConditionKind    `json:"kind"`
	Path           *PathExpression  `json:"path,omitempty"`
	OperatorRange  *SourceRange     `json:"operator_range,omitempty"`
	Operands       []*ConditionExpr `json:"operands,omitempty"`
	OperatorRanges []SourceRange    `json:"operator_ranges,omitempty"`
}

// ConditionNode is if condition { then } with an optional else branch. An
// else if keeps its nested ConditionNode as the only Else node and sets
// ElseIf. E is the end of the last closing brace.
type ConditionNode struct {
	P, E             int
	KeywordRange     SourceRange    `json:"keyword_range"`
	Condition        *ConditionExpr `json:"condition"`
	Then             []Node         `json:"then"`
	ElseKeywordRange *SourceRange   `json:"else_keyword_range,omitempty"`
	ElseIf           bool           `json:"else_if,omitempty"`
	Else             []Node         `json:"else,omitempty"`
}

func (n *ConditionNode) Pos() int     { return n.P }
func (n *ConditionNode) End() int     { return n.E }
func (n *ConditionNode) Type() string { return "ConditionNode" }

// ElementDecl is a component declaration. A Component Signature for Render
// Clauses is Bodyless, or has a DefaultRange and a residual Body.
type ElementDecl struct {
	P, E         int
	Name         string           `json:"name"`
	Attributes   []*AttributeDecl `json:"attributes"`
	Body         []Node           `json:"body"`
	Bodyless     bool             `json:"bodyless,omitempty"`
	DefaultRange *SourceRange     `json:"default_range,omitempty"`
}

// IsSignature reports whether the declaration is a Component Signature that
// Render Clauses may follow.
func (n *ElementDecl) IsSignature() bool {
	return n.Bodyless || n.DefaultRange != nil
}

func (n *ElementDecl) Pos() int     { return n.P }
func (n *ElementDecl) End() int     { return n.E }
func (n *ElementDecl) Type() string { return "ElementDecl" }

type AttributeDecl struct {
	P, E            int
	Kind            DeclarationKind `json:"kind"`
	Name            string          `json:"name"`
	NameRange       SourceRange     `json:"name_range"`
	TypeDecl        string          `json:"type,omitempty"`
	TypeRange       SourceRange     `json:"type_range,omitempty"`
	TypeExpr        *TypeExpr       `json:"type_expr,omitempty"`
	Mode            InputMode       `json:"mode,omitempty"`
	OptionalMarker  *SourceRange    `json:"optional_marker,omitempty"`
	DefaultOperator *SourceRange    `json:"default_operator,omitempty"`
	Default         *ScalarLiteral  `json:"default,omitempty"`
	Override        *ScalarLiteral  `json:"override,omitempty"`

	// Transitional raw spellings are retained for prototype JSON consumers.
	// Canonical semantics must use Kind, Mode, Default, and Override.
	DefaultValue string `json:"default_value,omitempty"`
	Value        string `json:"value,omitempty"`
}

func (n *AttributeDecl) Pos() int     { return n.P }
func (n *AttributeDecl) End() int     { return n.E }
func (n *AttributeDecl) Type() string { return "AttributeDecl" }

// ImportSpecifier is one imported source name and its optional local alias.
type ImportSpecifier struct {
	Name       string       `json:"name"`
	NameRange  SourceRange  `json:"name_range"`
	Alias      string       `json:"alias,omitempty"`
	AliasRange *SourceRange `json:"alias_range,omitempty"`
}

// LocalName returns the name the specifier introduces into scope.
func (s ImportSpecifier) LocalName() string {
	if s.Alias != "" {
		return s.Alias
	}
	return s.Name
}

type ImportStmt struct {
	P, E       int
	Items      []string          `json:"items"`
	Path       string            `json:"path"`
	PathRange  SourceRange       `json:"path_range"`
	Specifiers []ImportSpecifier `json:"specifiers,omitempty"`
}

func (n *ImportStmt) Pos() int     { return n.P }
func (n *ImportStmt) End() int     { return n.E }
func (n *ImportStmt) Type() string { return "ImportStmt" }

type Document struct {
	P, E  int
	Nodes []Node `json:"nodes"`
}

func (n *Document) Pos() int     { return n.P }
func (n *Document) End() int     { return n.E }
func (n *Document) Type() string { return "Document" }

func (d *Document) UnmarshalJSON(data []byte) error {
	var raw struct {
		P     int               `json:"p"`
		E     int               `json:"e"`
		Nodes []json.RawMessage `json:"nodes"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	d.P = raw.P
	d.E = raw.E

	var err error
	if raw.Nodes != nil {
		d.Nodes = make([]Node, len(raw.Nodes))
		for i, nRaw := range raw.Nodes {
			var wrapper struct {
				NodeType string `json:"_type"`
			}
			if err := json.Unmarshal(nRaw, &wrapper); err != nil {
				return err
			}
			var n Node
			switch wrapper.NodeType {
			case "TextNode":
				n = &TextNode{}
			case "ElementNode":
				n = &ElementNode{}
			case "ElementDecl":
				n = &ElementDecl{}
			case "ImportStmt":
				n = &ImportStmt{}
			case "RecordDecl":
				n = &RecordDecl{}
			case "StateDecl":
				n = &StateDecl{}
			case "RenderClause":
				n = &RenderClause{}
			case "FixtureDecl":
				n = &FixtureDecl{}
			case "StoryDecl":
				n = &StoryDecl{}
			default:
				return fmt.Errorf("unknown node type: %s", wrapper.NodeType)
			}
			if err := json.Unmarshal(nRaw, n); err != nil {
				return err
			}
			d.Nodes[i] = n
		}
	}
	return err
}

func (e *ElementNode) UnmarshalJSON(data []byte) error {
	type Alias ElementNode
	aux := &struct {
		Children []json.RawMessage `json:"children"`
		*Alias
	}{
		Alias: (*Alias)(e),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	nodes, err := unmarshalBody(aux.Children)
	if err != nil {
		return err
	}
	e.Children = nodes
	return nil
}

func (e *ElementDecl) UnmarshalJSON(data []byte) error {
	type Alias ElementDecl
	aux := &struct {
		Body []json.RawMessage `json:"body"`
		*Alias
	}{
		Alias: (*Alias)(e),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	nodes, err := unmarshalBody(aux.Body)
	if err != nil {
		return err
	}
	e.Body = nodes
	return nil
}

func MarshalNode(n Node) ([]byte, error) {
	b, err := json.Marshal(n)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	m["_type"] = n.Type()
	return json.Marshal(m)
}

func (d *Document) MarshalJSON() ([]byte, error) {
	type Alias Document
	var rawNodes []json.RawMessage
	for _, n := range d.Nodes {
		b, err := MarshalNode(n)
		if err != nil {
			return nil, err
		}
		rawNodes = append(rawNodes, b)
	}
	return json.Marshal(&struct {
		*Alias
		Nodes []json.RawMessage `json:"nodes"`
	}{
		Alias: (*Alias)(d),
		Nodes: rawNodes,
	})
}

func (e *ElementNode) MarshalJSON() ([]byte, error) {
	type Alias ElementNode
	var rawChildren []json.RawMessage
	for _, c := range e.Children {
		b, err := MarshalNode(c)
		if err != nil {
			return nil, err
		}
		rawChildren = append(rawChildren, b)
	}
	return json.Marshal(&struct {
		*Alias
		Children []json.RawMessage `json:"children"`
	}{
		Alias:    (*Alias)(e),
		Children: rawChildren,
	})
}

func (e *ElementDecl) MarshalJSON() ([]byte, error) {
	type Alias ElementDecl
	var rawBody []json.RawMessage
	for _, c := range e.Body {
		b, err := MarshalNode(c)
		if err != nil {
			return nil, err
		}
		rawBody = append(rawBody, b)
	}
	return json.Marshal(&struct {
		*Alias
		Body []json.RawMessage `json:"body"`
	}{
		Alias: (*Alias)(e),
		Body:  rawBody,
	})
}

func (c *RenderClause) UnmarshalJSON(data []byte) error {
	type Alias RenderClause
	aux := &struct {
		Body []json.RawMessage `json:"body"`
		*Alias
	}{
		Alias: (*Alias)(c),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	nodes, err := unmarshalBody(aux.Body)
	if err != nil {
		return err
	}
	c.Body = nodes
	return nil
}

func (c *RenderClause) MarshalJSON() ([]byte, error) {
	type Alias RenderClause
	var rawBody []json.RawMessage
	for _, child := range c.Body {
		b, err := MarshalNode(child)
		if err != nil {
			return nil, err
		}
		rawBody = append(rawBody, b)
	}
	return json.Marshal(&struct {
		*Alias
		Body []json.RawMessage `json:"body"`
	}{
		Alias: (*Alias)(c),
		Body:  rawBody,
	})
}

// unmarshalBody decodes body nodes tagged with _type. A nil slice stays nil.
func unmarshalBody(raw []json.RawMessage) ([]Node, error) {
	if raw == nil {
		return nil, nil
	}
	nodes := make([]Node, len(raw))
	for i, cRaw := range raw {
		var wrapper struct {
			NodeType string `json:"_type"`
		}
		if err := json.Unmarshal(cRaw, &wrapper); err != nil {
			return nil, err
		}
		var n Node
		switch wrapper.NodeType {
		case "TextNode":
			n = &TextNode{}
		case "ElementNode":
			n = &ElementNode{}
		case "RepetitionNode":
			n = &RepetitionNode{}
		case "SlotNode":
			n = &SlotNode{}
		case "ConditionNode":
			n = &ConditionNode{}
		case "DoctypeNode":
			n = &DoctypeNode{}
		case "HTMLCommentNode":
			n = &HTMLCommentNode{}
		case "RawTextNode":
			n = &RawTextNode{}
		default:
			return nil, fmt.Errorf("unknown node type: %s", wrapper.NodeType)
		}
		if err := json.Unmarshal(cRaw, n); err != nil {
			return nil, err
		}
		nodes[i] = n
	}
	return nodes, nil
}

func marshalBody(body []Node) ([]json.RawMessage, error) {
	var raw []json.RawMessage
	for _, child := range body {
		b, err := MarshalNode(child)
		if err != nil {
			return nil, err
		}
		raw = append(raw, b)
	}
	return raw, nil
}

func (r *RepetitionNode) UnmarshalJSON(data []byte) error {
	type Alias RepetitionNode
	aux := &struct {
		Body []json.RawMessage `json:"body"`
		*Alias
	}{
		Alias: (*Alias)(r),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	nodes, err := unmarshalBody(aux.Body)
	if err != nil {
		return err
	}
	r.Body = nodes
	return nil
}

func (r *RepetitionNode) MarshalJSON() ([]byte, error) {
	type Alias RepetitionNode
	rawBody, err := marshalBody(r.Body)
	if err != nil {
		return nil, err
	}
	return json.Marshal(&struct {
		*Alias
		Body []json.RawMessage `json:"body"`
	}{
		Alias: (*Alias)(r),
		Body:  rawBody,
	})
}

func (n *SlotNode) UnmarshalJSON(data []byte) error {
	type Alias SlotNode
	aux := &struct {
		Body []json.RawMessage `json:"body"`
		*Alias
	}{
		Alias: (*Alias)(n),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	nodes, err := unmarshalBody(aux.Body)
	if err != nil {
		return err
	}
	n.Body = nodes
	return nil
}

func (n *SlotNode) MarshalJSON() ([]byte, error) {
	type Alias SlotNode
	rawBody, err := marshalBody(n.Body)
	if err != nil {
		return nil, err
	}
	return json.Marshal(&struct {
		*Alias
		Body []json.RawMessage `json:"body"`
	}{
		Alias: (*Alias)(n),
		Body:  rawBody,
	})
}

func (n *ConditionNode) UnmarshalJSON(data []byte) error {
	type Alias ConditionNode
	aux := &struct {
		Then []json.RawMessage `json:"then"`
		Else []json.RawMessage `json:"else,omitempty"`
		*Alias
	}{
		Alias: (*Alias)(n),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	then, err := unmarshalBody(aux.Then)
	if err != nil {
		return err
	}
	otherwise, err := unmarshalBody(aux.Else)
	if err != nil {
		return err
	}
	n.Then, n.Else = then, otherwise
	return nil
}

func (n *ConditionNode) MarshalJSON() ([]byte, error) {
	type Alias ConditionNode
	rawThen, err := marshalBody(n.Then)
	if err != nil {
		return nil, err
	}
	rawElse, err := marshalBody(n.Else)
	if err != nil {
		return nil, err
	}
	return json.Marshal(&struct {
		*Alias
		Then []json.RawMessage `json:"then"`
		Else []json.RawMessage `json:"else,omitempty"`
	}{
		Alias: (*Alias)(n),
		Then:  rawThen,
		Else:  rawElse,
	})
}

// String returns the canonical source spelling of a condition.
func (c *ConditionExpr) String() string {
	if c == nil {
		return ""
	}
	switch c.Kind {
	case PathCondition:
		return c.Path.String()
	case PresenceCondition:
		return c.Path.String() + "?"
	case NotCondition:
		return "!" + c.Operands[0].String()
	case GroupCondition:
		return "(" + c.Operands[0].String() + ")"
	case AndCondition, OrCondition:
		operator := " && "
		if c.Kind == OrCondition {
			operator = " || "
		}
		parts := make([]string, len(c.Operands))
		for i, operand := range c.Operands {
			parts[i] = operand.String()
		}
		return strings.Join(parts, operator)
	}
	return ""
}
