package templmigrate

import (
	"go/ast"
	goparser "go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// foreignComponent is a KTML component an earlier in-place batch converted,
// which this batch's templ files still call through its templ bridge.
type foreignComponent struct {
	dir     string
	doc     string // the .ktml document declaring the component
	schema  string // the package's .ktmlschema document
	pkgPath string
	pkgName string
	// extra are the bridge literal's fields no templ parameter maps to,
	// added by hand (key: value): a KTML call cannot fill them.
	extra []string
}

// foreignSig returns the signature of the KTML component behind a templ
// bridge (a function <Template>Templ in a converter-written _bridge.go), or
// nil. The bridge's body is the component's Hybrid H literal, so it holds
// the presenter and every field the templ parameters fill.
func (c *conversion) foreignSig(fn *types.Func) *tmplSig {
	key := fn.Pkg().Path() + "." + fn.Name()
	if sig, ok := c.foreign[key]; ok {
		return sig
	}
	c.foreign[key] = nil
	if !strings.HasSuffix(fn.Name(), "Templ") || fn.Type().(*types.Signature).Recv() != nil {
		return nil
	}
	var fset *token.FileSet
	for _, t := range c.run.typed {
		fset = t.fset
		break
	}
	if fset == nil {
		return nil
	}
	path := fset.Position(fn.Pos()).Filename
	data, err := os.ReadFile(path)
	if err != nil || !converterOutput(path, data[:min(len(data), 512)]) || !strings.HasSuffix(path, "_bridge.go") {
		return nil
	}
	file, err := goparser.ParseFile(token.NewFileSet(), path, data, goparser.SkipObjectResolution)
	if err != nil {
		return nil
	}
	var decl *ast.FuncDecl
	for _, d := range file.Decls {
		if f, ok := d.(*ast.FuncDecl); ok && f.Name.Name == fn.Name() {
			decl = f
		}
	}
	literal := bridgeLiteral(decl)
	if literal == nil {
		return nil
	}
	typeName, ok := literal.Type.(*ast.Ident)
	if !ok {
		return nil
	}
	dir := filepath.Dir(path)
	// The Go type of an unexported template's component is exported
	// (secret is Secret); markup calls it by its KTML name.
	component, doc := typeName.Name, componentDocument(dir, typeName.Name)
	if lower := lowerFirst(typeName.Name); doc == "" && lower != typeName.Name {
		component, doc = lower, componentDocument(dir, lower)
	}
	sig := &tmplSig{
		name: strings.TrimSuffix(fn.Name(), "Templ"), component: component, fn: fn,
		key:     key,
		foreign: &foreignComponent{dir: dir, doc: doc, schema: c.schemaDoc(dir), pkgPath: fn.Pkg().Path(), pkgName: fn.Pkg().Name()},
	}
	if sig.foreign.doc == "" {
		return nil
	}
	// Where each bridge parameter goes.
	presenterArgs := map[string]bool{}
	fields := map[string]string{} // parameter -> KTML input
	for _, element := range literal.Elts {
		kv, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		input := lowerFirst(key.Name)
		mapped := true
		switch value := kv.Value.(type) {
		case *ast.CallExpr:
			callee := callName(value.Fun)
			switch {
			case callee == "templ.GetChildren":
				sig.children = true
			case strings.HasPrefix(callee, "migrate") || callee == "int64":
				mapped = false
				if len(value.Args) == 1 {
					if ident, ok := value.Args[0].(*ast.Ident); ok {
						fields[ident.Name] = input
						mapped = true
					}
				}
			case sig.view || strings.Contains(callee, "."):
				// The presenter is the first call of a package function.
				mapped = false
			default:
				// The presenter.
				sig.view, sig.viewInput, sig.presenter = true, input, callee
				for i, arg := range value.Args {
					switch a := arg.(type) {
					case *ast.Ident:
						if a.Name == "ctx" && i == 0 {
							sig.context = true
						} else {
							presenterArgs[a.Name] = true
						}
					case *ast.CallExpr:
						if callName(a.Fun) == "time.Now" {
							sig.clock = true
						}
					}
				}
			}
		case *ast.Ident:
			fields[value.Name] = input
		default:
			mapped = false
		}
		if !mapped {
			sig.foreign.extra = append(sig.foreign.extra, key.Name+": "+types.ExprString(kv.Value))
		}
	}
	if sig.view {
		presenter, ok := fn.Pkg().Scope().Lookup(sig.presenter).(*types.Func)
		if !ok {
			return nil // the package's KTML Go is not generated yet
		}
		results := presenter.Type().(*types.Signature).Results()
		if results.Len() != 1 {
			return nil
		}
		named, ok := types.Unalias(results.At(0).Type()).(*types.Named)
		if !ok {
			return nil
		}
		sig.viewType = named.Obj().Name()
	}
	sig.clockName = "now"
	signature := fn.Type().(*types.Signature)
	for i := 0; i < signature.Params().Len(); i++ {
		v := signature.Params().At(i)
		pt := v.Type()
		if signature.Variadic() && i == signature.Params().Len()-1 {
			pt = types.NewSlice(pt.(*types.Slice).Elem())
		}
		p := typedParam(v.Name(), pt)
		ktmlName := v.Name()
		if input, ok := fields[v.Name()]; ok {
			ktmlName = input
		}
		sig.params = append(sig.params, sigParam{name: v.Name(), kind: p.kind, t: v.Type(), obj: v, ktml: ktmlName})
	}
	c.foreign[key] = sig
	return sig
}

// bridgeLiteral finds the component literal a bridge renders:
// return X{…}.Render(templ.ClearChildren(ctx), w).
func bridgeLiteral(decl *ast.FuncDecl) *ast.CompositeLit {
	if decl == nil || decl.Body == nil {
		return nil
	}
	var found *ast.CompositeLit
	ast.Inspect(decl.Body, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Render" || found != nil {
			return true
		}
		if lit, ok := sel.X.(*ast.CompositeLit); ok {
			found = lit
		}
		return true
	})
	return found
}

func callName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		if x, ok := f.X.(*ast.Ident); ok {
			return x.Name + "." + f.Sel.Name
		}
	}
	return ""
}

func lowerFirst(name string) string {
	if name == "" || name[0] < 'A' || name[0] > 'Z' {
		return name
	}
	return string(name[0]-'A'+'a') + name[1:]
}

// componentDocument returns the .ktml document of dir that declares a
// component, or "".
func componentDocument(dir, component string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	declaration := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(component) + `(\s*\[|\s*\{|\s*$)`)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".ktml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err == nil && declaration.Match(data) {
			return filepath.Join(dir, entry.Name())
		}
	}
	return ""
}
