package htmx

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Route is one entry of the route index: a method and effective path, its
// parameters, the handler expression, and where it is registered.
// Registration is the path argument and Declaration the whole call.
type Route struct {
	Method       string   `json:"method"`
	Path         string   `json:"path"`
	Params       []string `json:"params,omitempty"`
	Handler      string   `json:"handler"`
	Registration Location `json:"registration"`
	Declaration  Location `json:"declaration"`
	// HandlerLocation is the handler expression, for tools that follow it
	// into Go, such as story generation's reachability.
	HandlerLocation *Location `json:"handlerLocation,omitempty"`

	// Prefix is the composed Route/Mount prefix and Literal the registered
	// pattern; Path joins them.
	Prefix  string `json:"prefix,omitempty"`
	Literal string `json:"literal,omitempty"`
	// SlashOptional marks a "/" registered under a prefix, which Chi serves
	// with and without the trailing slash.
	SlashOptional bool `json:"slashOptional,omitempty"`
	// Subtree marks an opaque Mount: a handler serving everything below Path.
	Subtree bool `json:"subtree,omitempty"`
	// Editable reports whether a rename can rewrite this registration: its
	// pattern is a string literal reached under one prefix only.
	Editable   bool `json:"editable,omitempty"`
	LiteralRaw bool `json:"literalRaw,omitempty"`
}

type Diagnostic struct {
	Code     string   `json:"code"`
	Message  string   `json:"message"`
	Location Location `json:"location"`
}

type RouteIndex struct {
	Routes      []Route      `json:"routes"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}

// DiscoverRoutes indexes the Chi routes under dir with the built-in matchers.
func DiscoverRoutes(dir string) (RouteIndex, error) {
	return DiscoverRoutesWith(ChiSettings{Dir: dir, Patterns: Patterns{Defaults: true}})
}

// DiscoverRoutesWith indexes the Chi routes under settings.Dir with the
// built-in matchers and any configured patterns.
func DiscoverRoutesWith(settings ChiSettings) (RouteIndex, error) {
	workspace := newGoWorkspace(settings)
	index, _, err := workspace.refresh()
	return index, err
}

var chiRouteMethods = map[string]string{
	"Connect": "CONNECT",
	"Delete":  "DELETE",
	"Get":     "GET",
	"Head":    "HEAD",
	"Options": "OPTIONS",
	"Patch":   "PATCH",
	"Post":    "POST",
	"Put":     "PUT",
	"Trace":   "TRACE",
}

// prefixValue is the composed prefix a router value registers under. known
// is false below a prefix that could not be evaluated.
type prefixValue struct {
	prefix string
	known  bool
}

var rootPrefix = prefixValue{known: true}

func (value prefixValue) join(segment string) prefixValue {
	if !value.known {
		return value
	}
	joined, _ := joinRoute(value.prefix, segment)
	return prefixValue{prefix: joined, known: true}
}

type routeAnalysis struct {
	workspace   *goWorkspace
	patterns    Patterns
	routes      map[routeKey]Route
	diagnostics map[diagnosticKey]Diagnostic
	memo        map[string]bool
	active      map[string]bool
	// wrappers are functions called through a user pattern: their bodies
	// are the custom wrapper itself and never registration roots.
	wrappers map[*types.Func]bool
	// wrapperTypes are the named types captured as {router} by user patterns.
	wrapperTypes map[*types.TypeName]bool
	reached      map[*types.Func]bool
}

type routeKey struct {
	method, path, uri string
	offset            int
}

type diagnosticKey struct {
	code, uri string
	line, col int
}

type analysisContext struct {
	pkg *goPackage
	fn  *funcDecl
	env map[types.Object]prefixValue
}

func (ctx analysisContext) info() *types.Info { return ctx.pkg.info }

func (analysis *routeAnalysis) run() RouteIndex {
	analysis.prepass()
	for _, pkg := range analysis.workspace.orderedPackages() {
		for _, fn := range pkg.funcs {
			if fn.decl.Body == nil || fn.unchecked || analysis.reached[fn.object] || analysis.wrappers[fn.object] {
				continue
			}
			analysis.analyzeFunc(fn, nil, nil)
		}
	}
	return analysis.index()
}

// prepass finds the functions reached through router-propagating calls and
// the wrapper functions behind user patterns. Unreached functions are the
// registration roots, analysed with every router at the root prefix.
func (analysis *routeAnalysis) prepass() {
	for _, pkg := range analysis.workspace.orderedPackages() {
		for _, file := range pkg.files {
			ast.Inspect(file.ast, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				ctx := analysisContext{pkg: pkg}
				if pattern, captures := analysis.matchUserPattern(call, pkg.info); pattern != nil {
					if callee := analysis.workspace.callee(call.Fun, pkg.info); callee != nil {
						analysis.wrappers[callee.object] = true
					}
					if router := captures[captureRouter]; router != nil {
						if named := namedType(pkg.info.TypeOf(router)); named != nil {
							analysis.wrapperTypes[named.Obj()] = true
						}
					}
					if routes := captures[captureRoutes]; routes != nil {
						analysis.markReached(routes, pkg.info)
					}
					if handler := captures[captureHandler]; handler != nil && pattern.Kind == mountPattern {
						analysis.markReached(handler, pkg.info)
					}
					return true
				}
				if name, ok := analysis.chiMethod(call, ctx); ok {
					switch name {
					case "Route":
						if len(call.Args) == 2 {
							analysis.markReached(call.Args[1], pkg.info)
						}
					case "Group":
						if len(call.Args) == 1 {
							analysis.markReached(call.Args[0], pkg.info)
						}
					case "Mount":
						if len(call.Args) == 2 {
							analysis.markReached(call.Args[1], pkg.info)
						}
					}
				}
				return true
			})
		}
	}
	// Router-typed arguments and receivers are only known once wrapper
	// types are, so calls are classified in a second pass.
	for _, pkg := range analysis.workspace.orderedPackages() {
		for _, file := range pkg.files {
			ast.Inspect(file.ast, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				callee := analysis.workspace.callee(call.Fun, pkg.info)
				if callee == nil || analysis.wrappers[callee.object] {
					return true
				}
				if analysis.passesRouter(call, pkg.info) {
					analysis.reached[callee.object] = true
				}
				return true
			})
		}
	}
}

func (analysis *routeAnalysis) markReached(expr ast.Expr, info *types.Info) {
	switch expr := unparen(expr).(type) {
	case *ast.FuncLit:
		return
	case *ast.CallExpr:
		if callee := analysis.workspace.callee(expr.Fun, info); callee != nil {
			analysis.reached[callee.object] = true
		}
	default:
		if fn := analysis.workspace.funcValue(expr, info); fn != nil {
			analysis.reached[fn.object] = true
		}
	}
}

func (analysis *routeAnalysis) passesRouter(call *ast.CallExpr, info *types.Info) bool {
	if selector, ok := unparen(call.Fun).(*ast.SelectorExpr); ok {
		if selection := info.Selections[selector]; selection != nil && selection.Kind() == types.MethodVal && analysis.routerType(info.TypeOf(selector.X)) {
			return true
		}
	}
	for _, arg := range call.Args {
		if analysis.routerType(info.TypeOf(arg)) {
			return true
		}
	}
	return false
}

func (analysis *routeAnalysis) routerType(typ types.Type) bool {
	if isRouterType(typ) {
		return true
	}
	if named := namedType(typ); named != nil {
		return analysis.wrapperTypes[named.Obj()]
	}
	return false
}

func namedType(typ types.Type) *types.Named {
	if typ == nil {
		return nil
	}
	if pointer, ok := typ.(*types.Pointer); ok {
		typ = pointer.Elem()
	}
	named, _ := typ.(*types.Named)
	return named
}

// analyzeFunc walks fn's body with its router parameters bound. A function
// is analysed once per distinct binding.
func (analysis *routeAnalysis) analyzeFunc(fn *funcDecl, bindings map[types.Object]prefixValue, returned *prefixValue) {
	if fn.unchecked {
		return
	}
	key := memoKey(fn, bindings, returned)
	if analysis.memo[key] || analysis.active[key] || len(analysis.active) > 64 {
		return
	}
	analysis.memo[key] = true
	analysis.active[key] = true
	defer delete(analysis.active, key)

	env := make(map[types.Object]prefixValue, len(bindings))
	for object, value := range bindings {
		env[object] = value
	}
	ctx := analysisContext{pkg: fn.pkg, fn: fn, env: env}
	if returned != nil {
		ast.Inspect(fn.decl.Body, func(node ast.Node) bool {
			if _, ok := node.(*ast.FuncLit); ok {
				return false
			}
			if ret, ok := node.(*ast.ReturnStmt); ok {
				for _, result := range ret.Results {
					if object := objectOf(result, ctx.info()); object != nil {
						env[object] = *returned
					}
				}
			}
			return true
		})
	}
	analysis.walkBody(fn.decl.Body, ctx)
}

func memoKey(fn *funcDecl, bindings map[types.Object]prefixValue, returned *prefixValue) string {
	parts := make([]string, 0, len(bindings)+2)
	parts = append(parts, fmt.Sprintf("%p", fn))
	for object, value := range bindings {
		parts = append(parts, fmt.Sprintf("%d=%t:%s", object.Pos(), value.known, value.prefix))
	}
	if returned != nil {
		parts = append(parts, fmt.Sprintf("return=%t:%s", returned.known, returned.prefix))
	}
	sort.Strings(parts[1:])
	return strings.Join(parts, "|")
}

// walkBody visits a body, first binding routers mounted by name.
func (analysis *routeAnalysis) walkBody(body ast.Node, ctx analysisContext) {
	analysis.bindMounts(body, ctx)
	analysis.walk(body, ctx)
}

// bindMounts binds each router mounted by name, as in r.Mount("/api", api),
// to its mount prefix before the body's registrations are visited.
func (analysis *routeAnalysis) bindMounts(body ast.Node, ctx analysisContext) {
	for range 4 {
		changed := false
		ast.Inspect(body, func(node ast.Node) bool {
			if _, ok := node.(*ast.FuncLit); ok && node != body {
				return false
			}
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			router, prefixExpr, handler, ok := analysis.mountParts(call, ctx)
			if !ok {
				return true
			}
			object := objectOf(handler, ctx.info())
			if object == nil || !analysis.routerType(object.Type()) {
				return true
			}
			value := analysis.prefixOf(router, ctx)
			if prefix, ok := constantString(prefixExpr, ctx.info()); ok {
				value = value.join(prefix)
			} else {
				value = prefixValue{}
			}
			if current, ok := ctx.env[object]; !ok || current != value {
				ctx.env[object] = value
				changed = true
			}
			return true
		})
		if !changed {
			return
		}
	}
}

func (analysis *routeAnalysis) mountParts(call *ast.CallExpr, ctx analysisContext) (router, prefix, handler ast.Expr, ok bool) {
	for _, pattern := range analysis.patterns.Mount {
		if captures, matched := pattern.match(call, ctx.info()); matched {
			handler := captures[captureRoutes]
			if handler == nil {
				handler = captures[captureHandler]
			}
			return captures[captureRouter], captures[capturePrefix], handler, true
		}
	}
	if !analysis.patterns.Defaults {
		return nil, nil, nil, false
	}
	if name, isChi := analysis.chiMethod(call, ctx); isChi && name == "Mount" && len(call.Args) == 2 {
		return call.Fun.(*ast.SelectorExpr).X, call.Args[0], call.Args[1], true
	}
	return nil, nil, nil, false
}

func (analysis *routeAnalysis) walk(root ast.Node, ctx analysisContext) {
	ast.Inspect(root, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		return !analysis.handleCall(call, ctx)
	})
}

// walkExprs walks expressions that a handled call does not consume itself.
func (analysis *routeAnalysis) walkExprs(ctx analysisContext, exprs ...ast.Expr) {
	for _, expr := range exprs {
		if expr != nil {
			analysis.walk(expr, ctx)
		}
	}
}

// handleCall processes one call and reports whether it walked the call's
// operands itself.
func (analysis *routeAnalysis) handleCall(call *ast.CallExpr, ctx analysisContext) bool {
	info := ctx.info()
	if pattern, captures := analysis.matchUserPattern(call, info); pattern != nil {
		switch pattern.Kind {
		case routePattern:
			analysis.register(pattern.Method, captures[captureRouter], captures[captureRoute], captures[captureHandler], call, ctx)
			analysis.walkExprs(ctx, captures[captureRouter], captures[captureHandler])
		case scopePattern:
			analysis.scope(captures[captureRouter], captures[capturePrefix], captures[captureRoutes], call, ctx)
			analysis.walkExprs(ctx, captures[captureRouter])
		case mountPattern:
			handler := captures[captureRoutes]
			if handler == nil {
				handler = captures[captureHandler]
			}
			analysis.mount(captures[captureRouter], captures[capturePrefix], handler, call, ctx)
			analysis.walkExprs(ctx, captures[captureRouter])
		}
		return true
	}

	if name, ok := analysis.chiMethod(call, ctx); ok {
		if !analysis.patterns.Defaults {
			return false
		}
		router := unparen(call.Fun).(*ast.SelectorExpr).X
		switch {
		case chiRouteMethods[name] != "" && len(call.Args) == 2:
			analysis.register(chiRouteMethods[name], router, call.Args[0], call.Args[1], call, ctx)
			analysis.walkExprs(ctx, router, call.Args[1])
			return true
		case (name == "Handle" || name == "HandleFunc") && len(call.Args) == 2:
			analysis.register("*", router, call.Args[0], call.Args[1], call, ctx)
			analysis.walkExprs(ctx, router, call.Args[1])
			return true
		case (name == "Method" || name == "MethodFunc") && len(call.Args) == 3:
			method, ok := constantString(call.Args[0], info)
			if !ok {
				analysis.diagnose("dynamic-method", "Chi route method must be a compile-time string for HTMX analysis", call.Args[0], ctx)
			} else {
				analysis.register(strings.ToUpper(method), router, call.Args[1], call.Args[2], call, ctx)
			}
			analysis.walkExprs(ctx, router, call.Args[2])
			return true
		case name == "Route" && len(call.Args) == 2:
			analysis.scope(router, call.Args[0], call.Args[1], call, ctx)
			analysis.walkExprs(ctx, router)
			return true
		case name == "Group" && len(call.Args) == 1:
			analysis.scope(router, nil, call.Args[0], call, ctx)
			analysis.walkExprs(ctx, router)
			return true
		case name == "Mount" && len(call.Args) == 2:
			analysis.mount(router, call.Args[0], call.Args[1], call, ctx)
			analysis.walkExprs(ctx, router)
			return true
		}
		return false
	}

	analysis.followCall(call, ctx)
	return false
}

// followCall analyses a package function that receives a router, as in
// mux.AddAuthentication(...) or registerAPI(r), with that router's prefix.
func (analysis *routeAnalysis) followCall(call *ast.CallExpr, ctx analysisContext) {
	callee := analysis.workspace.callee(call.Fun, ctx.info())
	if callee == nil || callee.decl.Body == nil || analysis.wrappers[callee.object] {
		return
	}
	bindings := make(map[types.Object]prefixValue)
	if selector, ok := unparen(call.Fun).(*ast.SelectorExpr); ok && callee.receiver != nil {
		if selection := ctx.info().Selections[selector]; selection != nil && selection.Kind() == types.MethodVal && analysis.routerish(selector.X, ctx) {
			bindings[callee.receiver] = analysis.prefixOf(selector.X, ctx)
		}
	}
	for i, arg := range call.Args {
		if i >= len(callee.params) || callee.params[i] == nil || !analysis.routerish(arg, ctx) {
			continue
		}
		bindings[callee.params[i]] = analysis.prefixOf(arg, ctx)
	}
	if len(bindings) > 0 {
		analysis.analyzeFunc(callee, bindings, nil)
	}
}

func (analysis *routeAnalysis) routerish(expr ast.Expr, ctx analysisContext) bool {
	if analysis.routerType(ctx.info().TypeOf(expr)) {
		return true
	}
	if object := objectOf(expr, ctx.info()); object != nil {
		_, bound := ctx.env[object]
		return bound
	}
	return false
}

func (analysis *routeAnalysis) chiMethod(call *ast.CallExpr, ctx analysisContext) (string, bool) {
	selector, ok := unparen(call.Fun).(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	selection := ctx.info().Selections[selector]
	if selection == nil || selection.Kind() != types.MethodVal || !isChiMethod(selection.Obj()) {
		return "", false
	}
	return selection.Obj().Name(), true
}

func (analysis *routeAnalysis) matchUserPattern(call *ast.CallExpr, info *types.Info) (*Pattern, map[string]ast.Expr) {
	for _, method := range sortedPatternMethods(analysis.patterns.Routes) {
		for _, pattern := range analysis.patterns.Routes[method] {
			if captures, ok := pattern.match(call, info); ok {
				return pattern, captures
			}
		}
	}
	for _, pattern := range analysis.patterns.Scope {
		if captures, ok := pattern.match(call, info); ok {
			return pattern, captures
		}
	}
	for _, pattern := range analysis.patterns.Mount {
		if captures, ok := pattern.match(call, info); ok {
			return pattern, captures
		}
	}
	return nil, nil
}

func sortedPatternMethods(routes map[string][]*Pattern) []string {
	methods := make([]string, 0, len(routes))
	for method := range routes {
		methods = append(methods, method)
	}
	sort.Strings(methods)
	return methods
}

// prefixOf evaluates the prefix a router expression registers under.
func (analysis *routeAnalysis) prefixOf(expr ast.Expr, ctx analysisContext) prefixValue {
	info := ctx.info()
	switch e := unparen(expr).(type) {
	case *ast.Ident:
		if object := objectOf(e, info); object != nil {
			if value, ok := ctx.env[object]; ok {
				return value
			}
		}
	case *ast.StarExpr:
		return analysis.prefixOf(e.X, ctx)
	case *ast.UnaryExpr:
		return analysis.prefixOf(e.X, ctx)
	case *ast.SelectorExpr:
		if object := objectOf(e, info); object != nil {
			if value, ok := ctx.env[object]; ok {
				return value
			}
		}
		// A field of a bound router or wrapper shares its prefix.
		if analysis.routerish(e.X, ctx) {
			return analysis.prefixOf(e.X, ctx)
		}
	case *ast.CallExpr:
		if pattern, captures := analysis.matchUserPattern(e, info); pattern != nil && pattern.Kind == scopePattern {
			if prefix, ok := constantString(captures[capturePrefix], info); ok {
				return analysis.prefixOf(captures[captureRouter], ctx).join(prefix)
			}
			return prefixValue{}
		}
		if name, ok := analysis.chiMethod(e, ctx); ok {
			receiver := unparen(e.Fun).(*ast.SelectorExpr).X
			switch name {
			case "With", "Group":
				return analysis.prefixOf(receiver, ctx)
			case "Route":
				if prefix, ok := constantString(e.Args[0], info); ok {
					return analysis.prefixOf(receiver, ctx).join(prefix)
				}
				return prefixValue{}
			}
		}
	}
	return rootPrefix
}

func (analysis *routeAnalysis) register(method string, router, pathExpr, handler ast.Expr, call *ast.CallExpr, ctx analysisContext) {
	prefix := analysis.prefixOf(router, ctx)
	if !prefix.known {
		return
	}
	literal, ok := constantString(pathExpr, ctx.info())
	if !ok {
		analysis.diagnoseDynamicPath(pathExpr, ctx)
		return
	}
	if !strings.HasPrefix(literal, "/") {
		analysis.diagnose("invalid-route-pattern", fmt.Sprintf("Chi routing pattern %q must begin with '/'", literal), pathExpr, ctx)
		return
	}
	path, slashOptional := joinRoute(prefix.prefix, literal)
	registration := ctx.pkg.location(pathExpr)
	key := routeKey{method: method, path: path, uri: registration.URI, offset: int(pathExpr.Pos())}
	basic, isLiteral := unparen(pathExpr).(*ast.BasicLit)
	analysis.routes[key] = Route{
		Method:          method,
		Path:            path,
		Params:          routeParams(path),
		Handler:         handlerName(handler, ctx.pkg),
		HandlerLocation: handlerLocation(handler, ctx.pkg),
		Registration:    registration,
		Declaration:     ctx.pkg.location(call),
		Prefix:          prefix.prefix,
		Literal:         literal,
		SlashOptional:   slashOptional,
		Editable:        isLiteral && basic.Kind == token.STRING,
		LiteralRaw:      isLiteral && strings.HasPrefix(basic.Value, "`"),
	}
}

func (analysis *routeAnalysis) diagnoseDynamicPath(pathExpr ast.Expr, ctx analysisContext) {
	if object, ok := objectOf(pathExpr, ctx.info()).(*types.Var); ok && ctx.fn != nil && ctx.fn.isParam(object) {
		analysis.diagnose("unknown-wrapper", fmt.Sprintf(
			"route path is parameter %q of %s, so routes registered through this wrapper cannot be analysed; add a chi.patterns.routes matcher for its call sites",
			object.Name(), ctx.fn.object.Name()), pathExpr, ctx)
		return
	}
	analysis.diagnose("dynamic-route", "Chi route path must be a compile-time string for HTMX analysis", pathExpr, ctx)
}

func (analysis *routeAnalysis) scope(router, prefixExpr, routes ast.Expr, call *ast.CallExpr, ctx analysisContext) {
	child := analysis.prefixOf(router, ctx)
	if prefixExpr != nil {
		prefix, ok := constantString(prefixExpr, ctx.info())
		switch {
		case !ok:
			analysis.diagnose("dynamic-prefix", "Chi route prefix must be a compile-time string for HTMX analysis; routes below it are not indexed", prefixExpr, ctx)
			child = prefixValue{}
		case !strings.HasPrefix(prefix, "/"):
			analysis.diagnose("invalid-route-pattern", fmt.Sprintf("Chi routing pattern %q must begin with '/'", prefix), prefixExpr, ctx)
			child = prefixValue{}
		default:
			child = child.join(prefix)
		}
	}
	analysis.bindRoutes(routes, child, ctx)
}

// bindRoutes analyses a route function, a literal or a named function or
// method value, with its first parameter bound to prefix.
func (analysis *routeAnalysis) bindRoutes(routes ast.Expr, prefix prefixValue, ctx analysisContext) {
	switch fn := unparen(routes).(type) {
	case *ast.FuncLit:
		env := make(map[types.Object]prefixValue, len(ctx.env)+1)
		for object, value := range ctx.env {
			env[object] = value
		}
		if params := fn.Type.Params.List; len(params) > 0 && len(params[0].Names) > 0 {
			if object := ctx.info().Defs[params[0].Names[0]]; object != nil {
				env[object] = prefix
			}
		}
		analysis.walkBody(fn.Body, analysisContext{pkg: ctx.pkg, fn: ctx.fn, env: env})
	default:
		if isNil(routes, ctx.info()) {
			return
		}
		callee := analysis.workspace.funcValue(routes, ctx.info())
		if callee == nil || callee.decl.Body == nil {
			analysis.diagnose("unresolved-routes", fmt.Sprintf("route function %s is not declared under chi.dir, so its routes are not indexed", exprText(routes, ctx.pkg)), routes, ctx)
			return
		}
		bindings := make(map[types.Object]prefixValue)
		if len(callee.params) > 0 && callee.params[0] != nil {
			bindings[callee.params[0]] = prefix
		}
		analysis.analyzeFunc(callee, bindings, nil)
	}
}

func (analysis *routeAnalysis) mount(router, prefixExpr, handler ast.Expr, call *ast.CallExpr, ctx analysisContext) {
	child := analysis.prefixOf(router, ctx)
	prefix, ok := constantString(prefixExpr, ctx.info())
	switch {
	case !ok:
		analysis.diagnose("dynamic-prefix", "Chi mount prefix must be a compile-time string for HTMX analysis; routes below it are not indexed", prefixExpr, ctx)
		return
	case !strings.HasPrefix(prefix, "/"):
		analysis.diagnose("invalid-route-pattern", fmt.Sprintf("Chi routing pattern %q must begin with '/'", prefix), prefixExpr, ctx)
		return
	}
	child = child.join(prefix)
	if !child.known {
		return
	}
	info := ctx.info()
	if object := objectOf(handler, info); object != nil && analysis.routerType(object.Type()) {
		// Bound by bindMounts; its registrations carry the mount prefix.
		return
	}
	if inner, ok := unparen(handler).(*ast.CallExpr); ok && analysis.routerType(info.TypeOf(inner)) {
		if callee := analysis.workspace.callee(inner.Fun, info); callee != nil && callee.decl.Body != nil {
			bindings := make(map[types.Object]prefixValue)
			for i, arg := range inner.Args {
				if i < len(callee.params) && callee.params[i] != nil && analysis.routerish(arg, ctx) {
					bindings[callee.params[i]] = analysis.prefixOf(arg, ctx)
				}
			}
			analysis.analyzeFunc(callee, bindings, &child)
			analysis.walkExprs(ctx, inner.Args...)
			return
		}
	}
	analysis.walkExprs(ctx, handler)
	path, _ := joinRoute(child.prefix, "/*")
	registration := ctx.pkg.location(prefixExpr)
	analysis.routes[routeKey{method: "*", path: path, uri: registration.URI, offset: int(prefixExpr.Pos())}] = Route{
		Method:          "*",
		Path:            path,
		Params:          routeParams(path),
		Handler:         handlerName(handler, ctx.pkg),
		HandlerLocation: handlerLocation(handler, ctx.pkg),
		Registration:    registration,
		Declaration:     ctx.pkg.location(call),
		Prefix:          child.prefix,
		Literal:         "/*",
		Subtree:         true,
	}
}

func (analysis *routeAnalysis) diagnose(code, message string, node ast.Node, ctx analysisContext) {
	location := ctx.pkg.location(node)
	key := diagnosticKey{code: code, uri: location.URI, line: location.Range.Start.Line, col: location.Range.Start.Character}
	analysis.diagnostics[key] = Diagnostic{Code: code, Message: message, Location: location}
}

func (analysis *routeAnalysis) index() RouteIndex {
	paths := make(map[string]map[string]bool)
	for key, route := range analysis.routes {
		registration := fmt.Sprintf("%s#%d", key.uri, key.offset)
		if paths[registration] == nil {
			paths[registration] = make(map[string]bool)
		}
		paths[registration][route.Path] = true
	}
	var index RouteIndex
	for key, route := range analysis.routes {
		if len(paths[fmt.Sprintf("%s#%d", key.uri, key.offset)]) > 1 {
			// Reached under several prefixes: one literal edit would move them all.
			route.Editable = false
		}
		index.Routes = append(index.Routes, route)
	}
	for _, diagnostic := range analysis.diagnostics {
		index.Diagnostics = append(index.Diagnostics, diagnostic)
	}
	sort.Slice(index.Routes, func(i, j int) bool {
		left, right := index.Routes[i], index.Routes[j]
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		if left.Method != right.Method {
			return left.Method < right.Method
		}
		if left.Registration.URI != right.Registration.URI {
			return left.Registration.URI < right.Registration.URI
		}
		return comparePosition(left.Registration.Range.Start, right.Registration.Range.Start) < 0
	})
	sort.Slice(index.Diagnostics, func(i, j int) bool {
		left, right := index.Diagnostics[i], index.Diagnostics[j]
		if left.Location.URI != right.Location.URI {
			return left.Location.URI < right.Location.URI
		}
		if order := comparePosition(left.Location.Range.Start, right.Location.Range.Start); order != 0 {
			return order < 0
		}
		return left.Code < right.Code
	})
	return index
}

func constantString(expr ast.Expr, info *types.Info) (string, bool) {
	if expr == nil {
		return "", false
	}
	value := info.Types[expr].Value
	if value == nil || value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(value), true
}

func objectOf(expr ast.Expr, info *types.Info) types.Object {
	switch e := unparen(expr).(type) {
	case *ast.Ident:
		if object := info.Uses[e]; object != nil {
			return object
		}
		return info.Defs[e]
	case *ast.SelectorExpr:
		if selection := info.Selections[e]; selection != nil {
			return selection.Obj()
		}
		return info.Uses[e.Sel]
	}
	return nil
}

func isNil(expr ast.Expr, info *types.Info) bool {
	ident, ok := unparen(expr).(*ast.Ident)
	if !ok {
		return false
	}
	_, isNil := info.Uses[ident].(*types.Nil)
	return isNil
}

func unparen(expr ast.Expr) ast.Expr {
	for {
		paren, ok := expr.(*ast.ParenExpr)
		if !ok {
			return expr
		}
		expr = paren.X
	}
}

func handlerName(expr ast.Expr, pkg *goPackage) string {
	if expr == nil {
		return ""
	}
	if _, ok := unparen(expr).(*ast.FuncLit); ok {
		return "func literal"
	}
	text := exprText(expr, pkg)
	if line, _, multiline := strings.Cut(text, "\n"); multiline {
		text = line + "..."
	}
	if len(text) > 120 {
		text = text[:117] + "..."
	}
	return text
}

func handlerLocation(expr ast.Expr, pkg *goPackage) *Location {
	if expr == nil {
		return nil
	}
	location := pkg.location(expr)
	return &location
}

// joinRoute composes a prefix and a pattern as Chi does. A "/" below a
// prefix is served with and without the trailing slash; the index keeps the
// prefix itself and reports the slash as optional.
func joinRoute(prefix, route string) (string, bool) {
	if prefix == "" {
		return route, false
	}
	if route == "" || route == "/" {
		return prefix, !strings.HasSuffix(prefix, "/")
	}
	return strings.TrimSuffix(prefix, "/") + "/" + strings.TrimPrefix(route, "/"), false
}

func routeParams(route string) []string {
	var params []string
	for i := 0; i < len(route); i++ {
		if route[i] == '*' {
			params = append(params, "*")
			continue
		}
		if route[i] != '{' {
			continue
		}
		start := i + 1
		depth := 1
		for i++; i < len(route) && depth > 0; i++ {
			switch route[i] {
			case '{':
				depth++
			case '}':
				depth--
			}
		}
		end := i - 1
		if depth != 0 || end <= start {
			break
		}
		name := route[start:end]
		if colon := strings.IndexByte(name, ':'); colon >= 0 {
			name = name[:colon]
		}
		params = append(params, name)
		i--
	}
	return params
}

func fileURI(path string) string {
	return "file://" + filepath.ToSlash(path)
}

func quoteLiteral(value string, raw bool) string {
	if raw && !strings.Contains(value, "`") {
		return "`" + value + "`"
	}
	return strconv.Quote(value)
}
