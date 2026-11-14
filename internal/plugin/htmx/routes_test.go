package htmx

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDiscoverRoutes_GIVENStandardChiMethods_WHENScanned_THENIndexesRoutes(t *testing.T) {
	dir := writeGoFixture(t, `package web

import "github.com/go-chi/chi/v5"

func Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/vendors", listVendors)
	r.Post("/vendors", createVendor)
	r.Put("/vendors/{id}", updateVendor)
	r.Patch("/vendors/{id}/status", updateStatus)
	r.Delete("/vendors/{id}", deleteVendor)
	r.Handle("/assets/*", assets)
	return r
}
`)

	index, err := DiscoverRoutes(dir)
	if err != nil {
		t.Fatalf("DiscoverRoutes() error = %v", err)
	}

	got := routeKeys(index.Routes)
	want := []string{
		"* /assets/*",
		"GET /vendors",
		"POST /vendors",
		"DELETE /vendors/{id}",
		"PUT /vendors/{id}",
		"PATCH /vendors/{id}/status",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("routes = %#v, want %#v", got, want)
	}
	if got, want := index.Routes[0].Params, []string{"*"}; !reflect.DeepEqual(got, want) {
		t.Errorf("wildcard params = %#v, want %#v", got, want)
	}
}

func TestDiscoverRoutes_GIVENNestedRoutes_WHENScanned_THENComposesPrefixes(t *testing.T) {
	dir := writeGoFixture(t, `package web

import "github.com/go-chi/chi/v5"

func Routes() chi.Router {
	r := chi.NewRouter()
	r.Route("/vendors", func(api chi.Router) {
		api.Route("/{id}", func(actions chi.Router) {
			actions.Post("/approve", approveVendor)
		})
	})
	return r
}
`)

	index, err := DiscoverRoutes(dir)
	if err != nil {
		t.Fatalf("DiscoverRoutes() error = %v", err)
	}
	if got, want := routeKeys(index.Routes), []string{"POST /vendors/{id}/approve"}; !reflect.DeepEqual(got, want) {
		t.Errorf("routes = %#v, want %#v", got, want)
	}
	if got, want := index.Routes[0].Params, []string{"id"}; !reflect.DeepEqual(got, want) {
		t.Errorf("params = %#v, want %#v", got, want)
	}
}

func TestDiscoverRoutes_GIVENConstantsAndMount_WHENScanned_THENResolvesEffectivePath(t *testing.T) {
	dir := writeGoFixture(t, `package web

import "github.com/go-chi/chi/v5"

const apiPrefix = "/api"
const version = "/v1"

func Routes() chi.Router {
	r := chi.NewRouter()
	vendors := chi.NewRouter()
	vendors.Get("/{id:[0-9]{4}}", getVendor)
	r.Mount(apiPrefix + version + "/vendors", vendors)
	return r
}
`)

	index, err := DiscoverRoutes(dir)
	if err != nil {
		t.Fatalf("DiscoverRoutes() error = %v", err)
	}
	if got, want := routeKeys(index.Routes), []string{"GET /api/v1/vendors/{id:[0-9]{4}}"}; !reflect.DeepEqual(got, want) {
		t.Errorf("routes = %#v, want %#v", got, want)
	}
	if got, want := index.Routes[0].Params, []string{"id"}; !reflect.DeepEqual(got, want) {
		t.Errorf("params = %#v, want %#v", got, want)
	}
}

func TestDiscoverRoutes_GIVENDynamicPath_WHENScanned_THENReportsDiagnostic(t *testing.T) {
	dir := writeGoFixture(t, `package web

import "github.com/go-chi/chi/v5"

func Routes() chi.Router {
	r := chi.NewRouter()
	r.Post(basePath()+"/approve", handler)
	return r
}
`)

	index, err := DiscoverRoutes(dir)
	if err != nil {
		t.Fatalf("DiscoverRoutes() error = %v", err)
	}
	if len(index.Routes) != 0 {
		t.Errorf("routes = %#v, want none", index.Routes)
	}
	if len(index.Diagnostics) != 1 || index.Diagnostics[0].Code != "dynamic-route" {
		t.Fatalf("diagnostics = %#v, want one dynamic-route diagnostic", index.Diagnostics)
	}
}

func TestDiscoverRoutes_GIVENUnrelatedPostMethod_WHENScanned_THENIgnoresIt(t *testing.T) {
	dir := writeGoFixture(t, `package web

import "github.com/go-chi/chi/v5"

type client struct{}
func (client) Post(string, any) {}

func Routes() chi.Router {
	r := chi.NewRouter()
	c := client{}
	c.Post("/not-a-route", handler)
	r.Post("/route", handler)
	return r
}
`)

	index, err := DiscoverRoutes(dir)
	if err != nil {
		t.Fatalf("DiscoverRoutes() error = %v", err)
	}
	if got, want := routeKeys(index.Routes), []string{"POST /route"}; !reflect.DeepEqual(got, want) {
		t.Errorf("routes = %#v, want %#v", got, want)
	}
}

func TestDiscoverRoutes_GIVENInvalidDirectory_WHENScanned_THENReturnsError(t *testing.T) {
	_, err := DiscoverRoutes(filepath.Join(t.TempDir(), "missing"))
	if err == nil || !strings.Contains(err.Error(), "read Chi directory") {
		t.Fatalf("DiscoverRoutes() error = %v, want invalid directory error", err)
	}
}

func writeGoFixture(t *testing.T, source string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "router.go")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return dir
}

func routeKeys(routes []Route) []string {
	keys := make([]string, 0, len(routes))
	for _, route := range routes {
		keys = append(keys, route.Method+" "+route.Path)
	}
	return keys
}
