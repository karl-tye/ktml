package htmx

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"sync"
)

// chiImportPaths are the Chi import paths that type-check against the stub.
var chiImportPaths = map[string]bool{
	"github.com/go-chi/chi":    true,
	"github.com/go-chi/chi/v5": true,
}

// chiStubSource declares Chi's routing surface. The plugin cannot read the
// Chi module inside its sandbox, so user code type-checks against this stub:
// enough to give every router value, method chain, and embedding its Chi
// type. Handler and middleware types are left as any.
const chiStubSource = `package chi

type Middlewares []func(any) any

type Routes interface {
	Routes() []Route
	Middlewares() Middlewares
	Match(rctx *Context, method, path string) bool
	Find(rctx *Context, method, path string) string
	ServeHTTP(w, r any)
}

type Router interface {
	Routes
	Use(middlewares ...any)
	With(middlewares ...any) Router
	Group(fn func(r Router)) Router
	Route(pattern string, fn func(r Router)) Router
	Mount(pattern string, h any)
	Handle(pattern string, h any)
	HandleFunc(pattern string, h any)
	Method(method, pattern string, h any)
	MethodFunc(method, pattern string, h any)
	Connect(pattern string, h any)
	Delete(pattern string, h any)
	Get(pattern string, h any)
	Head(pattern string, h any)
	Options(pattern string, h any)
	Patch(pattern string, h any)
	Post(pattern string, h any)
	Put(pattern string, h any)
	Trace(pattern string, h any)
	NotFound(h any)
	MethodNotAllowed(h any)
}

type Route struct {
	SubRoutes Routes
	Handlers  map[string]any
	Pattern   string
}

type Context struct {
	Routes        Routes
	RoutePath     string
	RouteMethod   string
	URLParams     RouteParams
	RoutePatterns []string
}

type RouteParams struct {
	Keys, Values []string
}

type Mux struct{}

func NewRouter() *Mux { return nil }
func NewMux() *Mux    { return nil }

func (mx *Mux) Routes() []Route                                { return nil }
func (mx *Mux) Middlewares() Middlewares                       { return nil }
func (mx *Mux) Match(rctx *Context, method, path string) bool  { return false }
func (mx *Mux) Find(rctx *Context, method, path string) string { return "" }
func (mx *Mux) ServeHTTP(w, r any)                             {}
func (mx *Mux) Use(middlewares ...any)                         {}
func (mx *Mux) With(middlewares ...any) Router                 { return nil }
func (mx *Mux) Group(fn func(r Router)) Router                 { return nil }
func (mx *Mux) Route(pattern string, fn func(r Router)) Router { return nil }
func (mx *Mux) Mount(pattern string, h any)                    {}
func (mx *Mux) Handle(pattern string, h any)                   {}
func (mx *Mux) HandleFunc(pattern string, h any)               {}
func (mx *Mux) Method(method, pattern string, h any)           {}
func (mx *Mux) MethodFunc(method, pattern string, h any)       {}
func (mx *Mux) Connect(pattern string, h any)                  {}
func (mx *Mux) Delete(pattern string, h any)                   {}
func (mx *Mux) Get(pattern string, h any)                      {}
func (mx *Mux) Head(pattern string, h any)                     {}
func (mx *Mux) Options(pattern string, h any)                  {}
func (mx *Mux) Patch(pattern string, h any)                    {}
func (mx *Mux) Post(pattern string, h any)                     {}
func (mx *Mux) Put(pattern string, h any)                      {}
func (mx *Mux) Trace(pattern string, h any)                    {}
func (mx *Mux) NotFound(h any)                                 {}
func (mx *Mux) MethodNotAllowed(h any)                         {}

func Chain(middlewares ...any) Middlewares { return nil }
func URLParam(r any, key string) string    { return "" }
func URLParamFromCtx(ctx any, key string) string { return "" }
func RouteContext(ctx any) *Context        { return nil }
func NewRouteContext() *Context            { return nil }
func Walk(r Routes, walkFn any) error      { return nil }

var RouteCtxKey any
`

var (
	chiStubOnce    sync.Once
	chiStubPackage *types.Package
	chiRouterIface *types.Interface
)

// chiStub type-checks the stub once. Its package path is the real import path
// so error messages and type strings read as Chi's.
func chiStub() (*types.Package, *types.Interface) {
	chiStubOnce.Do(func() {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "chi_stub.go", chiStubSource, 0)
		if err != nil {
			panic("htmx: parse Chi stub: " + err.Error())
		}
		var config types.Config
		pkg, err := config.Check("github.com/go-chi/chi/v5", fset, []*ast.File{file}, nil)
		if err != nil {
			panic("htmx: type-check Chi stub: " + err.Error())
		}
		chiStubPackage = pkg
		chiRouterIface = pkg.Scope().Lookup("Router").Type().Underlying().(*types.Interface)
	})
	return chiStubPackage, chiRouterIface
}

// isChiMethod reports whether a selected method is one of the stub's, which
// holds for chi.Router, *chi.Mux, method values on them, and methods
// promoted through embedding.
func isChiMethod(object types.Object) bool {
	fn, ok := object.(*types.Func)
	if !ok || fn.Pkg() == nil {
		return false
	}
	stub, _ := chiStub()
	return fn.Pkg() == stub
}

// isRouterType reports whether values of typ can register Chi routes: they
// implement chi.Router directly or through a pointer.
func isRouterType(typ types.Type) bool {
	if typ == nil {
		return false
	}
	if basic, ok := typ.Underlying().(*types.Basic); ok && basic.Kind() == types.Invalid {
		return false
	}
	_, iface := chiStub()
	if types.Implements(typ, iface) {
		return true
	}
	if _, isPointer := typ.(*types.Pointer); !isPointer {
		if _, isInterface := typ.Underlying().(*types.Interface); !isInterface {
			return types.Implements(types.NewPointer(typ), iface)
		}
	}
	return false
}

func packageNameGuess(path string) string {
	name := path[strings.LastIndex(path, "/")+1:]
	if len(name) > 1 && name[0] == 'v' && strings.Trim(name[1:], "0123456789") == "" {
		trimmed := strings.TrimSuffix(path, "/"+name)
		name = trimmed[strings.LastIndex(trimmed, "/")+1:]
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
