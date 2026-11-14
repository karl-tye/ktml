package templmigrate

import (
	"go/types"
	"strings"

	parser "github.com/a-h/templ/parser/v2"
)

// symbols indexes every templ HTML declaration in the inventory, so a call
// can be told apart from a Go function that merely returns a component.
type symbols struct {
	// byPath holds "import/path.Name" and "import/path.Recv.Name" keys,
	// from typed packages.
	byPath map[string]bool
	// byPackage holds "package.Name" keys, for syntactic resolution.
	byPackage map[string]bool
	// files maps both key forms to the declaring file.
	files map[string]string
}

func collectSymbols(files []*sourceFile, typed map[*sourceFile]*typedFile) *symbols {
	s := &symbols{byPath: make(map[string]bool), byPackage: make(map[string]bool), files: make(map[string]string)}
	for _, file := range files {
		if file.parsed == nil {
			continue
		}
		pkgName := strings.TrimSpace(strings.TrimPrefix(file.parsed.Package.Expression.Value, "package"))
		t := typed[file]
		for _, node := range file.parsed.Nodes {
			template, ok := node.(*parser.HTMLTemplate)
			if !ok {
				continue
			}
			value := strings.TrimSpace(template.Expression.Value)
			receiver := ""
			if strings.HasPrefix(value, "(") {
				end := strings.Index(value, ")")
				if end < 0 {
					continue
				}
				fields := strings.Fields(value[1:end])
				receiver = strings.TrimPrefix(fields[len(fields)-1], "*")
				value = strings.TrimSpace(value[end+1:])
			}
			name := strings.TrimSpace(strings.SplitN(value, "(", 2)[0])
			if receiver != "" {
				name = receiver + "." + name
			}
			s.byPackage[pkgName+"."+name] = true
			s.files[pkgName+"."+name] = file.rel
			if t != nil {
				s.byPath[t.pkg.Path()+"."+name] = true
				s.files[t.pkg.Path()+"."+name] = file.rel
			}
		}
	}
	return s
}

// template reports whether a call names a templ declaration, and the file
// that declares it.
func (s *symbols) template(w *walker, name string, obj types.Object, typed bool) (string, bool) {
	if fn, ok := obj.(*types.Func); ok && fn.Pkg() != nil {
		key := fn.Pkg().Path() + "."
		if recv := fn.Type().(*types.Signature).Recv(); recv != nil {
			recvType := recv.Type()
			if pointer, ok := recvType.(*types.Pointer); ok {
				recvType = pointer.Elem()
			}
			if named, ok := types.Unalias(recvType).(*types.Named); ok {
				key += named.Obj().Name() + "."
			}
		}
		return s.files[key+fn.Name()], s.byPath[key+fn.Name()]
	}
	if typed && obj != nil {
		return "", false
	}
	pkgName := strings.TrimSpace(strings.TrimPrefix(w.file.parsed.Package.Expression.Value, "package"))
	if local, ok := strings.CutPrefix(name, "."); ok {
		if s.byPackage[pkgName+"."+local] {
			return s.files[pkgName+"."+local], true
		}
		// A method template called on a receiver value.
		for _, key := range sortedKeys(s.byPackage) {
			if strings.HasSuffix(key, "."+local) && strings.Count(key, ".") == 2 {
				return s.files[key], true
			}
		}
		return "", false
	}
	path, function := name[:strings.LastIndex(name, ".")], name[strings.LastIndex(name, ".")+1:]
	key := path[strings.LastIndex(path, "/")+1:] + "." + function
	return s.files[key], s.byPackage[key]
}
