package gogen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
)

// Fill lowering without closures (Hybrid H).
//
// A fill is first written as a ktml.ComponentFunc closure. Stored in a
// ktml.Component field and rendered through the interface, a closure always
// escapes, so each fill that captures anything is one heap allocation per
// render. liftFills rewrites every fill closure whose only captured variable
// is the render function's props pointer (or that captures nothing) into a
// named type with a Render method:
//
//	type ktmlFill3Page struct{ props *Page }
//
//	func (ktmlFill ktmlFill3Page) Render(ctx context.Context, ktmlW io.Writer) error {
//		props := ktmlFill.props
//		…the closure body…
//	}
//
// A struct with one pointer field is stored directly in an interface value,
// so building ktmlFill3Page{props} allocates nothing; only the props value
// itself escapes, once per render. Fills that capture any other local (a
// repetition item, a bind, a Render Identity) keep their closure. The
// rewrite is purely textual on the generated source: placeholder comments
// move with the text they map, and handwritten callers still see only
// ktml.Component fields.

// fillPrefix names lifted fill types: ktmlFill<sequence><Component>. The
// sequence is unique per generated file and the component name is unique per
// package and starts with a letter, so names are unique per package.
const fillPrefix = "ktmlFill"

type fillSite struct {
	sequence int
	call     *ast.CallExpr
	literal  *ast.FuncLit
	props    bool
	typ      string
}

// liftFills rewrites liftable fill closures in one component's generated
// declarations. It reports whether the result still references package ktml.
func liftFills(owner string, source []byte) ([]byte, bool, error) {
	const header = "package ktmllift\n"
	text := append([]byte(header), source...)
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", text, 0)
	if err != nil {
		return nil, false, fmt.Errorf("lift fills of %s: %w", owner, err)
	}
	offset := func(pos token.Pos) int { return fset.Position(pos).Offset }
	ktmlRefs := 0
	ast.Inspect(file, func(node ast.Node) bool {
		if selector, ok := node.(*ast.SelectorExpr); ok {
			if ident, ok := selector.X.(*ast.Ident); ok && ident.Name == "ktml" && ident.Obj == nil {
				ktmlRefs++
			}
		}
		return true
	})
	var sites []*fillSite
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		// propsDecl is the declaration of the render function's props
		// parameter (its *ast.Field).
		var propsDecl any
		propsType := ""
		for _, field := range function.Type.Params.List {
			for _, name := range field.Names {
				if name.Name == "props" {
					propsDecl = name.Obj.Decl
					propsType = string(text[offset(field.Type.Pos()):offset(field.Type.End())])
				}
			}
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			site := fillAt(node)
			if site == nil {
				return true
			}
			liftable := true
			ast.Inspect(site.literal.Body, func(node ast.Node) bool {
				ident, ok := node.(*ast.Ident)
				if !ok || ident.Obj == nil || !liftable {
					return liftable
				}
				declared, ok := ident.Obj.Decl.(ast.Node)
				if !ok {
					liftable = false
					return false
				}
				at := declared.Pos()
				inFunction := at >= function.Pos() && at < function.End()
				inLiteral := at >= site.literal.Pos() && at < site.literal.End()
				switch {
				case !inFunction || inLiteral:
				case propsDecl != nil && ident.Obj.Decl == propsDecl && ident.Name == "props":
					site.props = true
				default:
					liftable = false
				}
				return liftable
			})
			if liftable {
				site.typ = propsType
				sites = append(sites, site)
			}
			return true
		})
	}
	if len(sites) == 0 {
		return source, ktmlRefs > 0, nil
	}
	// Each lifted call drops one ktml.ComponentFunc reference.
	ktmlRefs -= len(sites)

	// rewrite copies text[start:end], replacing the outermost lifted calls in
	// it with their type's value and collecting their declarations.
	var declarations []fillDeclaration
	var rewrite func(start, end int) string
	rewrite = func(start, end int) string {
		var out strings.Builder
		at := start
		for _, site := range sites {
			callStart, callEnd := offset(site.call.Pos()), offset(site.call.End())
			if callStart < at || callEnd > end {
				continue
			}
			out.Write(text[at:callStart])
			name := fillPrefix + strconv.Itoa(site.sequence) + owner
			body := rewrite(offset(site.literal.Body.Lbrace)+1, offset(site.literal.Body.Rbrace))
			var declaration bytes.Buffer
			if site.props {
				fmt.Fprintf(&out, "%s{props}", name)
				fmt.Fprintf(&declaration, "type %s struct{ props %s }\n\nfunc (ktmlFill %s) Render(ctx context.Context, ktmlW io.Writer) error {\n\tprops := ktmlFill.props%s}\n\n", name, site.typ, name, body)
			} else {
				fmt.Fprintf(&out, "%s{}", name)
				fmt.Fprintf(&declaration, "type %s struct{}\n\nfunc (%s) Render(ctx context.Context, ktmlW io.Writer) error {%s}\n\n", name, name, body)
			}
			declarations = append(declarations, fillDeclaration{site.sequence, declaration.String()})
			at = callEnd
		}
		out.Write(text[at:end])
		return out.String()
	}
	sort.Slice(sites, func(i, j int) bool { return sites[i].call.Pos() < sites[j].call.Pos() })
	result := rewrite(len(header), len(text))
	sort.Slice(declarations, func(i, j int) bool { return declarations[i].sequence < declarations[j].sequence })
	var lifted strings.Builder
	lifted.WriteString(result)
	if !strings.HasSuffix(result, "\n") {
		lifted.WriteString("\n")
	}
	for _, declaration := range declarations {
		lifted.WriteString(declaration.text)
	}
	return []byte(lifted.String()), ktmlRefs > 0, nil
}

type fillDeclaration struct {
	sequence int
	text     string
}

// fillAt matches `ktmlContentN := ktml.ComponentFunc(func(…) error {…})`.
func fillAt(node ast.Node) *fillSite {
	assign, ok := node.(*ast.AssignStmt)
	if !ok || assign.Tok != token.DEFINE || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
		return nil
	}
	local, ok := assign.Lhs[0].(*ast.Ident)
	if !ok || !strings.HasPrefix(local.Name, "ktmlContent") {
		return nil
	}
	sequence, err := strconv.Atoi(strings.TrimPrefix(local.Name, "ktmlContent"))
	if err != nil {
		return nil
	}
	call, ok := assign.Rhs[0].(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return nil
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "ComponentFunc" {
		return nil
	}
	if pkg, ok := selector.X.(*ast.Ident); !ok || pkg.Name != "ktml" || pkg.Obj != nil {
		return nil
	}
	literal, ok := call.Args[0].(*ast.FuncLit)
	if !ok {
		return nil
	}
	return &fillSite{sequence: sequence, call: call, literal: literal}
}
