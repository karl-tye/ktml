package htmx

import (
	"go/ast"
	"go/scanner"
	"go/token"
)

// routingCallArity maps Chi's routing method names to their argument
// counts. A call with a matching name and arity may register routes;
// lookalikes such as Query().Get(key) have another arity.
var routingCallArity = map[string]int{
	"Get": 2, "Post": 2, "Put": 2, "Patch": 2, "Delete": 2, "Head": 2,
	"Options": 2, "Connect": 2, "Trace": 2, "Handle": 2, "HandleFunc": 2,
	"Method": 3, "MethodFunc": 3, "Route": 2, "Mount": 2, "Group": 1,
	"With": -1, "NewRouter": 0, "NewMux": 0,
}

// relevantFuncs returns the function declarations whose bodies can register
// routes: they make a routing-shaped call or a call matching a user pattern,
// mention a Chi package, or call such a function by name. Only their bodies
// are type-checked; handlers and templates are not.
func relevantFuncs(files []*ast.File, patterns Patterns) map[*ast.FuncDecl]bool {
	patternNames := make(map[string]bool)
	for _, list := range [][]*Pattern{patterns.Scope, patterns.Mount} {
		for _, pattern := range list {
			patternNames[callName(pattern.expr)] = true
		}
	}
	for _, list := range patterns.Routes {
		for _, pattern := range list {
			patternNames[callName(pattern.expr)] = true
		}
	}

	chiNames := make(map[*ast.File]map[string]bool)
	for _, file := range files {
		names := make(map[string]bool)
		for _, spec := range file.Imports {
			if path := spec.Path.Value; len(path) > 2 && chiImportPaths[path[1:len(path)-1]] {
				name := "chi"
				if spec.Name != nil {
					name = spec.Name.Name
				}
				names[name] = true
			}
		}
		chiNames[file] = names
	}

	type candidate struct {
		decl  *ast.FuncDecl
		calls map[string]bool
	}
	var candidates []candidate
	relevant := make(map[*ast.FuncDecl]bool)
	relevantNames := make(map[string]bool)
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			calls := make(map[string]bool)
			direct := false
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				switch node := node.(type) {
				case *ast.SelectorExpr:
					if ident, ok := node.X.(*ast.Ident); ok && chiNames[file][ident.Name] {
						direct = true
					}
				case *ast.CallExpr:
					name := callName(node)
					if arity, ok := routingCallArity[name]; ok && (arity < 0 || arity == len(node.Args)) {
						direct = true
					}
					if patternNames[name] {
						direct = true
					}
					if name != "" {
						calls[name] = true
					}
					// Function values passed along, as in r.Route("/x", h.Routes).
					for _, arg := range node.Args {
						if name := exprName(arg); name != "" {
							calls[name] = true
						}
					}
				}
				return !direct
			})
			if direct {
				relevant[fn] = true
				relevantNames[fn.Name.Name] = true
				continue
			}
			candidates = append(candidates, candidate{decl: fn, calls: calls})
		}
	}
	for changed := true; changed; {
		changed = false
		for _, candidate := range candidates {
			if relevant[candidate.decl] {
				continue
			}
			for name := range candidate.calls {
				if relevantNames[name] {
					relevant[candidate.decl] = true
					relevantNames[candidate.decl.Name.Name] = true
					changed = true
					break
				}
			}
		}
	}
	return relevant
}

func callName(call *ast.CallExpr) string {
	return exprName(call.Fun)
}

func exprName(expr ast.Expr) string {
	switch fun := expr.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		return fun.Sel.Name
	case *ast.ParenExpr:
		return exprName(fun.X)
	case *ast.IndexExpr:
		return exprName(fun.X)
	}
	return ""
}

// constDeclSources returns the source of each top-level const declaration,
// found with the scanner so that function bodies are never parsed.
func constDeclSources(source []byte) [][]byte {
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(source))
	var scan scanner.Scanner
	scan.Init(file, source, nil, 0)
	var decls [][]byte
	depth, parens := 0, 0
	start := -1
	first, grouped := false, false
	for {
		pos, tok, _ := scan.Scan()
		if tok == token.EOF {
			break
		}
		offset := file.Offset(pos)
		if start < 0 {
			switch tok {
			case token.LBRACE, token.LPAREN, token.LBRACK:
				depth++
			case token.RBRACE, token.RPAREN, token.RBRACK:
				depth--
			case token.CONST:
				if depth == 0 {
					start, first, parens = offset, true, 0
				}
			}
			continue
		}
		if first {
			// const ( ... ) ends at its closing parenthesis, const X = ...
			// at the end of its line.
			grouped, first = tok == token.LPAREN, false
		}
		switch tok {
		case token.LPAREN, token.LBRACE, token.LBRACK:
			parens++
		case token.RPAREN, token.RBRACE, token.RBRACK:
			parens--
			if grouped && parens == 0 {
				decls = append(decls, source[start:offset+1])
				start = -1
			}
		case token.SEMICOLON:
			if !grouped && parens == 0 {
				decls = append(decls, source[start:offset])
				start = -1
			}
		}
	}
	return decls
}
