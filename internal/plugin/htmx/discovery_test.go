package htmx

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func writeGoFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, source := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func discoverForTest(t *testing.T, settings ChiSettings) RouteIndex {
	t.Helper()
	index, err := DiscoverRoutesWith(settings)
	if err != nil {
		t.Fatalf("DiscoverRoutesWith() error = %v", err)
	}
	return index
}

func diagnosticCodes(index RouteIndex) []string {
	codes := make([]string, 0, len(index.Diagnostics))
	for _, diagnostic := range index.Diagnostics {
		codes = append(codes, diagnostic.Code)
	}
	return codes
}

func TestDiscoverRoutes_GIVENEveryStandardRegistration_WHENScanned_THENIndexesEachMethod(t *testing.T) {
	dir := writeGoFixture(t, `package web

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/a", h)
	r.Post("/a", h)
	r.Put("/a", h)
	r.Patch("/a", h)
	r.Delete("/a", h)
	r.Head("/a", h)
	r.Options("/a", h)
	r.Handle("/handle", h)
	r.HandleFunc("/handle-func", h)
	r.Method("post", "/method", h)
	r.MethodFunc(http.MethodPut, "/method-func", h)
	return r
}
`)
	index := discoverForTest(t, ChiSettings{Dir: dir, Patterns: Patterns{Defaults: true}})
	want := []string{"DELETE /a", "GET /a", "HEAD /a", "OPTIONS /a", "PATCH /a", "POST /a", "PUT /a", "* /handle", "* /handle-func", "POST /method"}
	if got := routeKeys(index.Routes); !reflect.DeepEqual(got, want) {
		t.Errorf("routes = %#v, want %#v", got, want)
	}
	// net/http is outside the sandbox, so http.MethodPut cannot be evaluated.
	if got := diagnosticCodes(index); !reflect.DeepEqual(got, []string{"dynamic-method"}) {
		t.Errorf("diagnostics = %#v, want one dynamic-method", index.Diagnostics)
	}
}

func TestDiscoverRoutes_GIVENNestedScopes_WHENScanned_THENComposesRouteGroupWithAndMountPrefixes(t *testing.T) {
	dir := writeGoFixture(t, `package web

import "github.com/go-chi/chi/v5"

const tickets = "/tickets"

type Handler struct{}

func (h *Handler) Routes(r chi.Router) {
	r.Post("/payments", h.payments)
}

func (h *Handler) API() chi.Router {
	api := chi.NewRouter()
	api.Get("/status", h.status)
	return api
}

func NewRouter(h *Handler, admin chi.Router) chi.Router {
	router := chi.NewRouter()
	router.Route("/webhooks", h.Routes)
	router.Group(func(g chi.Router) {
		g.Use(auth)
		g.With(audit).Get(tickets, list)
		g.Route(tickets+"/{id}", func(sub chi.Router) {
			sub.Get("/", show)
			sub.With(audit).Put("/status", update)
			sub.Route("/notes", func(notes chi.Router) {
				notes.Delete("/{noteID}", deleteNote)
			})
		})
	})
	router.Mount("/admin", admin)
	admin.Get("/users", users)
	router.Mount("/api", h.API())
	router.Mount("/debug", profiler())
	return router
}
`)
	index := discoverForTest(t, ChiSettings{Dir: dir, Patterns: Patterns{Defaults: true}})
	want := []string{
		"GET /admin/users",
		"GET /api/status",
		"* /debug/*",
		"GET /tickets",
		"GET /tickets/{id}",
		"DELETE /tickets/{id}/notes/{noteID}",
		"PUT /tickets/{id}/status",
		"POST /webhooks/payments",
	}
	if got := routeKeys(index.Routes); !reflect.DeepEqual(got, want) {
		t.Errorf("routes = %#v\nwant   %#v", got, want)
	}
	if len(index.Diagnostics) != 0 {
		t.Errorf("diagnostics = %#v, want none", index.Diagnostics)
	}
	for _, route := range index.Routes {
		if route.Path == "/tickets/{id}/notes/{noteID}" && !reflect.DeepEqual(route.Params, []string{"id", "noteID"}) {
			t.Errorf("params = %#v, want [id noteID]", route.Params)
		}
		if route.Path == "/tickets/{id}" && !route.SlashOptional {
			t.Errorf("GET /tickets/{id} from \"/\" should accept a trailing slash")
		}
		if route.Path == "/webhooks/payments" && route.Handler != "h.payments" {
			t.Errorf("handler = %q, want h.payments", route.Handler)
		}
	}
}

func TestDiscoverRoutes_GIVENRouterTypeEmbeddingMux_WHENScanned_THENFindsRoutesWhateverTheReceiverName(t *testing.T) {
	dir := writeGoFixture(t, `package web

import "github.com/go-chi/chi/v5"

type Router struct {
	*chi.Mux
	roles []string
}

func (app *Router) AddAuthentication() {
	app.Get("/login", login)
}

type server struct{ routes chi.Router }

func (s server) register() {
	s.routes.Post("/orders", createOrder)
}

func New() *Router {
	mux := Router{Mux: chi.NewRouter()}
	mux.AddAuthentication()
	mux.Route("/members", func(anything chi.Router) {
		anything.Get("/{memberID}", member)
	})
	return &mux
}
`)
	index := discoverForTest(t, ChiSettings{Dir: dir, Patterns: Patterns{Defaults: true}})
	want := []string{"GET /login", "GET /members/{memberID}", "POST /orders"}
	if got := routeKeys(index.Routes); !reflect.DeepEqual(got, want) {
		t.Errorf("routes = %#v, want %#v", got, want)
	}
}

func TestDiscoverRoutes_GIVENModuleConstantsOutsideDir_WHENReadable_THENEvaluatesThem(t *testing.T) {
	root := writeGoFiles(t, map[string]string{
		"go.mod": "module example.com/app\n\ngo 1.22\n",
		"internal/paths/paths.go": `package paths

const base = "/vendors"
const Terms = base + "/apply/terms"
`,
		"internal/web/router.go": `package web

import (
	"github.com/go-chi/chi/v5"

	"example.com/app/internal/paths"
	other "example.com/app/internal/web/sub"
)

func Routes(r chi.Router) {
	r.Get(paths.Terms, terms)
	r.Get(other.Path, sub)
}
`,
		"internal/web/sub/sub.go": `package sub

const Path = "/sub"
`,
	})
	index := discoverForTest(t, ChiSettings{Dir: filepath.Join(root, "internal", "web"), Patterns: Patterns{Defaults: true}})
	want := []string{"GET /sub", "GET /vendors/apply/terms"}
	if got := routeKeys(index.Routes); !reflect.DeepEqual(got, want) {
		t.Errorf("routes = %#v, want %#v", got, want)
	}
	for _, route := range index.Routes {
		if route.Editable {
			t.Errorf("%s is registered through a constant and must not be editable", route.Path)
		}
	}
}

func TestDiscoverRoutes_GIVENUnanalysableRegistrations_WHENScanned_THENReportsEachExplicitly(t *testing.T) {
	dir := writeGoFixture(t, `package web

import "github.com/go-chi/chi/v5"

func get(r chi.Router, path string, h any) {
	r.Get(path, h)
}

func Routes(r chi.Router, prefix string, external func(chi.Router)) {
	get(r, "/wrapped", h)
	r.Route(prefix, func(r chi.Router) {
		r.Get("/hidden", h)
	})
	r.Mount(prefix, h)
	r.Route("/ext", external)
	r.Get("relative", h)
	r.Post(dynamicPath(), h)
}
`)
	index := discoverForTest(t, ChiSettings{Dir: dir, Patterns: Patterns{Defaults: true}})
	if len(index.Routes) != 0 {
		t.Errorf("routes = %#v, want none", routeKeys(index.Routes))
	}
	want := []string{"unknown-wrapper", "dynamic-prefix", "dynamic-prefix", "unresolved-routes", "invalid-route-pattern", "dynamic-route"}
	if got := diagnosticCodes(index); !reflect.DeepEqual(got, want) {
		t.Errorf("diagnostics = %#v\nwant %#v", index.Diagnostics, want)
	}
	if !strings.Contains(index.Diagnostics[0].Message, `parameter "path" of get`) {
		t.Errorf("wrapper diagnostic = %q", index.Diagnostics[0].Message)
	}
}

func TestDiscoverRoutes_GIVENWrapperPatterns_WHENScanned_THENIndexesWrapperCallSitesOnly(t *testing.T) {
	dir := writeGoFixture(t, `package web

import "github.com/go-chi/chi/v5"

type App struct{ r chi.Router }

func (a *App) GET(path string, h any)  { a.r.Get(path, h) }
func (a *App) POST(path string, h any) { a.r.Post(path, h) }
func (a *App) Section(prefix string, fn func(*App)) {
	a.r.Route(prefix, func(r chi.Router) { fn(&App{r: r}) })
}
func (a *App) Attach(prefix string, h any) { a.r.Mount(prefix, h) }

func Register(app *App) {
	app.GET("/", home)
	app.Section("/vendors", func(vendors *App) {
		vendors.POST("/{id}/approve", approve)
	})
	app.Attach("/files", files)
	app.r.Get("/health", health)
}
`)
	patterns := mustPatterns(t, `{
		"scope": ["{router}.Section({prefix}, {routes})"],
		"mount": ["{router}.Attach({prefix}, {handler})"],
		"routes": {
			"GET": ["{router}.GET({route}, {handler})"],
			"POST": ["{router}.POST({route}, {handler})"]
		}
	}`)
	index := discoverForTest(t, ChiSettings{Dir: dir, Patterns: patterns})
	want := []string{"GET /", "* /files/*", "GET /health", "POST /vendors/{id}/approve"}
	if got := routeKeys(index.Routes); !reflect.DeepEqual(got, want) {
		t.Errorf("routes = %#v, want %#v", got, want)
	}
	if len(index.Diagnostics) != 0 {
		t.Errorf("wrapper bodies must not be diagnosed once patterns cover them: %#v", index.Diagnostics)
	}

	patterns.Defaults = false
	index = discoverForTest(t, ChiSettings{Dir: dir, Patterns: patterns})
	want = []string{"GET /", "* /files/*", "POST /vendors/{id}/approve"}
	if got := routeKeys(index.Routes); !reflect.DeepEqual(got, want) {
		t.Errorf("with defaults false routes = %#v, want %#v", got, want)
	}
}

func TestDiscoverRoutes_GIVENDevelopmentBuildVariant_WHENScanned_THENIgnoresConstrainedFiles(t *testing.T) {
	dir := writeGoFiles(t, map[string]string{
		"router.go": `package web

import "github.com/go-chi/chi/v5"

func Routes(r chi.Router) { r.Get(path, h) }
`,
		"path.go": `//go:build !ktmldev

package web

const path = "/production"
`,
		"path_dev.go": `//go:build ktmldev

package web

const path = "/development"
`,
		"path_windows.go": `package web

const other = "/windows"
`,
	})
	index := discoverForTest(t, ChiSettings{Dir: dir, Patterns: Patterns{Defaults: true}})
	if got := routeKeys(index.Routes); !reflect.DeepEqual(got, []string{"GET /production"}) {
		t.Errorf("routes = %#v, want GET /production", got)
	}
}

func TestDiscoverRoutes_GIVENRegistrationReachedUnderTwoPrefixes_WHENScanned_THENNeitherIsEditable(t *testing.T) {
	dir := writeGoFixture(t, `package web

import "github.com/go-chi/chi/v5"

func common(r chi.Router) { r.Get("/status", status) }

func Routes(r chi.Router) {
	r.Route("/a", common)
	r.Route("/b", common)
	r.Get("/c", c)
}
`)
	index := discoverForTest(t, ChiSettings{Dir: dir, Patterns: Patterns{Defaults: true}})
	if got, want := routeKeys(index.Routes), []string{"GET /a/status", "GET /b/status", "GET /c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("routes = %#v, want %#v", got, want)
	}
	for _, route := range index.Routes {
		if got, want := route.Editable, route.Path == "/c"; got != want {
			t.Errorf("%s Editable = %t, want %t", route.Path, got, want)
		}
	}
}

func TestGoWorkspace_GIVENOneChangedFile_WHENRefreshed_THENRecheckesOnlyItsPackageAndDependents(t *testing.T) {
	dir := writeGoFiles(t, map[string]string{
		"go.mod":             "module example.com/app\n",
		"web/router.go":      "package web\n\nimport (\n\t\"github.com/go-chi/chi/v5\"\n\t\"example.com/app/web/api\"\n)\n\nfunc Routes(r chi.Router) { r.Route(\"/api\", api.Routes) }\n",
		"web/api/api.go":     "package api\n\nimport \"github.com/go-chi/chi/v5\"\n\nfunc Routes(r chi.Router) { r.Get(\"/one\", h) }\n",
		"web/other/other.go": "package other\n\nimport \"github.com/go-chi/chi/v5\"\n\nfunc Routes(r chi.Router) { r.Get(\"/other\", h) }\n",
	})
	workspace := newGoWorkspace(ChiSettings{Dir: filepath.Join(dir, "web"), Patterns: Patterns{Defaults: true}})
	index, changed, err := workspace.refresh()
	if err != nil || !changed {
		t.Fatalf("first refresh changed=%t err=%v", changed, err)
	}
	if got, want := routeKeys(index.Routes), []string{"GET /api/one", "GET /other"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("routes = %#v, want %#v", got, want)
	}
	typesBefore := map[string]any{}
	for path, pkg := range workspace.packages {
		typesBefore[filepath.Base(path)] = pkg.types
	}

	if _, changed, _ := workspace.refresh(); changed {
		t.Fatalf("refresh without edits reported a change")
	}
	apiFile := filepath.Join(dir, "web", "api", "api.go")
	writeLater(t, apiFile, "package api\n\nimport \"github.com/go-chi/chi/v5\"\n\nfunc Routes(r chi.Router) { r.Get(\"/two\", h) }\n")
	index, changed, err = workspace.refresh()
	if err != nil || !changed {
		t.Fatalf("refresh after edit changed=%t err=%v", changed, err)
	}
	if got, want := routeKeys(index.Routes), []string{"GET /api/two", "GET /other"}; !reflect.DeepEqual(got, want) {
		t.Errorf("routes after edit = %#v, want %#v", got, want)
	}
	for path, pkg := range workspace.packages {
		reused := typesBefore[filepath.Base(path)] == any(pkg.types)
		if want := filepath.Base(path) == "other"; reused != want {
			t.Errorf("package %s reused = %t, want %t", filepath.Base(path), reused, want)
		}
	}
}

func writeLater(t *testing.T, path, content string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Coarse filesystem clocks could keep the old modification time.
	later := info.ModTime().Add(2 * time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
}

func mustPatterns(t *testing.T, patterns string) Patterns {
	t.Helper()
	settings, err := ParseSettings([]byte(`{"chi":{"dir":".","patterns":` + patterns + `}}`))
	if err != nil {
		t.Fatalf("ParseSettings() error = %v", err)
	}
	return settings.Chi.Patterns
}

func TestDiscoverRoutes_GIVENRegistrationsOnlyReachedThroughPlainCalls_WHENBodiesAreSkipped_THENStillFindsThem(t *testing.T) {
	dir := writeGoFiles(t, map[string]string{
		"a.go": `package web

import "github.com/go-chi/chi/v5"

func Setup(r chi.Router) { configure(r) }

func configure(router chi.Router) { attach(router, "unused") }
`,
		"b.go": `package web

import "github.com/go-chi/chi/v5"

func attach(r chi.Router, _ string) { r.Post("/deep", deep) }

func handler(q query) string { return q.Get("id") }
`,
	})
	index := discoverForTest(t, ChiSettings{Dir: dir, Patterns: Patterns{Defaults: true}})
	if got := routeKeys(index.Routes); !reflect.DeepEqual(got, []string{"POST /deep"}) {
		t.Errorf("routes = %#v, want POST /deep", got)
	}
	workspace := newGoWorkspace(ChiSettings{Dir: dir, Patterns: Patterns{Defaults: true}})
	if _, _, err := workspace.refresh(); err != nil {
		t.Fatal(err)
	}
	for _, pkg := range workspace.packages {
		for _, fn := range pkg.funcs {
			if got, want := fn.unchecked, fn.decl.Name.Name == "handler"; got != want {
				t.Errorf("%s unchecked = %t, want %t", fn.decl.Name.Name, got, want)
			}
		}
	}
}

func TestConstDeclSources_GIVENFileWithFunctions_WHENScanned_THENReturnsOnlyTopLevelConsts(t *testing.T) {
	source := "package p\n\nconst A = \"/a\" + B\n\nconst (\n\tB = \"/b\"\n\tC = len(\"x\")\n)\n\nfunc f() {\n\tconst local = \"/local\"\n}\n\nconst D = string(\"/d\")\n"
	var got []string
	for _, decl := range constDeclSources([]byte(source)) {
		got = append(got, string(decl))
	}
	want := []string{"const A = \"/a\" + B", "const (\n\tB = \"/b\"\n\tC = len(\"x\")\n)", "const D = string(\"/d\")"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("decls = %q\nwant %q", got, want)
	}
}

func TestGoWorkspace_GIVENChangedPaths_WHENRefreshingThem_THENReparsesOnlyThoseFiles(t *testing.T) {
	dir := writeGoFiles(t, map[string]string{
		"a.go": "package web\n\nimport \"github.com/go-chi/chi/v5\"\n\nfunc A(r chi.Router) { r.Get(\"/a\", h) }\n",
		"b.go": "package web\n\nimport \"github.com/go-chi/chi/v5\"\n\nfunc B(r chi.Router) { r.Get(\"/b\", h) }\n",
	})
	workspace := newGoWorkspace(ChiSettings{Dir: dir, Patterns: Patterns{Defaults: true}})
	if _, _, err := workspace.refresh(); err != nil {
		t.Fatal(err)
	}
	a, b := filepath.Join(dir, "a.go"), filepath.Join(dir, "b.go")
	before := workspace.files[b]
	writeLater(t, a, "package web\n\nimport \"github.com/go-chi/chi/v5\"\n\nfunc A(r chi.Router) { r.Get(\"/a2\", h) }\n")
	// c.go is new and named; a file written but not named stays unread.
	if err := os.WriteFile(filepath.Join(dir, "c.go"), []byte("package web\n\nimport \"github.com/go-chi/chi/v5\"\n\nfunc C(r chi.Router) { r.Get(\"/c\", h) }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "d.go"), []byte("package web\n\nimport \"github.com/go-chi/chi/v5\"\n\nfunc D(r chi.Router) { r.Get(\"/d\", h) }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	index, err := workspace.refreshPaths([]string{a, filepath.Join(dir, "c.go"), "/elsewhere/x.go"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := routeKeys(index.Routes), []string{"GET /a2", "GET /b", "GET /c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("routes = %#v, want %#v", got, want)
	}
	if workspace.files[b] != before {
		t.Errorf("b.go was re-parsed")
	}
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	index, _ = workspace.refreshPaths([]string{a})
	if got, want := routeKeys(index.Routes), []string{"GET /b", "GET /c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("routes after delete = %#v, want %#v", got, want)
	}
}
