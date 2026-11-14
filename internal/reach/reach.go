// Package reach finds the components an application actually renders: the
// Chi routes of the HTMX route index, the Go code their handlers reach, the
// components that code constructs (pages), and the components those call
// in KTML, transitively.
//
// The analysis is static and conservative within the module. Go packages of
// the module are type-checked from source; everything outside it is an empty
// package, so calls through values of outside types are not followed. A
// reference to a function or method counts as a call, and a call through an
// interface method reaches every method of that name on a module type that
// has the interface's method names.
package reach

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"go/types"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/config"
	"github.com/karl-tye/ktml/internal/gopkg"
	"github.com/karl-tye/ktml/internal/plugin/htmx"
	"github.com/karl-tye/ktml/internal/symbolindex"
)

// Options configure an analysis.
type Options struct {
	// Root is the project root, inside a Go module.
	Root string
	// Chi configures the route index: the directory of the Chi
	// registrations and any custom matchers.
	Chi htmx.ChiSettings
	// Snapshot is the indexed KTML workspace of Root.
	Snapshot *compiler.Snapshot
}

// Route is a route that reaches a component.
type Route struct {
	Method  string
	Path    string
	Handler string
}

// String is the route as "GET /jobs/{id}".
func (r Route) String() string { return r.Method + " " + r.Path }

// Component is a reachable component.
type Component struct {
	compiler.Component
	// Page marks a component the Go code of a handler constructs; the rest
	// are reached only through KTML calls.
	Page bool
	// Routes are the routes that reach it, by path and method.
	Routes []Route
}

// Result is the reachability of a workspace's components.
type Result struct {
	// Routes is the route index.
	Routes []htmx.Route
	// Reachable holds the components the routes reach, by symbol.
	Reachable map[compiler.SymbolID]*Component
	// Unreachable are the components no route reaches, by file and name.
	Unreachable []compiler.Component
	// Warnings are routes whose handlers could not be followed and route
	// index diagnostics.
	Warnings []string
}

// Lookup returns a reachable component. A nil Result reaches nothing.
func (r *Result) Lookup(id compiler.SymbolID) (*Component, bool) {
	if r == nil {
		return nil, false
	}
	component, ok := r.Reachable[id]
	return component, ok
}

// Pages counts the reachable pages.
func (r *Result) Pages() int {
	count := 0
	for _, component := range r.Reachable {
		if component.Page {
			count++
		}
	}
	return count
}

// ChiSettings is the route index configuration of a project root: dir when
// it is set, else the HTMX plugin's chi settings in ktml.config.json, else
// the whole module with the default matchers.
func ChiSettings(root, dir string) (htmx.ChiSettings, error) {
	settings := htmx.ChiSettings{Patterns: htmx.Patterns{Defaults: true}}
	path := filepath.Join(root, "ktml.config.json")
	if _, err := os.Stat(path); err == nil {
		cfg, err := config.Load(path)
		if err != nil {
			return settings, err
		}
		if plugin, ok := cfg.Plugin["htmx"]; ok && plugin.Settings != nil {
			data, err := json.Marshal(plugin.Settings)
			if err != nil {
				return settings, err
			}
			parsed, err := htmx.ParseSettings(data)
			if err != nil {
				return settings, fmt.Errorf("plugin.htmx.settings: %w", err)
			}
			settings = parsed.Chi
		}
	}
	if dir != "" {
		settings.Dir = dir
	}
	if settings.Dir != "" {
		info, err := os.Stat(filepath.Join(root, settings.Dir))
		if filepath.IsAbs(settings.Dir) {
			info, err = os.Stat(settings.Dir)
		}
		if err != nil || !info.IsDir() {
			return settings, errors.New("the Chi directory " + settings.Dir + " is not a directory")
		}
	}
	return settings, nil
}

// Analyze computes reachability.
func Analyze(options Options) (*Result, error) {
	root, err := filepath.Abs(options.Root)
	if err != nil {
		return nil, err
	}
	moduleRoot, modulePath, err := gopkg.FindModule(root)
	if err != nil {
		return nil, err
	}
	chi := options.Chi
	if chi.Dir == "" {
		chi.Dir = moduleRoot
	} else if !filepath.IsAbs(chi.Dir) {
		chi.Dir = filepath.Join(root, chi.Dir)
	}
	if !chi.Patterns.Defaults && len(chi.Patterns.Scope)+len(chi.Patterns.Mount)+len(chi.Patterns.Routes) == 0 {
		chi.Patterns.Defaults = true
	}
	index, err := htmx.DiscoverRoutesWith(chi)
	if err != nil {
		return nil, fmt.Errorf("route index: %w", err)
	}
	result := &Result{Routes: index.Routes, Reachable: make(map[compiler.SymbolID]*Component)}
	for _, diagnostic := range index.Diagnostics {
		result.Warnings = append(result.Warnings, fmt.Sprintf("%s:%d: [%s] %s", uriPath(diagnostic.Location.URI), diagnostic.Location.Range.Start.Line+1, diagnostic.Code, diagnostic.Message))
	}

	program, err := load(moduleRoot, modulePath)
	if err != nil {
		return nil, err
	}
	program.components = make(map[string]compiler.SymbolID)
	for _, component := range options.Snapshot.Components() {
		if compiler.IsStoryPath(component.File) {
			continue
		}
		program.components[siteKey(filepath.Dir(component.File), compiler.GeneratedGoField(component.Name))] = component.ID
	}
	program.build()

	calls := callGraph(options.Snapshot)
	for _, route := range index.Routes {
		reference := Route{Method: route.Method, Path: route.Path, Handler: route.Handler}
		node, ok := program.handler(route)
		if !ok {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s:%d: the handler %s of %s %s could not be followed", uriPath(route.Declaration.URI), route.Declaration.Range.Start.Line+1, route.Handler, route.Method, route.Path))
			continue
		}
		pages := program.reach(node)
		seen := make(map[compiler.SymbolID]bool)
		var queue []compiler.SymbolID
		for _, id := range pages {
			result.add(options.Snapshot, id, true, reference)
			seen[id] = true
			queue = append(queue, id)
		}
		for len(queue) > 0 {
			id := queue[0]
			queue = queue[1:]
			for _, callee := range calls[id] {
				if !seen[callee] {
					seen[callee] = true
					result.add(options.Snapshot, callee, false, reference)
					queue = append(queue, callee)
				}
			}
		}
	}
	for _, component := range result.Reachable {
		sort.Slice(component.Routes, func(i, j int) bool {
			if component.Routes[i].Path != component.Routes[j].Path {
				return component.Routes[i].Path < component.Routes[j].Path
			}
			return component.Routes[i].Method < component.Routes[j].Method
		})
	}
	for _, component := range options.Snapshot.Components() {
		if compiler.IsStoryPath(component.File) {
			continue
		}
		if _, ok := result.Reachable[component.ID]; !ok {
			result.Unreachable = append(result.Unreachable, component)
		}
	}
	sort.Slice(result.Unreachable, func(i, j int) bool {
		if result.Unreachable[i].File != result.Unreachable[j].File {
			return result.Unreachable[i].File < result.Unreachable[j].File
		}
		return result.Unreachable[i].Range.Start < result.Unreachable[j].Range.Start
	})
	sort.Strings(result.Warnings)
	return result, nil
}

func (r *Result) add(snapshot *compiler.Snapshot, id compiler.SymbolID, page bool, route Route) {
	component, ok := r.Reachable[id]
	if !ok {
		declared, found := snapshot.Component(id)
		if !found {
			return
		}
		component = &Component{Component: declared}
		r.Reachable[id] = component
	}
	component.Page = component.Page || page
	for _, existing := range component.Routes {
		if existing == route {
			return
		}
	}
	component.Routes = append(component.Routes, route)
}

// callGraph is the KTML call graph: the components each component calls,
// in its body and Render Clauses.
func callGraph(snapshot *compiler.Snapshot) map[compiler.SymbolID][]compiler.SymbolID {
	edges := make(map[compiler.SymbolID]map[compiler.SymbolID]bool)
	for _, symbols := range snapshot.SymbolIndex(snapshot.Files()) {
		for _, reference := range symbols.References {
			if reference.Kind != symbolindex.CallReference || reference.From == "" {
				continue
			}
			if _, ok := snapshot.Component(reference.From); !ok {
				continue
			}
			if _, ok := snapshot.Component(reference.Symbol); !ok {
				continue
			}
			if edges[reference.From] == nil {
				edges[reference.From] = make(map[compiler.SymbolID]bool)
			}
			edges[reference.From][reference.Symbol] = true
		}
	}
	graph := make(map[compiler.SymbolID][]compiler.SymbolID, len(edges))
	for from, targets := range edges {
		for target := range targets {
			graph[from] = append(graph[from], target)
		}
		sort.Slice(graph[from], func(i, j int) bool { return graph[from][i] < graph[from][j] })
	}
	return graph
}

func siteKey(dir, goName string) string { return dir + "\x00" + goName }

func uriPath(uri string) string {
	if parsed, err := url.Parse(uri); err == nil && parsed.Scheme == "file" {
		return parsed.Path
	}
	return uri
}

// program is the module's Go code.
type program struct {
	root, path string
	fset       *token.FileSet
	packages   map[string]*goPackage // by import path
	files      map[string]*ast.File  // by absolute path
	// components maps a directory and generated Go name to its component.
	components map[string]compiler.SymbolID
	// nodes are the functions and package variables, with what each
	// references.
	nodes map[types.Object]*node
	// methods indexes the methods of module types by name.
	methods map[string][]*types.Func
}

type goPackage struct {
	dir   string
	path  string
	files []*ast.File
	types *types.Package
	info  *types.Info
	state int // 0 unchecked, 1 checking, 2 checked
}

// node is what one function, method, package variable, or handler
// expression references.
type node struct {
	refs  []types.Object
	sites []compiler.SymbolID
	// dispatch holds interface methods called, resolved when reached.
	dispatch []*types.Func
}

// load parses the module's handwritten, non-test Go files for the default
// build context and type-checks its packages.
func load(root, modulePath string) (*program, error) {
	p := &program{root: root, path: modulePath, fset: token.NewFileSet(), packages: make(map[string]*goPackage), files: make(map[string]*ast.File)}
	context := build.Default
	err := filepath.WalkDir(root, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if current != root {
			if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata" || name == "vendor" || name == "node_modules" {
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(current, "go.mod")); err == nil {
				return filepath.SkipDir
			}
		}
		entries, err := os.ReadDir(current)
		if err != nil {
			return err
		}
		var files []*ast.File
		counts := make(map[string]int)
		for _, file := range entries {
			fileName := file.Name()
			if file.IsDir() || !strings.HasSuffix(fileName, ".go") || strings.HasSuffix(fileName, "_test.go") || gopkg.Generated(fileName) {
				continue
			}
			if match, err := context.MatchFile(current, fileName); err != nil || !match {
				continue
			}
			full := filepath.Join(current, fileName)
			parsed, err := parser.ParseFile(p.fset, full, nil, parser.SkipObjectResolution)
			if parsed == nil {
				continue
			}
			_ = err // a file with syntax errors still contributes what parsed
			files = append(files, parsed)
			counts[parsed.Name.Name]++
		}
		if len(files) == 0 {
			return nil
		}
		best := ""
		for name, count := range counts {
			if best == "" || count > counts[best] || count == counts[best] && name < best {
				best = name
			}
		}
		relative, _ := filepath.Rel(root, current)
		importPath := modulePath
		if relative != "." {
			importPath = path.Join(modulePath, filepath.ToSlash(relative))
		}
		pkg := &goPackage{dir: current, path: importPath}
		for _, file := range files {
			if file.Name.Name == best {
				pkg.files = append(pkg.files, file)
				p.files[p.fset.File(file.Pos()).Name()] = file
			}
		}
		p.packages[importPath] = pkg
		return nil
	})
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(p.packages))
	for importPath := range p.packages {
		paths = append(paths, importPath)
	}
	sort.Strings(paths)
	for _, importPath := range paths {
		p.check(p.packages[importPath])
	}
	return p, nil
}

func (p *program) check(pkg *goPackage) *types.Package {
	switch pkg.state {
	case 1:
		// An import cycle; Go rejects it, so this is a broken module.
		return types.NewPackage(pkg.path, pkg.files[0].Name.Name)
	case 2:
		return pkg.types
	}
	pkg.state = 1
	pkg.info = &types.Info{
		Types:      make(map[ast.Expr]types.TypeAndValue),
		Defs:       make(map[*ast.Ident]types.Object),
		Uses:       make(map[*ast.Ident]types.Object),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
	}
	config := types.Config{
		Importer:    importer{p},
		Error:       func(error) {},
		FakeImportC: true,
	}
	// Errors are expected: packages outside the module are empty.
	pkg.types, _ = config.Check(pkg.path, p.fset, pkg.files, pkg.info)
	pkg.state = 2
	return pkg.types
}

type importer struct{ p *program }

func (i importer) Import(importPath string) (*types.Package, error) {
	if pkg, ok := i.p.packages[importPath]; ok {
		return i.p.check(pkg), nil
	}
	pkg := types.NewPackage(importPath, guessName(importPath))
	pkg.MarkComplete()
	return pkg, nil
}

// guessName is the usual package name of an import path.
func guessName(importPath string) string {
	parts := strings.Split(importPath, "/")
	name := parts[len(parts)-1]
	if len(parts) > 1 && len(name) > 1 && name[0] == 'v' {
		if _, err := strconv.Atoi(name[1:]); err == nil {
			name = parts[len(parts)-2]
		}
	}
	name = strings.TrimPrefix(name, "go-")
	name = strings.TrimSuffix(name, ".go")
	return strings.Map(func(r rune) rune {
		if r == '-' || r == '.' {
			return '_'
		}
		return r
	}, name)
}

// local reports whether an object belongs to a module package.
func (p *program) local(object types.Object) bool {
	if object == nil || object.Pkg() == nil {
		return false
	}
	_, ok := p.packages[object.Pkg().Path()]
	return ok
}

// build records what every function, method, and package variable of the
// module references.
func (p *program) build() {
	p.nodes = make(map[types.Object]*node)
	p.methods = make(map[string][]*types.Func)
	for _, pkg := range p.packages {
		for _, file := range pkg.files {
			for _, declaration := range file.Decls {
				switch declaration := declaration.(type) {
				case *ast.FuncDecl:
					object, ok := pkg.info.Defs[declaration.Name].(*types.Func)
					if !ok {
						continue
					}
					if declaration.Recv != nil {
						p.methods[object.Name()] = append(p.methods[object.Name()], object)
					}
					n := &node{}
					if declaration.Body != nil {
						p.collect(pkg, declaration.Body, n)
					}
					p.nodes[object] = n
				case *ast.GenDecl:
					if declaration.Tok != token.VAR {
						continue
					}
					for _, spec := range declaration.Specs {
						value, ok := spec.(*ast.ValueSpec)
						if !ok {
							continue
						}
						n := &node{}
						for _, expr := range value.Values {
							p.collect(pkg, expr, n)
						}
						for _, name := range value.Names {
							if object := pkg.info.Defs[name]; object != nil {
								p.nodes[object] = n
							}
						}
					}
				}
			}
		}
	}
}

// collect records the references in a function body or expression.
func (p *program) collect(pkg *goPackage, root ast.Node, n *node) {
	keys := make(map[*ast.Ident]bool)
	ast.Inspect(root, func(current ast.Node) bool {
		switch current := current.(type) {
		case *ast.CompositeLit:
			// Keys of a literal of an unknown type, such as a generated
			// component's View field, are not references.
			for _, element := range current.Elts {
				if pair, ok := element.(*ast.KeyValueExpr); ok {
					if key, ok := pair.Key.(*ast.Ident); ok {
						keys[key] = true
					}
				}
			}
		case *ast.SelectorExpr:
			if qualifier, ok := current.X.(*ast.Ident); ok {
				if name, ok := pkg.info.Uses[qualifier].(*types.PkgName); ok {
					if id, ok := p.component(name.Imported().Path(), current.Sel.Name); ok {
						n.sites = append(n.sites, id)
					}
					if object := pkg.info.Uses[current.Sel]; p.local(object) {
						n.refs = append(n.refs, origin(object))
					}
					return false
				}
			}
			if selection := pkg.info.Selections[current]; selection != nil {
				if method, ok := selection.Obj().(*types.Func); ok {
					if signature, ok := method.Type().(*types.Signature); ok && signature.Recv() != nil && types.IsInterface(signature.Recv().Type()) {
						n.dispatch = append(n.dispatch, method)
					} else if p.local(method) {
						n.refs = append(n.refs, origin(method))
					}
				}
			}
			p.collect(pkg, current.X, n)
			return false
		case *ast.Ident:
			p.visitIdent(pkg, current, n, keys)
		}
		return true
	})
}

// visitIdent records an identifier's reference: a module function or
// package variable, or a component of the package itself.
func (p *program) visitIdent(pkg *goPackage, ident *ast.Ident, n *node, keys map[*ast.Ident]bool) {
	if keys[ident] {
		return
	}
	object := pkg.info.Uses[ident]
	switch object := object.(type) {
	case *types.Func:
		if p.local(object) {
			n.refs = append(n.refs, origin(object))
		}
	case *types.Var:
		if p.local(object) && object.Parent() == object.Pkg().Scope() {
			n.refs = append(n.refs, object)
		}
	case nil:
		if pkg.info.Defs[ident] == nil {
			if id, ok := p.component(pkg.path, ident.Name); ok {
				n.sites = append(n.sites, id)
			}
		}
	}
}

func origin(object types.Object) types.Object {
	if function, ok := object.(*types.Func); ok {
		return function.Origin()
	}
	return object
}

// component resolves a generated component type of a module package.
func (p *program) component(importPath, name string) (compiler.SymbolID, bool) {
	pkg, ok := p.packages[importPath]
	var dir string
	if ok {
		dir = pkg.dir
	} else if importPath == p.path || strings.HasPrefix(importPath, p.path+"/") {
		// A package of only generated files.
		dir = filepath.Join(p.root, filepath.FromSlash(strings.TrimPrefix(strings.TrimPrefix(importPath, p.path), "/")))
	} else {
		return "", false
	}
	id, ok := p.components[siteKey(dir, name)]
	return id, ok
}

// handler finds a route's handler expression in the loaded files.
func (p *program) handler(route htmx.Route) (*node, bool) {
	if route.HandlerLocation == nil {
		return nil, false
	}
	file, ok := p.files[uriPath(route.HandlerLocation.URI)]
	if !ok {
		return nil, false
	}
	tokenFile := p.fset.File(file.Pos())
	position := func(line, character int) token.Pos {
		if line+1 > tokenFile.LineCount() {
			return token.NoPos
		}
		return tokenFile.LineStart(line+1) + token.Pos(character)
	}
	start := position(route.HandlerLocation.Range.Start.Line, route.HandlerLocation.Range.Start.Character)
	end := position(route.HandlerLocation.Range.End.Line, route.HandlerLocation.Range.End.Character)
	var found ast.Expr
	ast.Inspect(file, func(current ast.Node) bool {
		if current == nil || found != nil || current.End() < start || current.Pos() > end {
			return false
		}
		if expr, ok := current.(ast.Expr); ok && expr.Pos() == start && expr.End() == end {
			found = expr
			return false
		}
		return true
	})
	if found == nil {
		return nil, false
	}
	var pkg *goPackage
	for _, candidate := range p.packages {
		for _, candidateFile := range candidate.files {
			if candidateFile == file {
				pkg = candidate
			}
		}
	}
	if pkg == nil {
		return nil, false
	}
	n := &node{}
	p.collect(pkg, found, n)
	return n, true
}

// reach returns the components the Go code reachable from n constructs.
func (p *program) reach(start *node) []compiler.SymbolID {
	seen := make(map[types.Object]bool)
	components := make(map[compiler.SymbolID]bool)
	queue := []*node{start}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, id := range current.sites {
			components[id] = true
		}
		targets := append([]types.Object(nil), current.refs...)
		for _, method := range current.dispatch {
			targets = append(targets, p.implementations(method)...)
		}
		for _, target := range targets {
			if seen[target] {
				continue
			}
			seen[target] = true
			if next, ok := p.nodes[target]; ok {
				queue = append(queue, next)
			}
		}
	}
	ids := make([]compiler.SymbolID, 0, len(components))
	for id := range components {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// implementations are the module methods an interface method call may
// reach: methods of that name whose receiver type has every method name of
// the interface. Names, not signatures, are compared, since types from
// outside the module are unknown.
func (p *program) implementations(called *types.Func) []types.Object {
	iface, ok := called.Type().(*types.Signature).Recv().Type().Underlying().(*types.Interface)
	if !ok {
		return nil
	}
	name := called.Name()
	var result []types.Object
	for _, method := range p.methods[name] {
		signature, ok := method.Type().(*types.Signature)
		if !ok || signature.Recv() == nil {
			continue
		}
		receiver := signature.Recv().Type()
		if pointer, ok := receiver.(*types.Pointer); ok {
			receiver = pointer.Elem()
		}
		set := types.NewMethodSet(types.NewPointer(receiver))
		complete := true
		for index := 0; index < iface.NumMethods(); index++ {
			wanted := iface.Method(index)
			if set.Lookup(wanted.Pkg(), wanted.Name()) == nil {
				complete = false
				break
			}
		}
		if complete {
			result = append(result, method)
		}
	}
	return result
}
