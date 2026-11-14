package ast

import "encoding/json"

// FixtureDecl is a named story fixture: fixture name: Type = literal. It is
// the only way a story supplies a trusted or contextual value.
type FixtureDecl struct {
	P, E         int
	KeywordRange SourceRange    `json:"keyword_range"`
	Name         string         `json:"name"`
	NameRange    SourceRange    `json:"name_range"`
	TypeDecl     string         `json:"type"`
	TypeRange    SourceRange    `json:"type_range"`
	TypeExpr     *TypeExpr      `json:"type_expr"`
	Value        *ScalarLiteral `json:"value"`
}

func (n *FixtureDecl) Pos() int     { return n.P }
func (n *FixtureDecl) End() int     { return n.E }
func (n *FixtureDecl) Type() string { return "FixtureDecl" }

// StoryDecl is one named story of a component:
// story Component.Name [input = value, …] { content }. The argument list and
// the content body are both optional; ArgumentsRange and BodyRange record
// whether each was written.
type StoryDecl struct {
	P, E           int
	KeywordRange   SourceRange      `json:"keyword_range"`
	Component      string           `json:"component"`
	ComponentRange SourceRange      `json:"component_range"`
	Name           string           `json:"name"`
	NameRange      SourceRange      `json:"name_range"`
	Arguments      []*ValueArgument `json:"arguments,omitempty"`
	ArgumentsRange *SourceRange     `json:"arguments_range,omitempty"`
	Body           []Node           `json:"body,omitempty"`
	BodyRange      *SourceRange     `json:"body_range,omitempty"`
}

func (n *StoryDecl) Pos() int     { return n.P }
func (n *StoryDecl) End() int     { return n.E }
func (n *StoryDecl) Type() string { return "StoryDecl" }

// ValueArgument is name = value: a story input, a record field, or a Variant
// payload field.
type ValueArgument struct {
	P, E      int
	Name      string      `json:"name"`
	NameRange SourceRange `json:"name_range"`
	Value     *ValueExpr  `json:"value"`
}

type ValueKind string

const (
	// ScalarValue is a string, number, or boolean literal.
	ScalarValue ValueKind = "scalar"
	// FieldsValue is a record literal, [field = value, …].
	FieldsValue ValueKind = "fields"
	// ListValue is a collection literal, [value, …].
	ListValue ValueKind = "list"
	// EmptyValue is [], an empty record or collection by context.
	EmptyValue ValueKind = "empty"
	// NameValue is an identifier: a Variant (with an optional payload), None,
	// or a fixture reference, by context.
	NameValue ValueKind = "name"
)

// ValueExpr is a story value literal. Its meaning is typed by the input or
// field it is given for.
type ValueExpr struct {
	P      int            `json:"start"`
	E      int            `json:"end"`
	Kind   ValueKind      `json:"kind"`
	Scalar *ScalarLiteral `json:"scalar,omitempty"`
	// Name and NameRange are set for NameValue.
	Name      string       `json:"name,omitempty"`
	NameRange *SourceRange `json:"name_range,omitempty"`
	// Fields holds a record literal's fields, or a Variant's payload fields
	// when PayloadRange is set.
	Fields       []*ValueArgument `json:"fields,omitempty"`
	PayloadRange *SourceRange     `json:"payload_range,omitempty"`
	Items        []*ValueExpr     `json:"items,omitempty"`
}

// IsStoryPath reports whether path is a story Document.
func IsStoryPath(path string) bool {
	return len(path) > len(".story.ktml") && path[len(path)-len(".story.ktml"):] == ".story.ktml"
}

func (n *StoryDecl) UnmarshalJSON(data []byte) error {
	type Alias StoryDecl
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

func (n *StoryDecl) MarshalJSON() ([]byte, error) {
	type Alias StoryDecl
	body, err := marshalBody(n.Body)
	if err != nil {
		return nil, err
	}
	return json.Marshal(&struct {
		*Alias
		Body []json.RawMessage `json:"body,omitempty"`
	}{
		Alias: (*Alias)(n),
		Body:  body,
	})
}
