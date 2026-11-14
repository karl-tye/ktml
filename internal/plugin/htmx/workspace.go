package htmx

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/constant"
	"go/parser"
	"go/printer"
	"go/scanner"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// goWorkspace loads the Go packages under chi.dir and keeps them between
// refreshes: unchanged files are not reparsed and packages whose files and
// in-directory dependencies are unchanged are not type-checked again.
type goWorkspace struct {
	settings ChiSettings
	dir      string
	fset     *token.FileSet
	files    map[string]*goFile
	packages map[string]*goPackage
	// external caches constant-only views of module packages outside dir.
	external map[string]*externalPackage
	module   moduleInfo
	parseErr []Diagnostic
}

type goFile struct {
	path    string
	modTime time.Time
	size    int64
	ast     *ast.File
	parse   *Diagnostic
	ignored bool
}

type goPackage struct {
	dir         string
	importPath  string
	files       []*goFile
	types       *types.Package
	info        *types.Info
	funcs       []*funcDecl
	byObject    map[*types.Func]*funcDecl
	workspace   *goWorkspace
	fingerprint string
}

type funcDecl struct {
	// unchecked bodies were left out of type-checking as unable to
	// register routes; analysis skips them.
	unchecked bool
	pkg       *goPackage
	decl      *ast.FuncDecl
	object    *types.Func
	receiver  types.Object
	params    []types.Object
}

func (fn *funcDecl) isParam(object types.Object) bool {
	for _, param := range fn.params {
		if param == object {
			return true
		}
	}
	return false
}

type moduleInfo struct {
	root string
	path string
}

type externalPackage struct {
	fingerprint string
	pkg         *types.Package
}

func newGoWorkspace(settings ChiSettings) *goWorkspace {
	return &goWorkspace{
		settings: settings,
		dir:      settings.Dir,
		fset:     token.NewFileSet(),
		files:    make(map[string]*goFile),
		packages: make(map[string]*goPackage),
		external: make(map[string]*externalPackage),
	}
}

// refresh rescans dir and reports whether any Go file changed since the last
// refresh. The index is rebuilt from cached packages when nothing changed.
func (workspace *goWorkspace) refresh() (RouteIndex, bool, error) {
	absDir, err := filepath.Abs(workspace.dir)
	if err != nil {
		return RouteIndex{}, false, fmt.Errorf("resolve Chi directory: %w", err)
	}
	info, err := os.Stat(absDir)
	if err != nil {
		return RouteIndex{}, false, fmt.Errorf("read Chi directory: %w", err)
	}
	if !info.IsDir() {
		return RouteIndex{}, false, fmt.Errorf("chi.dir %q is not a directory", workspace.dir)
	}
	workspace.dir = absDir
	if workspace.module.root == "" {
		workspace.module = findModule(absDir)
	}

	seen := make(map[string]bool)
	changed := false
	err = filepath.WalkDir(absDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			name := entry.Name()
			if path != absDir && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "vendor" || name == "testdata" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		seen[path] = true
		stat, err := entry.Info()
		if err != nil {
			return err
		}
		cached := workspace.files[path]
		if cached != nil && cached.modTime.Equal(stat.ModTime()) && cached.size == stat.Size() {
			return nil
		}
		changed = true
		workspace.files[path] = workspace.parseFile(path, stat)
		return nil
	})
	if err != nil {
		return RouteIndex{}, false, fmt.Errorf("scan Chi directory: %w", err)
	}
	for path := range workspace.files {
		if !seen[path] {
			delete(workspace.files, path)
			changed = true
		}
	}
	workspace.load()
	return workspace.analyze(), changed, nil
}

// refreshPaths re-parses only the given files, as reported by the editor's
// file watcher, then re-checks the packages they affect and recomposes the
// index. It falls back to a full scan before the first one.
func (workspace *goWorkspace) refreshPaths(paths []string) (RouteIndex, error) {
	if !filepath.IsAbs(workspace.dir) || len(workspace.files) == 0 && len(workspace.packages) == 0 {
		index, _, err := workspace.refresh()
		return index, err
	}
	for _, path := range paths {
		if !workspace.indexable(path) {
			continue
		}
		stat, err := os.Stat(path)
		if err != nil {
			delete(workspace.files, path)
			continue
		}
		cached := workspace.files[path]
		if cached != nil && cached.modTime.Equal(stat.ModTime()) && cached.size == stat.Size() {
			continue
		}
		workspace.files[path] = workspace.parseFile(path, stat)
	}
	workspace.load()
	return workspace.analyze(), nil
}

// indexable reports whether a refresh would index path: a non-test Go file
// under dir outside skipped directories.
func (workspace *goWorkspace) indexable(path string) bool {
	relative, err := filepath.Rel(workspace.dir, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false
	}
	if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
		return false
	}
	parts := strings.Split(filepath.ToSlash(relative), "/")
	for _, name := range parts[:len(parts)-1] {
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "vendor" || name == "testdata" || name == "node_modules" {
			return false
		}
	}
	return true
}

func (workspace *goWorkspace) parseFile(path string, stat fs.FileInfo) *goFile {
	file := &goFile{path: path, modTime: stat.ModTime(), size: stat.Size()}
	source, err := os.ReadFile(path)
	if err != nil {
		file.ignored = true
		file.parse = &Diagnostic{Code: "go-read", Message: err.Error(), Location: Location{URI: fileURI(path)}}
		return file
	}
	if !buildConstraintsSatisfied(path, source) {
		file.ignored = true
		return file
	}
	parsed, err := parser.ParseFile(workspace.fset, path, source, parser.AllErrors|parser.ParseComments)
	file.ast = parsed
	if err != nil {
		location := Location{URI: fileURI(path)}
		if list, ok := err.(scanner.ErrorList); ok && len(list) > 0 {
			location.Range.Start = Position{Line: max(0, list[0].Pos.Line-1), Character: max(0, list[0].Pos.Column-1)}
			location.Range.End = location.Range.Start
		}
		file.parse = &Diagnostic{Code: "go-parse", Message: err.Error(), Location: location}
	}
	if parsed == nil || parsed.Name == nil {
		file.ignored = true
	}
	return file
}

// buildConstraintsSatisfied evaluates //go:build lines and GOOS file-name
// suffixes for a production linux build, so ktmldev variants and
// other-platform files do not collide with the files they replace.
func buildConstraintsSatisfied(path string, source []byte) bool {
	name := strings.TrimSuffix(filepath.Base(path), ".go")
	for _, goos := range []string{"windows", "darwin", "freebsd", "openbsd", "netbsd", "plan9", "solaris", "js", "wasip1", "android", "ios", "aix", "dragonfly", "illumos"} {
		if strings.HasSuffix(name, "_"+goos) || strings.Contains(name, "_"+goos+"_") {
			return false
		}
	}
	for _, line := range bytes.Split(source, []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 || bytes.HasPrefix(trimmed, []byte("//")) && !constraint.IsGoBuild(string(trimmed)) {
			continue
		}
		if !constraint.IsGoBuild(string(trimmed)) {
			break
		}
		expr, err := constraint.Parse(string(trimmed))
		if err != nil {
			return true
		}
		return expr.Eval(func(tag string) bool {
			switch tag {
			case "linux", "unix", "amd64", "gc", "cgo":
				return true
			}
			return strings.HasPrefix(tag, "go1.")
		})
	}
	return true
}

func findModule(dir string) moduleInfo {
	for current := dir; ; {
		content, err := os.ReadFile(filepath.Join(current, "go.mod"))
		if err == nil {
			for _, line := range strings.Split(string(content), "\n") {
				fields := strings.Fields(line)
				if len(fields) >= 2 && fields[0] == "module" {
					path, err := strconv.Unquote(fields[1])
					if err != nil {
						path = fields[1]
					}
					return moduleInfo{root: current, path: path}
				}
			}
			return moduleInfo{}
		}
		parent := filepath.Dir(current)
		if parent == current {
			return moduleInfo{}
		}
		current = parent
	}
}

// load groups files into packages and type-checks the packages that changed.
func (workspace *goWorkspace) load() {
	byDir := make(map[string][]*goFile)
	workspace.parseErr = workspace.parseErr[:0]
	for _, file := range workspace.files {
		if file.parse != nil {
			workspace.parseErr = append(workspace.parseErr, *file.parse)
		}
		if file.ignored {
			continue
		}
		byDir[filepath.Dir(file.path)] = append(byDir[filepath.Dir(file.path)], file)
	}
	for dir := range workspace.packages {
		if _, ok := byDir[dir]; !ok {
			delete(workspace.packages, dir)
		}
	}
	for dir, files := range byDir {
		files = majorityPackage(files)
		sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
		fingerprint := filesFingerprint(files)
		pkg := workspace.packages[dir]
		if pkg == nil || pkg.fingerprint != fingerprint {
			pkg = &goPackage{dir: dir, files: files, workspace: workspace, fingerprint: fingerprint}
			workspace.packages[dir] = pkg
		}
		pkg.importPath = workspace.importPathFor(dir)
	}

	// Re-check a package when it or anything it imports from dir changed.
	checked := make(map[string]bool)
	var check func(pkg *goPackage, stack map[string]bool) bool
	check = func(pkg *goPackage, stack map[string]bool) bool {
		if done, ok := checked[pkg.dir]; ok {
			return done
		}
		stack[pkg.dir] = true
		dirty := pkg.types == nil
		for _, dependency := range workspace.localImports(pkg) {
			if stack[dependency.dir] {
				continue
			}
			if check(dependency, stack) {
				dirty = true
			}
		}
		delete(stack, pkg.dir)
		if dirty {
			workspace.typeCheck(pkg)
		}
		checked[pkg.dir] = dirty
		return dirty
	}
	for _, pkg := range workspace.orderedPackages() {
		check(pkg, make(map[string]bool))
	}
}

func majorityPackage(files []*goFile) []*goFile {
	counts := make(map[string]int)
	for _, file := range files {
		counts[file.ast.Name.Name]++
	}
	best := ""
	for name, count := range counts {
		if best == "" || count > counts[best] || count == counts[best] && name < best {
			best = name
		}
	}
	selected := files[:0:0]
	for _, file := range files {
		if file.ast.Name.Name == best {
			selected = append(selected, file)
		}
	}
	return selected
}

func filesFingerprint(files []*goFile) string {
	var builder strings.Builder
	for _, file := range files {
		fmt.Fprintf(&builder, "%s:%d:%d;", file.path, file.modTime.UnixNano(), file.size)
	}
	return builder.String()
}

func (workspace *goWorkspace) importPathFor(dir string) string {
	if workspace.module.root == "" {
		return ""
	}
	relative, err := filepath.Rel(workspace.module.root, dir)
	if err != nil || strings.HasPrefix(relative, "..") {
		return ""
	}
	if relative == "." {
		return workspace.module.path
	}
	return workspace.module.path + "/" + filepath.ToSlash(relative)
}

// localPackage resolves an import path to a package under dir: by module
// path when go.mod is readable, otherwise by matching the directory suffix.
func (workspace *goWorkspace) localPackage(importPath string) *goPackage {
	var suffixMatch *goPackage
	for _, pkg := range workspace.packages {
		if pkg.importPath != "" {
			if pkg.importPath == importPath {
				return pkg
			}
			continue
		}
		relative, err := filepath.Rel(workspace.dir, pkg.dir)
		if err != nil || relative == "." {
			continue
		}
		if strings.HasSuffix(importPath, "/"+filepath.ToSlash(relative)) {
			suffixMatch = pkg
		}
	}
	return suffixMatch
}

func (workspace *goWorkspace) localImports(pkg *goPackage) []*goPackage {
	var imports []*goPackage
	for _, file := range pkg.files {
		for _, spec := range file.ast.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				continue
			}
			if dependency := workspace.localPackage(path); dependency != nil && dependency != pkg {
				imports = append(imports, dependency)
			}
		}
	}
	return imports
}

func (workspace *goWorkspace) orderedPackages() []*goPackage {
	packages := make([]*goPackage, 0, len(workspace.packages))
	for _, pkg := range workspace.packages {
		packages = append(packages, pkg)
	}
	sort.Slice(packages, func(i, j int) bool { return packages[i].dir < packages[j].dir })
	return packages
}

func (workspace *goWorkspace) typeCheck(pkg *goPackage) {
	files := make([]*ast.File, len(pkg.files))
	for i, file := range pkg.files {
		files[i] = file.ast
	}
	pkg.info = &types.Info{
		Types:      make(map[ast.Expr]types.TypeAndValue),
		Defs:       make(map[*ast.Ident]types.Object),
		Uses:       make(map[*ast.Ident]types.Object),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
	}
	path := pkg.importPath
	if path == "" {
		path = "ktml.invalid/" + filepath.ToSlash(pkg.dir)
	}
	config := types.Config{
		Importer:    workspaceImporter{workspace: workspace, from: pkg},
		Error:       func(error) {},
		FakeImportC: true,
	}
	// Bodies that cannot register routes are left out of the check and
	// restored afterwards: handlers and templates dominate packages.
	relevant := relevantFuncs(files, workspace.settings.Patterns)
	stripped := make(map[*ast.FuncDecl]*ast.BlockStmt)
	for _, file := range files {
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil && !relevant[fn] {
				stripped[fn] = fn.Body
				fn.Body = nil
			}
		}
	}
	// Errors are expected: imports outside the sandbox resolve to empty
	// packages, and only routing calls and constants need to type-check.
	pkg.types, _ = config.Check(path, workspace.fset, files, pkg.info)
	for fn, body := range stripped {
		fn.Body = body
	}
	pkg.funcs = nil
	pkg.byObject = make(map[*types.Func]*funcDecl)
	for _, file := range files {
		for _, decl := range file.Decls {
			fnDecl, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			object, _ := pkg.info.Defs[fnDecl.Name].(*types.Func)
			if object == nil {
				continue
			}
			fn := &funcDecl{pkg: pkg, decl: fnDecl, object: object, unchecked: stripped[fnDecl] != nil}
			if fnDecl.Recv != nil && len(fnDecl.Recv.List) > 0 && len(fnDecl.Recv.List[0].Names) > 0 {
				fn.receiver = pkg.info.Defs[fnDecl.Recv.List[0].Names[0]]
			}
			for _, field := range fnDecl.Type.Params.List {
				if len(field.Names) == 0 {
					fn.params = append(fn.params, nil)
				}
				for _, name := range field.Names {
					fn.params = append(fn.params, pkg.info.Defs[name])
				}
			}
			pkg.funcs = append(pkg.funcs, fn)
			pkg.byObject[object] = fn
		}
	}
}

// workspaceImporter serves Chi from the stub, packages under dir from their
// own type-check, other module packages as constant-only views, and anything
// else as an empty package.
type workspaceImporter struct {
	workspace *goWorkspace
	from      *goPackage
}

func (importer workspaceImporter) Import(path string) (*types.Package, error) {
	if chiImportPaths[path] {
		stub, _ := chiStub()
		return stub, nil
	}
	if pkg := importer.workspace.localPackage(path); pkg != nil && pkg != importer.from && pkg.types != nil {
		return pkg.types, nil
	}
	if pkg := importer.workspace.externalConstants(path); pkg != nil {
		return pkg, nil
	}
	pkg := types.NewPackage(path, packageNameGuess(path))
	pkg.MarkComplete()
	return pkg, nil
}

// externalConstants returns the string constants of a module package outside
// dir, when go.mod and the package directory are readable. Route paths often
// come from such constants.
func (workspace *goWorkspace) externalConstants(importPath string) *types.Package {
	module := workspace.module
	if module.root == "" || (importPath != module.path && !strings.HasPrefix(importPath, module.path+"/")) {
		return nil
	}
	dir := filepath.Join(module.root, filepath.FromSlash(strings.TrimPrefix(strings.TrimPrefix(importPath, module.path), "/")))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var fingerprint strings.Builder
	var paths []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		fmt.Fprintf(&fingerprint, "%s:%d:%d;", name, info.ModTime().UnixNano(), info.Size())
		paths = append(paths, filepath.Join(dir, name))
	}
	if cached := workspace.external[importPath]; cached != nil && cached.fingerprint == fingerprint.String() {
		return cached.pkg
	}
	values := make(map[string]string)
	var specs []*ast.ValueSpec
	name := ""
	for _, path := range paths {
		source, err := os.ReadFile(path)
		if err != nil || !buildConstraintsSatisfied(path, source) {
			continue
		}
		if name == "" {
			if clause, err := parser.ParseFile(token.NewFileSet(), path, source, parser.PackageClauseOnly); err == nil {
				name = clause.Name.Name
			}
		}
		// Only const declarations are parsed; each on its own, so one
		// unparsable declaration does not hide the others.
		for _, decl := range constDeclSources(source) {
			file, err := parser.ParseFile(token.NewFileSet(), "", append([]byte("package p\n"), decl...), parser.SkipObjectResolution)
			if err != nil {
				continue
			}
			for _, decl := range file.Decls {
				if gen, ok := decl.(*ast.GenDecl); ok {
					for _, spec := range gen.Specs {
						if valueSpec, ok := spec.(*ast.ValueSpec); ok {
							specs = append(specs, valueSpec)
						}
					}
				}
			}
		}
	}
	// Constants may refer to each other in any order.
	for range 4 {
		for _, spec := range specs {
			for i, ident := range spec.Names {
				if i >= len(spec.Values) {
					continue
				}
				if value, ok := evalStringConstant(spec.Values[i], values); ok {
					values[ident.Name] = value
				}
			}
		}
	}
	if name == "" {
		name = packageNameGuess(importPath)
	}
	pkg := types.NewPackage(importPath, name)
	for constName, value := range values {
		if ast.IsExported(constName) {
			pkg.Scope().Insert(types.NewConst(token.NoPos, pkg, constName, types.Typ[types.UntypedString], constant.MakeString(value)))
		}
	}
	pkg.MarkComplete()
	workspace.external[importPath] = &externalPackage{fingerprint: fingerprint.String(), pkg: pkg}
	return pkg
}

func evalStringConstant(expr ast.Expr, values map[string]string) (string, bool) {
	switch expr := expr.(type) {
	case *ast.BasicLit:
		if expr.Kind != token.STRING {
			return "", false
		}
		value, err := strconv.Unquote(expr.Value)
		return value, err == nil
	case *ast.Ident:
		value, ok := values[expr.Name]
		return value, ok
	case *ast.ParenExpr:
		return evalStringConstant(expr.X, values)
	case *ast.BinaryExpr:
		if expr.Op != token.ADD {
			return "", false
		}
		left, leftOK := evalStringConstant(expr.X, values)
		right, rightOK := evalStringConstant(expr.Y, values)
		return left + right, leftOK && rightOK
	case *ast.CallExpr:
		// A typed constant conversion such as Path("/x").
		if len(expr.Args) == 1 {
			return evalStringConstant(expr.Args[0], values)
		}
	}
	return "", false
}

// callee resolves a call's function expression to a declaration under dir.
func (workspace *goWorkspace) callee(fun ast.Expr, info *types.Info) *funcDecl {
	return workspace.funcValue(fun, info)
}

// funcValue resolves a function or method value, such as handler.Routes, to
// its declaration under dir.
func (workspace *goWorkspace) funcValue(expr ast.Expr, info *types.Info) *funcDecl {
	var object types.Object
	switch e := unparen(expr).(type) {
	case *ast.Ident:
		object = info.Uses[e]
	case *ast.SelectorExpr:
		if selection := info.Selections[e]; selection != nil {
			object = selection.Obj()
		} else {
			object = info.Uses[e.Sel]
		}
	case *ast.IndexExpr:
		return workspace.funcValue(e.X, info)
	}
	fn, ok := object.(*types.Func)
	if !ok {
		return nil
	}
	fn = fn.Origin()
	for _, pkg := range workspace.packages {
		if pkg.types != nil && fn.Pkg() == pkg.types {
			return pkg.byObject[fn]
		}
	}
	return nil
}

func (pkg *goPackage) location(node ast.Node) Location {
	fset := pkg.workspace.fset
	start := fset.Position(node.Pos())
	end := fset.Position(node.End())
	return Location{
		URI: fileURI(start.Filename),
		Range: Range{
			Start: Position{Line: max(0, start.Line-1), Character: max(0, start.Column-1)},
			End:   Position{Line: max(0, end.Line-1), Character: max(0, end.Column-1)},
		},
	}
}

func exprText(expr ast.Expr, pkg *goPackage) string {
	var value strings.Builder
	if err := printer.Fprint(&value, pkg.workspace.fset, expr); err != nil {
		return ""
	}
	return value.String()
}

// analyze runs route analysis over the loaded packages.
func (workspace *goWorkspace) analyze() RouteIndex {
	analysis := &routeAnalysis{
		workspace:    workspace,
		patterns:     workspace.settings.Patterns,
		routes:       make(map[routeKey]Route),
		diagnostics:  make(map[diagnosticKey]Diagnostic),
		memo:         make(map[string]bool),
		active:       make(map[string]bool),
		wrappers:     make(map[*types.Func]bool),
		wrapperTypes: make(map[*types.TypeName]bool),
		reached:      make(map[*types.Func]bool),
	}
	index := analysis.run()
	index.Diagnostics = append(index.Diagnostics, workspace.parseErr...)
	sort.SliceStable(index.Diagnostics, func(i, j int) bool {
		return index.Diagnostics[i].Location.URI < index.Diagnostics[j].Location.URI
	})
	return index
}
