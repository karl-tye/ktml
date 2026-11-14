// Package symbolindex is the KTML language server's persistent workspace
// symbol index, and the protocol its providers contribute to it with.
//
// A provider is a plugin that declares the symbol-index capability (plugin
// ABI 2) or a native adapter. The host asks each provider for the documents
// it knows with an Method request, at start for the whole workspace and
// after each change for the changed documents. A provider answers with one
// Document per document it indexed; a returned Document replaces everything
// that provider contributed for its URI before. Symbol IDs are opaque strings
// shared across providers and documents: a reference in one document names a
// symbol another document, or another provider, declares.
package symbolindex

// Method is the request the host sends a provider through ktml_lsp_handle.
// Its params are a Request and its result a Result, in a JSON-RPC response.
const Method = "$/ktml/index"

// Position and Range are LSP positions: zero-based lines and UTF-16 columns.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Contains reports whether position lies in the range, ends included, the
// way a cursor right after a name still selects it.
func (r Range) Contains(position Position) bool {
	return !less(position, r.Start) && !less(r.End, position)
}

// Within reports whether r lies inside other.
func (r Range) Within(other Range) bool {
	return !less(r.Start, other.Start) && !less(other.End, r.End)
}

func less(a, b Position) bool {
	return a.Line < b.Line || a.Line == b.Line && a.Character < b.Character
}

// Kind is what a symbol declares.
type Kind string

const (
	Component Kind = "component"
	Input     Kind = "input"
	Slot      Kind = "slot"
	// Binding is a Render Clause payload binding; LoopBinding is a Keyed
	// Repetition item.
	Binding     Kind = "binding"
	LoopBinding Kind = "loop-binding"
	// Alias is an import alias: a local name for a symbol of another
	// document.
	Alias   Kind = "alias"
	Record  Kind = "record"
	Field   Kind = "field"
	State   Kind = "state"
	Variant Kind = "variant"
	Story   Kind = "story"
	Fixture Kind = "fixture"
	Route   Kind = "route"
	// AlpineBinding is a property, method, getter, or x-ref of an Alpine
	// scope.
	AlpineBinding Kind = "alpine-binding"
	// Class is a Tailwind or plain CSS class token. Classes are declared in
	// stylesheets, so the index holds their uses only.
	Class Kind = "class"
)

// ReferenceKind is how a reference uses its symbol.
type ReferenceKind string

const (
	ReadReference   ReferenceKind = "read"
	WriteReference  ReferenceKind = "write"
	CallReference   ReferenceKind = "call"
	ImportReference ReferenceKind = "import"
	TypeReference   ReferenceKind = "type"
)

// Symbol is one declaration. Range is its name.
type Symbol struct {
	ID     string `json:"id"`
	Kind   Kind   `json:"kind"`
	Name   string `json:"name"`
	Range  Range  `json:"range"`
	Detail string `json:"detail,omitempty"`
	// Container is the ID of the enclosing symbol, such as an input's
	// component.
	Container string `json:"container,omitempty"`
}

// Reference is one use of a symbol.
type Reference struct {
	Symbol string        `json:"symbol"`
	Kind   ReferenceKind `json:"kind"`
	Range  Range         `json:"range"`
	// From is the ID of the symbol the reference is made in, such as the
	// component whose body calls another: a call graph edge From -> Symbol.
	From string `json:"from,omitempty"`
	// Alias marks a reference spelled with an import alias.
	Alias bool `json:"alias,omitempty"`
}

// Document is everything one provider knows about one document.
type Document struct {
	URI        string      `json:"uri"`
	Version    int         `json:"version,omitempty"`
	Symbols    []Symbol    `json:"symbols"`
	References []Reference `json:"references"`
}

// Request asks a provider for documents: every document it knows when
// Workspace is set, and otherwise the changed URIs and any document whose
// symbols they can change, such as their importers.
type Request struct {
	Workspace bool     `json:"workspace,omitempty"`
	URIs      []string `json:"uris,omitempty"`
}

// Result is a provider's answer. Removed lists URIs whose contributions are
// dropped, such as deleted files.
type Result struct {
	Documents []Document `json:"documents"`
	Removed   []string   `json:"removed,omitempty"`
}
