package htmx

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"go/types"
	"sort"
	"strings"
)

// A Pattern is a Go call expression with named captures, compiled once and
// matched against the typed Go AST. Captures are written {name}; the
// anonymous {_} matches any one expression and {...} matches the remaining
// call arguments. Identifiers outside captures match by name, and a package
// name also matches through an import alias.
type Pattern struct {
	Source string
	Kind   patternKind
	Method string
	expr   *ast.CallExpr
}

type patternKind int

const (
	scopePattern patternKind = iota
	mountPattern
	routePattern
)

func (kind patternKind) String() string {
	switch kind {
	case scopePattern:
		return "scope"
	case mountPattern:
		return "mount"
	default:
		return "routes"
	}
}

const (
	captureRouter  = "router"
	capturePrefix  = "prefix"
	captureRoutes  = "routes"
	captureRoute   = "route"
	captureHandler = "handler"
)

// patternCaptures lists, per pattern kind, the required named captures.
// {handler} is accepted in mount patterns as a synonym of {routes}.
var patternCaptures = map[patternKind][]string{
	scopePattern: {captureRouter, capturePrefix, captureRoutes},
	mountPattern: {captureRouter, capturePrefix, captureRoutes},
	routePattern: {captureRouter, captureRoute, captureHandler},
}

const (
	wildcardIdent = "___"   // {_}, the same width as its source
	restIdent     = "_____" // {...}
)

// CompilePattern parses and validates one matcher. Errors carry the
// 1-based column inside the pattern string.
func CompilePattern(kind patternKind, source string) (*Pattern, error) {
	substituted, captures, err := substituteCaptures(source)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	expr, err := parser.ParseExprFrom(fset, "", substituted, 0)
	if err != nil {
		if list, ok := err.(scanner.ErrorList); ok && len(list) > 0 {
			return nil, fmt.Errorf("column %d: %s; a pattern is a Go call expression such as {router}.Get({route}, {handler})", list[0].Pos.Column, list[0].Msg)
		}
		return nil, fmt.Errorf("not a Go expression: %v", err)
	}
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return nil, fmt.Errorf("must be a call expression such as {router}.Get({route}, {handler})")
	}
	column := func(node ast.Node) int { return fset.Position(node.Pos()).Column }

	seen := make(map[string]bool)
	var problem error
	ast.Inspect(call, func(node ast.Node) bool {
		if problem != nil || node == nil {
			return false
		}
		switch node := node.(type) {
		case *ast.CallExpr:
			if node.Ellipsis.IsValid() {
				problem = fmt.Errorf("column %d: spread arguments are not supported; use {...} for the remaining arguments", column(node))
				return false
			}
			for i, arg := range node.Args {
				if ident, ok := arg.(*ast.Ident); ok && ident.Name == restIdent && i != len(node.Args)-1 {
					problem = fmt.Errorf("column %d: {...} must be the last argument", column(arg))
					return false
				}
			}
		case *ast.SelectorExpr:
			if name, ok := captures[node.Sel.Name]; ok {
				problem = fmt.Errorf("column %d: capture {%s} cannot name a method or field; captures stand for expressions", column(node.Sel), name)
				return false
			}
			if node.Sel.Name == wildcardIdent || node.Sel.Name == restIdent {
				problem = fmt.Errorf("column %d: wildcards cannot name a method or field", column(node.Sel))
				return false
			}
			// Selector names are literal; only the operand is matched further.
			ast.Inspect(node.X, func(inner ast.Node) bool {
				return inner == nil || visitPatternNode(inner, captures, seen, column, &problem)
			})
			return false
		case *ast.Ident:
			if node.Name == restIdent {
				if parent := enclosingCallArg(call, node); !parent {
					problem = fmt.Errorf("column %d: {...} is only valid as the last call argument", column(node))
				}
				return false
			}
		}
		return visitPatternNode(node, captures, seen, column, &problem)
	})
	if problem != nil {
		return nil, problem
	}

	allowed := patternCaptures[kind]
	allowedSet := make(map[string]bool, len(allowed)+1)
	for _, name := range allowed {
		allowedSet[name] = true
	}
	if kind == mountPattern {
		allowedSet[captureHandler] = true
	}
	var unknown []string
	for name := range seen {
		if !allowedSet[name] {
			unknown = append(unknown, "{"+name+"}")
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("%s patterns accept only %s, {_}, and {...}; found %s", kind, captureList(allowed), strings.Join(unknown, ", "))
	}
	if kind == mountPattern && seen[captureRoutes] && seen[captureHandler] {
		return nil, fmt.Errorf("mount patterns take {routes} or its synonym {handler}, not both")
	}
	var missing []string
	for _, name := range allowed {
		if seen[name] || (kind == mountPattern && name == captureRoutes && seen[captureHandler]) {
			continue
		}
		missing = append(missing, "{"+name+"}")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%s patterns require %s; missing %s", kind, captureList(allowed), strings.Join(missing, ", "))
	}
	return &Pattern{Source: source, Kind: kind, expr: call}, nil
}

func visitPatternNode(node ast.Node, captures map[string]string, seen map[string]bool, column func(ast.Node) int, problem *error) bool {
	if *problem != nil {
		return false
	}
	switch node := node.(type) {
	case *ast.Ident:
		if name, ok := captures[node.Name]; ok {
			if seen[name] {
				*problem = fmt.Errorf("column %d: capture {%s} appears more than once", column(node), name)
				return false
			}
			seen[name] = true
		}
		return true
	case *ast.CallExpr, *ast.SelectorExpr, *ast.BasicLit, *ast.ParenExpr, *ast.StarExpr, *ast.UnaryExpr, *ast.IndexExpr:
		return true
	default:
		*problem = fmt.Errorf("column %d: unsupported syntax %T in a pattern; use calls, selectors, identifiers, literals, and captures", column(node), node)
		return false
	}
}

func enclosingCallArg(root *ast.CallExpr, target *ast.Ident) bool {
	found := false
	ast.Inspect(root, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || found {
			return !found
		}
		for _, arg := range call.Args {
			if arg == target {
				found = true
			}
		}
		return true
	})
	return found
}

func captureList(names []string) string {
	braced := make([]string, len(names))
	for i, name := range names {
		braced[i] = "{" + name + "}"
	}
	return strings.Join(braced, ", ")
}

// substituteCaptures replaces each {name} with an identifier of the same
// width (_name_), {_} with ___ and {...} with _____, so parser columns stay
// those of the source.
func substituteCaptures(source string) (string, map[string]string, error) {
	var out strings.Builder
	captures := make(map[string]string)
	for i := 0; i < len(source); i++ {
		if source[i] == '}' {
			return "", nil, fmt.Errorf("column %d: unmatched }", i+1)
		}
		if source[i] != '{' {
			out.WriteByte(source[i])
			continue
		}
		end := strings.IndexByte(source[i:], '}')
		if end < 0 {
			return "", nil, fmt.Errorf("column %d: unterminated capture", i+1)
		}
		name := source[i+1 : i+end]
		switch {
		case name == "_":
			out.WriteString(wildcardIdent)
		case name == "...":
			out.WriteString(restIdent)
		case validCaptureName(name):
			ident := "_" + name + "_"
			captures[ident] = name
			out.WriteString(ident)
		default:
			return "", nil, fmt.Errorf("column %d: invalid capture {%s}; captures are lower-case names such as {router}", i+1, name)
		}
		i += end
	}
	return out.String(), captures, nil
}

func validCaptureName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && r != '_' {
			return false
		}
	}
	return true
}

// match reports whether node matches the pattern and returns its captures.
func (pattern *Pattern) match(node *ast.CallExpr, info *types.Info) (map[string]ast.Expr, bool) {
	captures := make(map[string]ast.Expr)
	if !matchNode(pattern.expr, node, info, captures) {
		return nil, false
	}
	return captures, true
}

func matchNode(pattern, node ast.Node, info *types.Info, captures map[string]ast.Expr) bool {
	if paren, ok := node.(*ast.ParenExpr); ok {
		if _, patternParen := pattern.(*ast.ParenExpr); !patternParen {
			return matchNode(pattern, paren.X, info, captures)
		}
	}
	switch pattern := pattern.(type) {
	case *ast.Ident:
		if pattern.Name == wildcardIdent {
			return true
		}
		if name := captureName(pattern.Name); name != "" {
			expr, ok := node.(ast.Expr)
			if ok {
				captures[name] = expr
			}
			return ok
		}
		ident, ok := node.(*ast.Ident)
		if !ok {
			return false
		}
		if ident.Name == pattern.Name {
			return true
		}
		if pkgName, ok := info.Uses[ident].(*types.PkgName); ok {
			return pkgName.Imported().Name() == pattern.Name
		}
		return false
	case *ast.CallExpr:
		call, ok := node.(*ast.CallExpr)
		if !ok || call.Ellipsis.IsValid() || !matchNode(pattern.Fun, call.Fun, info, captures) {
			return false
		}
		args := pattern.Args
		rest := len(args) > 0 && isRestIdent(args[len(args)-1])
		if rest {
			args = args[:len(args)-1]
			if len(call.Args) < len(args) {
				return false
			}
		} else if len(call.Args) != len(args) {
			return false
		}
		for i, arg := range args {
			if !matchNode(arg, call.Args[i], info, captures) {
				return false
			}
		}
		return true
	case *ast.SelectorExpr:
		selector, ok := node.(*ast.SelectorExpr)
		return ok && selector.Sel.Name == pattern.Sel.Name && matchNode(pattern.X, selector.X, info, captures)
	case *ast.BasicLit:
		literal, ok := node.(*ast.BasicLit)
		return ok && literal.Kind == pattern.Kind && literal.Value == pattern.Value
	case *ast.ParenExpr:
		paren, ok := node.(*ast.ParenExpr)
		return ok && matchNode(pattern.X, paren.X, info, captures)
	case *ast.StarExpr:
		star, ok := node.(*ast.StarExpr)
		return ok && matchNode(pattern.X, star.X, info, captures)
	case *ast.UnaryExpr:
		unary, ok := node.(*ast.UnaryExpr)
		return ok && unary.Op == pattern.Op && matchNode(pattern.X, unary.X, info, captures)
	case *ast.IndexExpr:
		index, ok := node.(*ast.IndexExpr)
		return ok && matchNode(pattern.X, index.X, info, captures) && matchNode(pattern.Index, index.Index, info, captures)
	}
	return false
}

func captureName(ident string) string {
	if len(ident) < 3 || ident[0] != '_' || ident[len(ident)-1] != '_' || ident == wildcardIdent || ident == restIdent {
		return ""
	}
	name := ident[1 : len(ident)-1]
	if !validCaptureName(name) {
		return ""
	}
	return name
}

func isRestIdent(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == restIdent
}
