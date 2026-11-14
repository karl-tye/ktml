package symbolindex

import (
	"sort"
	"sync"
)

// Location is a range in a document.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// Occurrence is a symbol's declaration or a reference to it at a position.
type Occurrence struct {
	Symbol      string
	Location    Location
	Declaration bool
	Kind        ReferenceKind
}

// Declaration is a symbol declared in a document.
type Declaration struct {
	Symbol
	URI string
}

// Use is a reference in a document.
type Use struct {
	Reference
	URI string
}

// Stats counts what the index holds.
type Stats struct {
	Documents  int
	Symbols    int
	References int
}

type docKey struct {
	provider string
	uri      string
}

// entry is everything the index holds about one symbol ID, by contributing
// document.
type entry struct {
	declarations map[docKey][]Symbol
	references   map[docKey][]Reference
	// outgoing are the references made from inside this symbol.
	outgoing map[docKey][]Reference
}

func (e *entry) empty() bool {
	return len(e.declarations) == 0 && len(e.references) == 0 && len(e.outgoing) == 0
}

// Index holds every provider's documents. It is safe for concurrent use.
type Index struct {
	mu      sync.RWMutex
	docs    map[docKey]Document
	byURI   map[string]map[string]bool
	symbols map[string]*entry
}

func New() *Index {
	return &Index{
		docs:    make(map[docKey]Document),
		byURI:   make(map[string]map[string]bool),
		symbols: make(map[string]*entry),
	}
}

// Apply replaces a provider's documents with result's and drops its removed
// URIs.
func (x *Index) Apply(provider string, result Result) {
	x.mu.Lock()
	defer x.mu.Unlock()
	for _, uri := range result.Removed {
		x.remove(docKey{provider, uri})
	}
	for _, document := range result.Documents {
		key := docKey{provider, document.URI}
		x.remove(key)
		x.add(key, document)
	}
}

// RemoveProvider drops everything a provider contributed.
func (x *Index) RemoveProvider(provider string) {
	x.mu.Lock()
	defer x.mu.Unlock()
	for key := range x.docs {
		if key.provider == provider {
			x.remove(key)
		}
	}
}

func (x *Index) symbol(id string) *entry {
	current := x.symbols[id]
	if current == nil {
		current = &entry{
			declarations: make(map[docKey][]Symbol),
			references:   make(map[docKey][]Reference),
			outgoing:     make(map[docKey][]Reference),
		}
		x.symbols[id] = current
	}
	return current
}

func (x *Index) add(key docKey, document Document) {
	x.docs[key] = document
	if x.byURI[key.uri] == nil {
		x.byURI[key.uri] = make(map[string]bool)
	}
	x.byURI[key.uri][key.provider] = true
	for _, symbol := range document.Symbols {
		current := x.symbol(symbol.ID)
		current.declarations[key] = append(current.declarations[key], symbol)
	}
	for _, reference := range document.References {
		current := x.symbol(reference.Symbol)
		current.references[key] = append(current.references[key], reference)
		if reference.From != "" {
			from := x.symbol(reference.From)
			from.outgoing[key] = append(from.outgoing[key], reference)
		}
	}
}

func (x *Index) remove(key docKey) {
	document, ok := x.docs[key]
	if !ok {
		return
	}
	delete(x.docs, key)
	delete(x.byURI[key.uri], key.provider)
	if len(x.byURI[key.uri]) == 0 {
		delete(x.byURI, key.uri)
	}
	drop := func(id string) {
		current := x.symbols[id]
		if current == nil {
			return
		}
		delete(current.declarations, key)
		delete(current.references, key)
		delete(current.outgoing, key)
		if current.empty() {
			delete(x.symbols, id)
		}
	}
	for _, symbol := range document.Symbols {
		drop(symbol.ID)
	}
	for _, reference := range document.References {
		drop(reference.Symbol)
		if reference.From != "" {
			drop(reference.From)
		}
	}
}

// At returns the innermost declaration or reference at position in uri.
func (x *Index) At(uri string, position Position) (Occurrence, bool) {
	x.mu.RLock()
	defer x.mu.RUnlock()
	var found Occurrence
	ok := false
	better := func(candidate Range) bool {
		if !candidate.Contains(position) {
			return false
		}
		// The innermost range wins; on a tie the first provider in name
		// order keeps it, so answers are deterministic.
		return !ok || candidate.Within(found.Location.Range) && candidate != found.Location.Range
	}
	for _, provider := range sortedProviders(x.byURI[uri]) {
		document := x.docs[docKey{provider, uri}]
		for _, symbol := range document.Symbols {
			if better(symbol.Range) {
				found, ok = Occurrence{Symbol: symbol.ID, Location: Location{uri, symbol.Range}, Declaration: true}, true
			}
		}
		for _, reference := range document.References {
			if better(reference.Range) {
				found, ok = Occurrence{Symbol: reference.Symbol, Location: Location{uri, reference.Range}, Kind: reference.Kind}, true
			}
		}
	}
	return found, ok
}

// Declarations returns where a symbol is declared, in URI and position
// order.
func (x *Index) Declarations(id string) []Declaration {
	x.mu.RLock()
	defer x.mu.RUnlock()
	current := x.symbols[id]
	if current == nil {
		return nil
	}
	var declarations []Declaration
	for key, symbols := range current.declarations {
		for _, symbol := range symbols {
			declarations = append(declarations, Declaration{Symbol: symbol, URI: key.uri})
		}
	}
	sort.Slice(declarations, func(i, j int) bool {
		return locationLess(Location{declarations[i].URI, declarations[i].Range}, Location{declarations[j].URI, declarations[j].Range})
	})
	return uniqueDeclarations(declarations)
}

// References returns a symbol's uses, in URI and position order.
func (x *Index) References(id string) []Use {
	x.mu.RLock()
	defer x.mu.RUnlock()
	current := x.symbols[id]
	if current == nil {
		return nil
	}
	return sortedUses(current.references)
}

// Outgoing returns the references made from inside a symbol: its call graph
// edges when it is a component.
func (x *Index) Outgoing(id string) []Use {
	x.mu.RLock()
	defer x.mu.RUnlock()
	current := x.symbols[id]
	if current == nil {
		return nil
	}
	return sortedUses(current.outgoing)
}

// Symbols returns every declaration whose name passes match, in name and
// location order.
func (x *Index) Symbols(match func(Symbol) bool) []Declaration {
	x.mu.RLock()
	defer x.mu.RUnlock()
	var declarations []Declaration
	for key, document := range x.docs {
		for _, symbol := range document.Symbols {
			if match(symbol) {
				declarations = append(declarations, Declaration{Symbol: symbol, URI: key.uri})
			}
		}
	}
	sort.Slice(declarations, func(i, j int) bool {
		if declarations[i].Name != declarations[j].Name {
			return declarations[i].Name < declarations[j].Name
		}
		return locationLess(Location{declarations[i].URI, declarations[i].Range}, Location{declarations[j].URI, declarations[j].Range})
	})
	return uniqueDeclarations(declarations)
}

// Stats counts the documents, declarations, and references held.
func (x *Index) Stats() Stats {
	x.mu.RLock()
	defer x.mu.RUnlock()
	stats := Stats{Documents: len(x.docs)}
	for _, document := range x.docs {
		stats.Symbols += len(document.Symbols)
		stats.References += len(document.References)
	}
	return stats
}

func sortedUses(byDocument map[docKey][]Reference) []Use {
	var uses []Use
	for key, references := range byDocument {
		for _, reference := range references {
			uses = append(uses, Use{Reference: reference, URI: key.uri})
		}
	}
	sort.Slice(uses, func(i, j int) bool {
		left, right := Location{uses[i].URI, uses[i].Range}, Location{uses[j].URI, uses[j].Range}
		if left != right {
			return locationLess(left, right)
		}
		if uses[i].Symbol != uses[j].Symbol {
			return uses[i].Symbol < uses[j].Symbol
		}
		return uses[i].From < uses[j].From
	})
	// Two providers may report the same use.
	unique := uses[:0]
	for i, use := range uses {
		if i > 0 && use.URI == uses[i-1].URI && use.Range == uses[i-1].Range && use.Symbol == uses[i-1].Symbol && use.From == uses[i-1].From {
			continue
		}
		unique = append(unique, use)
	}
	return unique
}

func uniqueDeclarations(declarations []Declaration) []Declaration {
	unique := declarations[:0]
	for i, declaration := range declarations {
		if i > 0 && declaration.URI == declarations[i-1].URI && declaration.Range == declarations[i-1].Range && declaration.ID == declarations[i-1].ID {
			continue
		}
		unique = append(unique, declaration)
	}
	return unique
}

func locationLess(a, b Location) bool {
	if a.URI != b.URI {
		return a.URI < b.URI
	}
	if a.Range.Start != b.Range.Start {
		return less(a.Range.Start, b.Range.Start)
	}
	return less(a.Range.End, b.Range.End)
}

func sortedProviders(providers map[string]bool) []string {
	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
