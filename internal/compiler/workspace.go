package compiler

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/karl-tye/ktml/internal/ast"
)

type SymbolID string

type ValueType string

const (
	StringType  ValueType = "string"
	BooleanType ValueType = "boolean"
	NumberType  ValueType = "number"
)

type InputMode string

const (
	RequiredInput  InputMode = "required"
	OptionalInput  InputMode = "optional"
	DefaultedInput InputMode = "defaulted"
)

type ScalarLiteralKind string

const (
	StringLiteral  ScalarLiteralKind = "string"
	BooleanLiteral ScalarLiteralKind = "boolean"
	NumberLiteral  ScalarLiteralKind = "number"
)

type ScalarValue struct {
	Kind    ScalarLiteralKind
	Raw     string
	String  string
	Boolean bool
	Number  float64
	Integer int64
	Range   SourceRange
}

type SourceRange struct {
	File  string
	Start int
	End   int
}

type Input struct {
	Name                 string
	Type                 Type
	Mode                 InputMode
	Default              *ScalarValue
	Range                SourceRange
	NameRange            SourceRange
	TypeRange            SourceRange
	OptionalMarkerRange  *SourceRange
	DefaultOperatorRange *SourceRange
	// Implicit marks the optional children input an ordinary component
	// declares by rendering a children outlet. Its ranges are the first
	// outlet's.
	Implicit bool

	typeExpr *ast.TypeExpr
}

// IsContent reports whether the input is children or a named slot.
func (input Input) IsContent() bool {
	return input.Type.Kind == ContentTypeKind
}

type Component struct {
	ID              SymbolID
	Name            string
	File            string
	Range           SourceRange
	Inputs          []Input
	AcceptsChildren bool
}

type ReferenceKind string

const (
	ImportReference ReferenceKind = "import"
	CallReference   ReferenceKind = "call"
)

type Reference struct {
	Symbol SymbolID
	Kind   ReferenceKind
	Range  SourceRange
	// Alias marks a reference spelled with an import alias rather than the
	// symbol's declared name; symbol rename leaves it unchanged.
	Alias bool
}

type workspaceFile struct {
	path   string
	source []byte
	doc    *ast.Document
}

type Snapshot struct {
	root         string
	files        map[string]workspaceFile
	components   map[SymbolID]Component
	visible      map[string]map[string]SymbolID
	references   map[SymbolID][]Reference
	dependencies map[string][]string
	diagnostics  map[string][]Diagnostic
	records      map[SymbolID]Record
	states       map[SymbolID]State
	groups       map[SymbolID]ClauseGroup
	types        map[string]map[string]SymbolID
	paths        map[string][]ResolvedPath

	repetitions      map[SymbolID]Repetition
	repetitionSyntax map[*ast.RepetitionNode]SymbolID
	contentUses      map[string][]ContentUse
	// callSources and outletSources hold the c<n> and o<n> Render Identity
	// sources of component calls and content outlets.
	callSources   map[*ast.ElementNode]string
	outletSources map[*ast.ElementNode]string
	// scriptPositions holds, while a script value is resolved, where each
	// of its paths falls in the static JavaScript around it.
	scriptPositions map[*ast.PathExpression]ScriptPosition
	// trustedScripts collects, while a script body is resolved, its
	// TrustedScript paths.
	trustedScripts map[*ast.PathExpression]bool

	stories  map[SymbolID]Story
	fixtures map[SymbolID]Fixture

	// generation is the workspace generation the snapshot analyzed;
	// stamps and missing are what it read from disk, so Load can tell
	// whether the disk changed since.
	generation uint64
	stamps     map[string]fileStamp
	missing    []string
}

type fileStamp struct {
	size    int64
	modTime time.Time
}

// unchangedOnDisk reports whether every file the snapshot read from disk is
// as it was, and every import it could not find is still missing.
func (s *Snapshot) unchangedOnDisk() bool {
	for path, stamp := range s.stamps {
		info, err := os.Stat(path)
		if err != nil || info.Size() != stamp.size || !info.ModTime().Equal(stamp.modTime) {
			return false
		}
	}
	for _, path := range s.missing {
		if _, err := os.Stat(path); err == nil {
			return false
		}
	}
	return true
}

type Workspace struct {
	mu      sync.RWMutex
	root    string
	entries []string
	indexed bool
	// deferStories leaves story Documents out of discovery until they are
	// opened or LoadStories is called.
	deferStories bool
	overlays     map[string]overlay
	parsed       map[string]parsedFile
	parseRuns    map[string]int
	latest       *Snapshot
	lastValid    *Snapshot
	// generation counts editor overlay and document set changes; a
	// snapshot of the current generation whose disk reads are unchanged is
	// still current.
	generation uint64
}

type overlay struct {
	version int
	source  []byte
}

type parsedFile struct {
	source string
	result Result
}

func NewWorkspace(root string) *Workspace {
	absolute, err := filepath.Abs(root)
	if err != nil {
		absolute = filepath.Clean(root)
	}
	return &Workspace{
		root:      absolute,
		overlays:  make(map[string]overlay),
		parsed:    make(map[string]parsedFile),
		parseRuns: make(map[string]int),
	}
}

// Load analyzes entry and its imports. When the latest snapshot already
// holds entry, and neither the overlays nor the files it read changed since,
// it is returned as it is.
func (w *Workspace) Load(entry string) (*Snapshot, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	entry, err := canonicalPath(entry)
	if err != nil {
		return nil, err
	}
	if latest := w.latest; latest != nil && latest.generation == w.generation {
		if _, loaded := latest.files[entry]; loaded && latest.unchangedOnDisk() {
			if w.indexed {
				w.entries = appendUnique(w.entries, entry)
			}
			return latest, nil
		}
	}
	if w.indexed {
		w.entries = appendUnique(w.entries, entry)
	} else {
		w.entries = []string{entry}
	}
	snapshot, err := w.analyzeEntries(w.entries)
	if err != nil {
		return nil, err
	}
	w.latest = snapshot
	if snapshot.Valid() {
		w.lastValid = snapshot
	}
	return snapshot, nil
}

// Index discovers KTML documents under the workspace root once and builds a
// persistent package-wide symbol snapshot. Later overlays update this set
// without rediscovering unchanged files.
func (w *Workspace) Index() (*Snapshot, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.indexed {
		entries, err := w.discover(func(path string) bool {
			if !w.deferStories || !IsStoryPath(path) {
				return true
			}
			_, open := w.overlays[path]
			return open
		})
		if err != nil {
			return nil, err
		}
		w.entries = entries
		w.indexed = true
	}
	snapshot, err := w.analyzeEntries(w.entries)
	if err != nil {
		return nil, err
	}
	w.latest = snapshot
	if snapshot.Valid() {
		w.lastValid = snapshot
	}
	return snapshot, nil
}

// discover returns the KTML documents under the root that keep accepts, in
// path order.
func (w *Workspace) discover(keep func(path string) bool) ([]string, error) {
	var entries []string
	// A directory with its own go.mod inside a module root is another
	// module, which Go's ./... leaves out too.
	_, statErr := os.Stat(filepath.Join(w.root, "go.mod"))
	rootModule := statErr == nil
	err := filepath.WalkDir(w.root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != w.root && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "vendor" || entry.Name() == "node_modules") {
				return filepath.SkipDir
			}
			if path != w.root && rootModule {
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if strings.HasSuffix(path, ".ktml") || IsSchemaPath(path) {
			canonical, err := canonicalPath(path)
			if err != nil {
				return err
			}
			if keep(canonical) {
				entries = append(entries, canonical)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(entries)
	return entries, nil
}

// DeferStories leaves story Documents out of Index's discovery, for a
// language server, whose features rarely need them: a story Document joins
// when it is opened (Update or Overlay before Index) and LoadStories brings
// in the rest. Sync does not add one changed on disk.
func (w *Workspace) DeferStories() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.deferStories = true
}

// LoadStories ends DeferStories: every story Document under the root joins,
// and an indexed workspace is analyzed again. It returns the latest
// snapshot, nil before Index.
func (w *Workspace) LoadStories() (*Snapshot, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.deferStories {
		return w.latest, nil
	}
	w.deferStories = false
	if !w.indexed {
		return w.latest, nil
	}
	stories, err := w.discover(IsStoryPath)
	if err != nil {
		return nil, err
	}
	for _, path := range stories {
		w.entries = appendUnique(w.entries, path)
	}
	sort.Strings(w.entries)
	w.generation++
	snapshot, err := w.analyzeEntries(w.entries)
	if err != nil {
		return nil, err
	}
	w.latest = snapshot
	if snapshot.Valid() {
		w.lastValid = snapshot
	}
	return snapshot, nil
}

// Update overlays one document and reanalyzes its dependency closure. Unchanged
// sources reuse their parsed syntax while semantic dependents are revalidated.
func (w *Workspace) Update(path string, version int, source []byte) (*Snapshot, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	path, err := canonicalPath(path)
	if err != nil {
		return nil, err
	}
	if existing, ok := w.overlays[path]; ok && version > 0 && existing.version > 0 && version <= existing.version {
		return w.latest, nil
	}
	w.overlays[path] = overlay{version: version, source: append([]byte(nil), source...)}
	w.generation++
	if len(w.entries) == 0 {
		w.entries = []string{path}
	} else if w.indexed {
		w.entries = appendUnique(w.entries, path)
	}
	snapshot, err := w.analyzeEntries(w.entries)
	if err != nil {
		return nil, err
	}
	w.latest = snapshot
	if snapshot.Valid() {
		w.lastValid = snapshot
	}
	return snapshot, nil
}

// Overlay records an editor overlay without analyzing it. The next Load,
// Index, or Update reads the document from it.
func (w *Workspace) Overlay(path string, version int, source []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	path, err := canonicalPath(path)
	if err != nil {
		return err
	}
	w.overlays[path] = overlay{version: version, source: append([]byte(nil), source...)}
	w.generation++
	return nil
}

// DropOverlay removes an editor overlay without analyzing: the next Load,
// Index, or Update reads the document from disk.
func (w *Workspace) DropOverlay(path string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	path, err := canonicalPath(path)
	if err != nil {
		return err
	}
	if _, ok := w.overlays[path]; ok {
		delete(w.overlays, path)
		w.generation++
	}
	return nil
}

// Indexed reports whether Index has discovered the workspace's documents.
func (w *Workspace) Indexed() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.indexed
}

// Close removes an editor overlay and restores the on-disk document.
func (w *Workspace) Close(path string) (*Snapshot, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	path, err := canonicalPath(path)
	if err != nil {
		return nil, err
	}
	delete(w.overlays, path)
	w.generation++
	if len(w.entries) == 0 {
		return w.latest, nil
	}
	if _, err := os.Stat(path); os.IsNotExist(err) && w.indexed {
		w.entries = removeString(w.entries, path)
	}
	snapshot, err := w.analyzeEntries(w.entries)
	if err != nil {
		return nil, err
	}
	w.latest = snapshot
	if snapshot.Valid() {
		w.lastValid = snapshot
	}
	return snapshot, nil
}

// Current returns the latest workspace-wide snapshot: the one the last
// Update, Close, or Index built after the workspace was indexed. It indexes
// the workspace on first use.
func (w *Workspace) Current() (*Snapshot, error) {
	w.mu.RLock()
	latest, indexed := w.latest, w.indexed
	w.mu.RUnlock()
	if indexed && latest != nil {
		return latest, nil
	}
	return w.Index()
}

// Sync brings an indexed workspace's document set in line with the disk for
// paths changed outside the editor: a KTML document that now exists joins
// it (a story Document only once stories are not deferred), and one that no longer exists, and is not open, leaves it. It reports
// whether the set changed; the next Index reads every document again.
func (w *Workspace) Sync(paths []string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.indexed {
		return false
	}
	changed := false
	for _, path := range paths {
		canonical, err := canonicalPath(path)
		if err != nil || !strings.HasSuffix(canonical, ".ktml") && !IsSchemaPath(canonical) {
			continue
		}
		if _, overlaid := w.overlays[canonical]; overlaid {
			continue
		}
		_, statErr := os.Stat(canonical)
		present := false
		for _, entry := range w.entries {
			present = present || entry == canonical
		}
		switch {
		case statErr == nil && !present && strings.HasPrefix(canonical, w.root+string(filepath.Separator)) && !(w.deferStories && IsStoryPath(canonical)):
			w.entries = appendUnique(w.entries, canonical)
			sort.Strings(w.entries)
			changed = true
		case os.IsNotExist(statErr) && present:
			w.entries = removeString(w.entries, canonical)
			changed = true
		}
	}
	if changed {
		w.generation++
	}
	return changed
}

func (w *Workspace) Snapshot() *Snapshot {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.latest
}

func (w *Workspace) LastValid() *Snapshot {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.lastValid
}

// ParseCount exposes parser work for incremental-analysis verification.
func (w *Workspace) ParseCount(path string) int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	path, err := canonicalPath(path)
	if err != nil {
		return 0
	}
	return w.parseRuns[path]
}

func (s *Snapshot) Valid() bool {
	if s == nil {
		return false
	}
	for _, diagnostics := range s.diagnostics {
		for _, diagnostic := range diagnostics {
			if diagnostic.Severity == SeverityError {
				return false
			}
		}
	}
	return true
}

// Err returns the first compiler error in deterministic file order.
func (s *Snapshot) Err() error {
	if s == nil {
		return fmt.Errorf("invalid compiler snapshot")
	}
	paths := make([]string, 0, len(s.diagnostics))
	for path := range s.diagnostics {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	remaining := 0
	for _, path := range paths {
		for _, diagnostic := range s.diagnostics[path] {
			if diagnostic.Severity == SeverityError {
				remaining++
			}
		}
	}
	for _, path := range paths {
		for _, diagnostic := range s.diagnostics[path] {
			if diagnostic.Severity != SeverityError {
				continue
			}
			remaining--
			if remaining > 0 {
				return fmt.Errorf("%s (and %d more errors)", diagnostic, remaining)
			}
			return diagnostic
		}
	}
	return nil
}

func (s *Snapshot) Resolve(file, name string) (Component, bool) {
	if s == nil {
		return Component{}, false
	}
	path, err := canonicalPath(file)
	if err != nil {
		return Component{}, false
	}
	id, ok := s.visible[path][name]
	if !ok {
		return Component{}, false
	}
	component, ok := s.components[id]
	component.Inputs = append([]Input(nil), component.Inputs...)
	return component, ok
}

func (s *Snapshot) Component(id SymbolID) (Component, bool) {
	if s == nil {
		return Component{}, false
	}
	component, ok := s.components[id]
	component.Inputs = append([]Input(nil), component.Inputs...)
	return component, ok
}

func (s *Snapshot) References(id SymbolID) []Reference {
	if s == nil {
		return nil
	}
	return append([]Reference(nil), s.references[id]...)
}

// SymbolAt resolves a component declaration, import, or call at a byte offset.
func (s *Snapshot) SymbolAt(file string, offset int) (Component, SourceRange, bool) {
	if s == nil {
		return Component{}, SourceRange{}, false
	}
	path, err := canonicalPath(file)
	if err != nil {
		return Component{}, SourceRange{}, false
	}
	for _, component := range s.components {
		if component.Range.File == path && offset >= component.Range.Start && offset <= component.Range.End {
			component.Inputs = append([]Input(nil), component.Inputs...)
			return component, component.Range, true
		}
	}
	for id, references := range s.references {
		if _, component := s.components[id]; !component {
			continue
		}
		for _, reference := range references {
			if reference.Range.File == path && offset >= reference.Range.Start && offset <= reference.Range.End {
				component := s.components[id]
				component.Inputs = append([]Input(nil), component.Inputs...)
				return component, reference.Range, true
			}
		}
	}
	return Component{}, SourceRange{}, false
}

func (s *Snapshot) Source(file string) []byte {
	if s == nil {
		return nil
	}
	path, err := canonicalPath(file)
	if err != nil {
		return nil
	}
	return append([]byte(nil), s.files[path].source...)
}

func (s *Snapshot) VisibleComponents(file string) []Component {
	if s == nil {
		return nil
	}
	path, err := canonicalPath(file)
	if err != nil {
		return nil
	}
	components := make([]Component, 0, len(s.visible[path]))
	for _, id := range s.visible[path] {
		component := s.components[id]
		component.Inputs = append([]Input(nil), component.Inputs...)
		components = append(components, component)
	}
	sort.Slice(components, func(i, j int) bool { return components[i].Name < components[j].Name })
	return components
}

func (s *Snapshot) Components() []Component {
	if s == nil {
		return nil
	}
	components := make([]Component, 0, len(s.components))
	for _, component := range s.components {
		component.Inputs = append([]Input(nil), component.Inputs...)
		components = append(components, component)
	}
	sort.Slice(components, func(i, j int) bool {
		if components[i].Name != components[j].Name {
			return components[i].Name < components[j].Name
		}
		return components[i].File < components[j].File
	})
	return components
}

func (s *Snapshot) Dependencies(file string) []string {
	if s == nil {
		return nil
	}
	path, err := canonicalPath(file)
	if err != nil {
		return nil
	}
	return append([]string(nil), s.dependencies[path]...)
}

func (s *Snapshot) Diagnostics(file string) []Diagnostic {
	if s == nil {
		return nil
	}
	path, err := canonicalPath(file)
	if err != nil {
		return nil
	}
	return append([]Diagnostic(nil), s.diagnostics[path]...)
}

// Files returns the path of every document the snapshot loaded, in path
// order.
func (s *Snapshot) Files() []string {
	if s == nil {
		return nil
	}
	return sortedFilePaths(s.files)
}

// Documents returns the validated syntax documents in deterministic path order.
func (s *Snapshot) Documents() []*ast.Document {
	if s == nil {
		return nil
	}
	documents := make([]*ast.Document, 0, len(s.files))
	for _, path := range sortedFilePaths(s.files) {
		if document := s.files[path].doc; document != nil {
			documents = append(documents, document)
		}
	}
	return documents
}

func (w *Workspace) analyze(entry string) (*Snapshot, error) {
	return w.analyzeEntries([]string{entry})
}

func (w *Workspace) analyzeEntries(entries []string) (*Snapshot, error) {
	snapshot := &Snapshot{
		root:         w.root,
		files:        make(map[string]workspaceFile),
		components:   make(map[SymbolID]Component),
		visible:      make(map[string]map[string]SymbolID),
		references:   make(map[SymbolID][]Reference),
		dependencies: make(map[string][]string),
		diagnostics:  make(map[string][]Diagnostic),
		records:      make(map[SymbolID]Record),
		states:       make(map[SymbolID]State),
		groups:       make(map[SymbolID]ClauseGroup),
		types:        make(map[string]map[string]SymbolID),
		paths:        make(map[string][]ResolvedPath),

		repetitions:      make(map[SymbolID]Repetition),
		repetitionSyntax: make(map[*ast.RepetitionNode]SymbolID),
		contentUses:      make(map[string][]ContentUse),
		callSources:      make(map[*ast.ElementNode]string),
		outletSources:    make(map[*ast.ElementNode]string),
		stories:          make(map[SymbolID]Story),
		fixtures:         make(map[SymbolID]Fixture),

		generation: w.generation,
		stamps:     make(map[string]fileStamp),
	}
	visiting := make(map[string]bool)
	for _, entry := range entries {
		if err := w.loadFile(snapshot, entry, visiting); err != nil {
			return nil, err
		}
	}
	snapshot.indexDeclarations()
	snapshot.indexRecords()
	snapshot.indexStates()
	snapshot.resolveImports()
	snapshot.resolveRecordFields()
	snapshot.resolveStateFields()
	snapshot.checkRecordCycles()
	snapshot.resolveInputTypes()
	snapshot.checkGeneratedNames()
	snapshot.resolveClauseGroups()
	snapshot.resolveCalls()
	snapshot.checkDocumentKinds()
	snapshot.resolveStories()
	snapshot.sort()
	return snapshot, nil
}

func (w *Workspace) loadFile(snapshot *Snapshot, path string, visiting map[string]bool) error {
	if _, loaded := snapshot.files[path]; loaded {
		return nil
	}
	if visiting[path] {
		return nil
	}
	visiting[path] = true
	defer delete(visiting, path)
	overlay, overlaid := w.overlays[path]
	var source []byte
	if overlaid {
		source = overlay.source
	}
	if !overlaid {
		info, statErr := os.Stat(path)
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		source = content
		if statErr == nil {
			snapshot.stamps[path] = fileStamp{size: info.Size(), modTime: info.ModTime()}
		}
	}
	result := w.compile(path, source)
	file := workspaceFile{path: path, source: append([]byte(nil), source...), doc: result.Document}
	snapshot.files[path] = file
	snapshot.diagnostics[path] = append(snapshot.diagnostics[path], result.Diagnostics...)
	if result.HasErrors() {
		return nil
	}
	for _, node := range result.Document.Nodes {
		imp, ok := node.(*ast.ImportStmt)
		if !ok {
			continue
		}
		dependency, err := resolveImportPath(path, imp.Path)
		if err != nil {
			snapshot.addDiagnostic(path, "invalid-import", err.Error(), imp.Pos(), imp.End())
			continue
		}
		if IsSchemaPath(path) && !IsSchemaPath(dependency) {
			snapshot.addDiagnostic(path, "schema-imports-component", fmt.Sprintf("schema document cannot import components from %s", dependency), imp.PathRange.Start, imp.PathRange.End)
			continue
		}
		snapshot.dependencies[path] = appendUnique(snapshot.dependencies[path], dependency)
		if _, overlaid := w.overlays[dependency]; !overlaid {
			if _, err := os.Stat(dependency); err != nil {
				snapshot.missing = append(snapshot.missing, dependency)
				snapshot.addDiagnostic(path, "unresolved-import", fmt.Sprintf("cannot import %s: %v", dependency, err), imp.Pos(), imp.End())
				continue
			}
		}
		if visiting[dependency] {
			snapshot.addDiagnostic(path, "import-cycle", fmt.Sprintf("import cycle includes %s", dependency), imp.Pos(), imp.End())
			continue
		}
		if err := w.loadFile(snapshot, dependency, visiting); err != nil {
			snapshot.addDiagnostic(path, "unresolved-import", err.Error(), imp.Pos(), imp.End())
			continue
		}
		if snapshot.hasErrors(dependency) {
			snapshot.addDiagnostic(path, "invalid-dependency", fmt.Sprintf("imported file %s contains errors", dependency), imp.Pos(), imp.End())
		}
	}
	return nil
}

func (w *Workspace) compile(path string, source []byte) Result {
	key := string(source)
	if cached, ok := w.parsed[path]; ok && cached.source == key {
		return cached.result
	}
	result := CompileSyntax(path, source)
	w.parsed[path] = parsedFile{source: key, result: result}
	w.parseRuns[path]++
	return result
}

func (s *Snapshot) indexDeclarations() {
	paths := sortedFilePaths(s.files)
	for _, path := range paths {
		file := s.files[path]
		if file.doc == nil {
			continue
		}
		s.visible[path] = make(map[string]SymbolID)
		groups := make(map[string][]*ast.ElementDecl)
		var names []string
		for _, node := range file.doc.Nodes {
			declaration, ok := node.(*ast.ElementDecl)
			if !ok {
				continue
			}
			if IsSchemaPath(path) {
				s.addDiagnostic(path, "component-in-schema", fmt.Sprintf("component %s must be declared in a .ktml document", declaration.Name), declaration.Pos(), declaration.Pos()+len(declaration.Name))
				continue
			}
			if _, exists := groups[declaration.Name]; !exists {
				names = append(names, declaration.Name)
			}
			groups[declaration.Name] = append(groups[declaration.Name], declaration)
		}
		sort.Strings(names)
		for _, name := range names {
			declaration, duplicates := baseDeclaration(groups[name])
			if declaration == nil {
				s.addDiagnostic(path, "missing-component-schema", fmt.Sprintf("component %s has overrides but no typed base declaration", name), groups[name][0].Pos(), groups[name][0].End())
				continue
			}
			if duplicates > 0 {
				s.addDiagnostic(path, "duplicate-component", fmt.Sprintf("component %s has multiple base declarations", name), declaration.Pos(), declaration.Pos()+len(name))
				continue
			}
			id := componentID(path, name)
			component := Component{ID: id, Name: name, File: path, Range: SourceRange{File: path, Start: declaration.Pos(), End: declaration.Pos() + len(name)}}
			inputNames := make(map[string]bool)
			generatedNames := make(map[string]Input)
			for _, attribute := range declaration.Attributes {
				if attribute.Kind != ast.InputDeclaration {
					continue
				}
				if inputNames[attribute.Name] {
					s.addDiagnostic(path, "duplicate-input", fmt.Sprintf("component %s declares input %s more than once", name, attribute.Name), attribute.Pos(), attribute.End())
					continue
				}
				inputNames[attribute.Name] = true
				generatedName := GeneratedGoField(attribute.Name)
				if !validGeneratedGoField(generatedName) {
					s.addDiagnostic(path, "invalid-generated-field", fmt.Sprintf("input %s does not normalize to an exported Go field", attribute.Name), attribute.NameRange.Start, attribute.NameRange.End)
					continue
				}
				if first, collision := generatedNames[generatedName]; collision {
					s.addDiagnostic(path, "generated-field-collision", fmt.Sprintf("inputs %s and %s both normalize to Go field %s", first.Name, attribute.Name, generatedName), attribute.NameRange.Start, attribute.NameRange.End)
					continue
				}
				mode := InputMode(attribute.Mode)
				var defaultValue *ScalarValue
				if attribute.Default != nil {
					defaultValue = semanticScalar(attribute.Default)
					defaultValue.Range.File = path
				}
				input := Input{
					Name: attribute.Name, Mode: mode, Default: defaultValue,
					typeExpr:  typeExpression(attribute.TypeExpr, attribute.TypeDecl, attribute.TypeRange),
					Range:     SourceRange{File: path, Start: attribute.Pos(), End: attribute.End()},
					NameRange: SourceRange{File: path, Start: attribute.NameRange.Start, End: attribute.NameRange.End},
					TypeRange: SourceRange{File: path, Start: attribute.TypeRange.Start, End: attribute.TypeRange.End},
				}
				if attribute.OptionalMarker != nil {
					input.OptionalMarkerRange = &SourceRange{File: path, Start: attribute.OptionalMarker.Start, End: attribute.OptionalMarker.End}
				}
				if attribute.DefaultOperator != nil {
					input.DefaultOperatorRange = &SourceRange{File: path, Start: attribute.DefaultOperator.Start, End: attribute.DefaultOperator.End}
				}
				component.Inputs = append(component.Inputs, input)
				generatedNames[generatedName] = input
			}
			if outlet := firstChildrenOutlet(declaration.Body); outlet != nil && !inputNames[ChildrenInput] && !declaration.IsSignature() {
				outletRange := SourceRange{File: path, Start: outlet.Pos(), End: outlet.Pos() + len(outlet.Name)}
				component.Inputs = append(component.Inputs, Input{
					Name: ChildrenInput, Mode: OptionalInput, Implicit: true,
					typeExpr: &ast.TypeExpr{P: outletRange.Start, E: outletRange.End, Kind: ast.NamedTypeExpr, Name: ContentTypeName},
					Range:    outletRange, NameRange: outletRange, TypeRange: outletRange,
				})
			}
			s.components[id] = component
			s.visible[path][name] = id
		}
	}
}

func (s *Snapshot) resolveImports() {
	for _, path := range sortedFilePaths(s.files) {
		file := s.files[path]
		if file.doc == nil {
			continue
		}
		for _, node := range file.doc.Nodes {
			imp, ok := node.(*ast.ImportStmt)
			if !ok {
				continue
			}
			dependency, err := resolveImportPath(path, imp.Path)
			if err != nil {
				continue
			}
			if IsSchemaPath(path) && !IsSchemaPath(dependency) {
				continue
			}
			if s.hasErrors(dependency) {
				continue
			}
			for _, specifier := range importSpecifiers(file.source, imp) {
				nameRange := SourceRange{File: path, Start: specifier.NameRange.Start, End: specifier.NameRange.End}
				localRange := nameRange
				if specifier.AliasRange != nil {
					localRange = SourceRange{File: path, Start: specifier.AliasRange.Start, End: specifier.AliasRange.End}
				}
				if IsSchemaPath(dependency) {
					id, exists := s.exportedType(dependency, specifier.Name)
					if !exists {
						s.addDiagnostic(path, "unknown-import", fmt.Sprintf("%s does not export record or state %s", dependency, specifier.Name), nameRange.Start, nameRange.End)
						continue
					}
					local := specifier.LocalName()
					if existing, collision := s.types[path][local]; collision && existing != id {
						s.addDiagnostic(path, "import-collision", fmt.Sprintf("type name %s is already in scope", local), localRange.Start, localRange.End)
						continue
					}
					if s.types[path] == nil {
						s.types[path] = make(map[string]SymbolID)
					}
					s.types[path][local] = id
					s.references[id] = append(s.references[id], Reference{Symbol: id, Kind: ImportReference, Range: nameRange})
					continue
				}
				if specifier.Alias != "" {
					s.addDiagnostic(path, "unsupported-component-alias", fmt.Sprintf("component import alias %s is not supported yet", specifier.Alias), localRange.Start, localRange.End)
					continue
				}
				id := componentID(dependency, specifier.Name)
				if _, exists := s.components[id]; !exists {
					s.addDiagnostic(path, "unknown-import", fmt.Sprintf("%s does not export component %s", dependency, specifier.Name), nameRange.Start, nameRange.End)
					continue
				}
				if existing, collision := s.visible[path][specifier.Name]; collision && existing != id {
					s.addDiagnostic(path, "import-collision", fmt.Sprintf("component name %s is already in scope", specifier.Name), nameRange.Start, nameRange.End)
					continue
				}
				s.visible[path][specifier.Name] = id
				s.references[id] = append(s.references[id], Reference{Symbol: id, Kind: ImportReference, Range: nameRange})
			}
		}
	}
}

// importSpecifiers returns parsed specifiers, synthesizing ranges for
// hand-built syntax trees that only carry item names.
func importSpecifiers(source []byte, imp *ast.ImportStmt) []ast.ImportSpecifier {
	if len(imp.Specifiers) == len(imp.Items) && len(imp.Specifiers) > 0 {
		return imp.Specifiers
	}
	specifiers := make([]ast.ImportSpecifier, 0, len(imp.Items))
	for _, item := range imp.Items {
		start := importItemOffset(source, imp, item)
		specifiers = append(specifiers, ast.ImportSpecifier{Name: item, NameRange: ast.SourceRange{Start: start, End: start + len(item)}})
	}
	return specifiers
}

func (s *Snapshot) hasErrors(path string) bool {
	for _, diagnostic := range s.diagnostics[path] {
		if diagnostic.Severity == SeverityError {
			return true
		}
	}
	return false
}

func (s *Snapshot) resolveCalls() {
	for _, path := range sortedFilePaths(s.files) {
		file := s.files[path]
		if file.doc == nil || IsSchemaPath(path) {
			continue
		}
		sources := make(map[string]*sourceCounters)
		for _, node := range file.doc.Nodes {
			var name string
			var body []ast.Node
			var locals []Binding
			switch declaration := node.(type) {
			case *ast.ElementDecl:
				name, body = declaration.Name, declaration.Body
			case *ast.RenderClause:
				name, body = declaration.Name, declaration.Body
				clause, ok := s.clauseAt(path, declaration)
				if !ok {
					continue
				}
				locals = clause.Bindings
			default:
				continue
			}
			callerID, callerExists := s.visible[path][name]
			if sources[name] == nil {
				sources[name] = new(sourceCounters)
			}
			caller := pathScope{component: s.components[callerID], locals: locals, sources: sources[name]}
			if !callerExists {
				caller.component = Component{}
			}
			s.validateMarkup(path, body)
			for _, child := range body {
				s.resolveNode(path, file.source, caller, child)
			}
		}
	}
}

func (s *Snapshot) resolveNode(path string, source []byte, caller pathScope, node ast.Node) {
	if text, ok := node.(*ast.TextNode); ok {
		s.resolveQuotedValue(path, caller, text.Quoted, textOrInputContext, "")
		return
	}
	if repetition, ok := node.(*ast.RepetitionNode); ok {
		s.resolveRepetition(path, source, caller, repetition)
		return
	}
	if condition, ok := node.(*ast.ConditionNode); ok {
		s.resolveConditionNode(path, source, caller, condition)
		return
	}
	if raw, ok := node.(*ast.RawTextNode); ok {
		s.addDiagnostic(path, "raw-text-placement", "a ``` raw-text literal ``` is only the content of a script or style element", raw.P, raw.E)
		return
	}
	if slot, ok := node.(*ast.SlotNode); ok {
		s.addDiagnostic(path, "slot-outside-call", fmt.Sprintf("slot %s must be a direct body node of a component call", slot.Name), slot.KeywordRange.Start, slot.NameRange.End)
		for _, child := range slot.Body {
			s.resolveNode(path, source, caller, child)
		}
		return
	}
	element, ok := node.(*ast.ElementNode)
	if !ok {
		return
	}
	if s.resolveOutlet(path, caller, element) {
		return
	}
	id, componentCall := s.visible[path][element.Name]
	if componentCall {
		for _, attribute := range element.Attributes {
			if !attribute.IsNamed() {
				continue
			}
			if _, input := inputNamed(s.components[id], attribute.Name); input {
				if _, exact := exactQuotedPath(attribute.Quoted); exact {
					// Call validation types exact input arguments against the target input.
					continue
				}
			}
			s.resolveQuotedValue(path, caller, attribute.Quoted, textOrInputContext, attribute.Name)
		}
	} else {
		s.resolveAttributes(path, caller, element.Attributes)
		s.validateAttributeCollisions(path, element.Attributes)
		s.validateSpreads(path, element.Attributes)
	}
	if componentCall {
		if caller.sources != nil {
			s.callSources[element] = "c" + strconv.Itoa(caller.sources.calls)
			caller.sources.calls++
		}
		target := s.components[id]
		s.references[id] = append(s.references[id], Reference{Symbol: id, Kind: CallReference, Range: SourceRange{File: path, Start: element.Pos(), End: element.Pos() + len(element.Name)}})
		s.validateCall(path, caller, target, element)
	} else if startsUpper(element.Name) {
		s.addDiagnostic(path, "unknown-component", fmt.Sprintf("component %s is not in scope", element.Name), element.Pos(), element.Pos()+len(element.Name))
	} else if RawTextElement(element.Name) {
		s.validateRawTextBody(path, caller, element)
		return
	}
	for _, child := range element.Children {
		if slot, fill := child.(*ast.SlotNode); fill && componentCall {
			s.resolveSlotFill(path, caller, s.components[id], slot)
			for _, nested := range slot.Body {
				s.resolveNode(path, source, caller, nested)
			}
			continue
		}
		s.resolveNode(path, source, caller, child)
	}
}

func inputNamed(component Component, name string) (Input, bool) {
	for _, input := range component.Inputs {
		if input.Name == name {
			return input, true
		}
	}
	return Input{}, false
}

// renderContext is the output context of a quoted value's paths.
type renderContext int

const (
	// textOrInputContext is HTML text, or a component call input value.
	textOrInputContext renderContext = iota
	attributeContext
	// scriptAttributeContext is an element attribute that ScriptAttribute
	// classifies: its value is evaluated as JavaScript.
	scriptAttributeContext
	// styleAttributeContext is the style attribute: its value is CSS.
	styleAttributeContext
	// scriptBodyContext is the quoted text of a script element.
	scriptBodyContext
	// urlAttributeContext is an element attribute that URLAttribute
	// classifies: its value is a URL.
	urlAttributeContext
	// urlTailContext is a URL attribute whose literal text before the first
	// path fixes the scheme and host (SafeURLPrefix): its paths are scalar
	// path or query text.
	urlTailContext
)

// resolveQuotedValue types the paths of a text or attribute value. attribute
// names the element attribute for script-attribute diagnostics.
func (s *Snapshot) resolveQuotedValue(path string, caller pathScope, quoted *ast.QuotedValue, context renderContext, attribute string) {
	if quoted == nil {
		return
	}
	_, exact := exactQuotedPath(quoted)
	if context == urlAttributeContext && SafeURLPrefix(quoted) {
		context = urlTailContext
	}
	for _, segment := range quoted.Segments {
		if segment.Kind != ast.PathSegment || segment.Path == nil || len(segment.Path.Parts) == 0 {
			continue
		}
		s.resolveRenderedPath(path, caller, segment.Path, context, exact, attribute)
	}
}

func (s *Snapshot) validateCall(path string, caller pathScope, target Component, call *ast.ElementNode) {
	targetInputs := make(map[string]Input, len(target.Inputs))
	for _, input := range target.Inputs {
		targetInputs[input.Name] = input
	}
	supplied := make(map[string]bool)
	s.reportConditionalInputs(path, target.Name, call)
	for _, attribute := range call.Attributes {
		if !attribute.IsNamed() {
			continue
		}
		input, exists := targetInputs[attribute.Name]
		if !exists {
			s.addDiagnostic(path, "unknown-input", fmt.Sprintf("component %s has no input %s", target.Name, attribute.Name), attribute.Pos(), attribute.Pos()+len(attribute.Name))
			continue
		}
		if input.IsContent() {
			s.addDiagnostic(path, "content-as-attribute", fmt.Sprintf("content input %s cannot be supplied as an attribute; fill it with slot %s { … }", attribute.Name, attribute.Name), attribute.Pos(), attribute.End())
			continue
		}
		if supplied[attribute.Name] {
			s.addDiagnostic(path, "duplicate-input", fmt.Sprintf("input %s is supplied more than once", attribute.Name), attribute.Pos(), attribute.End())
			continue
		}
		supplied[attribute.Name] = true
		if attribute.BagRange != nil {
			if input.Type.Kind != ContextualTypeKind || input.Type.Name != AttributesType {
				s.addDiagnostic(path, "attribute-bag-placement", fmt.Sprintf("input %s requires %s; an attribute bag ( … ) supplies only an Attributes input", attribute.Name, input.Type), attribute.BagRange.Start, attribute.BagRange.End)
				continue
			}
			s.resolveAttributeBag(path, caller, attribute)
			continue
		}
		if expression, ok := exactQuotedPath(attribute.Quoted); ok {
			resolved, ok := s.resolvePath(path, caller, expression)
			if !ok {
				continue
			}
			if resolved.Optional {
				last := resolved.Parts[len(resolved.Parts)-1]
				s.addDiagnostic(path, "optional-value-access", fmt.Sprintf("optional %s %s must be narrowed before value access", last.kind(), last.Name), last.Range.Start, last.Range.End)
				continue
			}
			if resolved.Type.Kind == ContextualTypeKind && input.Type.Kind != ContextualTypeKind {
				s.addDiagnostic(path, "contextual-type-mismatch", fmt.Sprintf("input %s requires %s; %s value %s cannot be used as one", attribute.Name, input.Type, resolved.Type, expression.String()), attribute.Pos(), attribute.End())
				continue
			}
			if !resolved.Type.Equal(input.Type) {
				s.addDiagnostic(path, "input-type-mismatch", fmt.Sprintf("input %s requires %s but %s is %s", attribute.Name, input.Type, expression.String(), resolved.Type), attribute.Pos(), attribute.End())
			}
			continue
		}
		if !literalMatches(attribute.Value, input.Type) {
			s.addDiagnostic(path, "input-type-mismatch", fmt.Sprintf("input %s requires %s", attribute.Name, input.Type), attribute.Pos(), attribute.End())
		}
	}
	for _, input := range target.Inputs {
		if input.Mode == RequiredInput && !input.IsContent() && !supplied[input.Name] {
			s.addDiagnostic(path, "missing-input", fmt.Sprintf("component %s requires input %s", target.Name, input.Name), call.Pos(), call.Pos()+len(call.Name))
		}
	}
	s.validateCallContent(path, target, call)
}

func (s *Snapshot) addDiagnostic(path, code, message string, start, end int) {
	s.addSeverity(path, SeverityError, code, message, start, end)
}

// addWarning records a nonfatal semantic diagnostic shared by every consumer.
func (s *Snapshot) addWarning(path, code, message string, start, end int) {
	s.addSeverity(path, SeverityWarning, code, message, start, end)
}

func (s *Snapshot) addSeverity(path string, severity Severity, code, message string, start, end int) {
	if end <= start {
		end = start + 1
	}
	s.diagnostics[path] = append(s.diagnostics[path], Diagnostic{File: path, Code: code, Message: message, Severity: severity, Start: start, End: end})
}

func (s *Snapshot) sort() {
	for path := range s.dependencies {
		sort.Strings(s.dependencies[path])
	}
	for id := range s.references {
		sort.Slice(s.references[id], func(i, j int) bool {
			a, b := s.references[id][i].Range, s.references[id][j].Range
			if a.File != b.File {
				return a.File < b.File
			}
			return a.Start < b.Start
		})
	}
	for path := range s.diagnostics {
		sort.SliceStable(s.diagnostics[path], func(i, j int) bool {
			return s.diagnostics[path][i].Start < s.diagnostics[path][j].Start
		})
	}
}

func canonicalPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}

// ResolveImportPath returns the canonical absolute source path that an import
// statement in importer names.
func ResolveImportPath(importer, rawPath string) (string, error) {
	return resolveImportPath(importer, rawPath)
}

func resolveImportPath(importer, rawPath string) (string, error) {
	path, err := strconv.Unquote(rawPath)
	if err != nil {
		return "", fmt.Errorf("invalid import path %s", rawPath)
	}
	if !strings.HasSuffix(path, ".ktml") && !IsSchemaPath(path) {
		path += ".ktml"
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(importer), path)
	}
	return canonicalPath(path)
}

func componentID(path, name string) SymbolID {
	return SymbolID(path + "::component::" + name)
}

func sortedFilePaths(files map[string]workspaceFile) []string {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func removeString(values []string, value string) []string {
	for index, existing := range values {
		if existing == value {
			return append(values[:index], values[index+1:]...)
		}
	}
	return values
}

func baseDeclaration(declarations []*ast.ElementDecl) (*ast.ElementDecl, int) {
	var base *ast.ElementDecl
	duplicates := 0
	for _, declaration := range declarations {
		override := false
		for _, attribute := range declaration.Attributes {
			if attribute.Kind == ast.OverrideDeclaration {
				override = true
				break
			}
		}
		if override {
			continue
		}
		if base != nil {
			duplicates++
			continue
		}
		base = declaration
	}
	return base, duplicates
}

func semanticScalar(literal *ast.ScalarLiteral) *ScalarValue {
	if literal == nil {
		return nil
	}
	return &ScalarValue{
		Kind: ScalarLiteralKind(literal.Kind), Raw: literal.Raw, String: literal.String,
		Boolean: literal.Boolean, Number: literal.Number,
		Range: SourceRange{Start: literal.P, End: literal.E},
	}
}

// GeneratedGoField is the Go identifier generated code uses for a KTML input
// or component name: the first letter and every letter after a hyphen are
// uppercased, and hyphens are dropped.
func GeneratedGoField(name string) string {
	var result strings.Builder
	upperNext := true
	for _, current := range name {
		if current == '-' {
			upperNext = true
			continue
		}
		if upperNext {
			current = unicode.ToUpper(current)
			upperNext = false
		}
		result.WriteRune(current)
	}
	return result.String()
}

func validGeneratedGoField(name string) bool {
	for _, current := range name {
		return unicode.IsUpper(current)
	}
	return false
}

// ChildrenInput is the name of the unlabelled content input.
const ChildrenInput = "children"

// UsesChildrenOutlet reports whether a body renders a children outlet in any
// body position.
func UsesChildrenOutlet(nodes []ast.Node) bool {
	return firstChildrenOutlet(nodes) != nil
}

// firstChildrenOutlet returns the first children outlet in source order,
// including outlets nested in calls, slot fills, repetitions, and branches.
func firstChildrenOutlet(nodes []ast.Node) *ast.ElementNode {
	for _, node := range nodes {
		var nested [][]ast.Node
		switch value := node.(type) {
		case *ast.ElementNode:
			if value.Name == ChildrenInput && len(value.Attributes) == 0 && len(value.Children) == 0 {
				return value
			}
			nested = append(nested, value.Children)
		case *ast.RepetitionNode:
			nested = append(nested, value.Body)
		case *ast.SlotNode:
			nested = append(nested, value.Body)
		case *ast.ConditionNode:
			nested = append(nested, value.Then, value.Else)
		}
		for _, body := range nested {
			if outlet := firstChildrenOutlet(body); outlet != nil {
				return outlet
			}
		}
	}
	return nil
}

func importItemOffset(source []byte, imp *ast.ImportStmt, item string) int {
	if imp.Pos() < 0 || imp.Pos() >= len(source) {
		return imp.Pos()
	}
	end := imp.End()
	if end > len(source) {
		end = len(source)
	}
	if relative := strings.Index(string(source[imp.Pos():end]), item); relative >= 0 {
		return imp.Pos() + relative
	}
	return imp.Pos()
}

func startsUpper(name string) bool {
	for _, r := range name {
		return unicode.IsUpper(r)
	}
	return false
}

func exactQuotedPath(quoted *ast.QuotedValue) (*ast.PathExpression, bool) {
	if quoted == nil || len(quoted.Segments) != 1 || quoted.Segments[0].Kind != ast.PathSegment || quoted.Segments[0].Path == nil || len(quoted.Segments[0].Path.Parts) == 0 {
		return nil, false
	}
	return quoted.Segments[0].Path, true
}

func literalMatches(raw string, valueType Type) bool {
	if valueType.Kind != ScalarTypeKind {
		return false
	}
	value := raw
	quoted := false
	if unquoted, err := strconv.Unquote(raw); err == nil {
		value = unquoted
		quoted = true
	}
	switch valueType.Scalar {
	case StringType:
		return quoted || value != ""
	case BooleanType:
		return !quoted && (value == "true" || value == "false")
	case NumberType:
		if quoted {
			return false
		}
		_, err := strconv.ParseFloat(value, 64)
		return err == nil
	case IntegerType:
		return !quoted && integerLiteral(value)
	default:
		return false
	}
}
