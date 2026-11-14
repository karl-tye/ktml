package htmx

import (
	"strings"
	"testing"
)

func TestParseSettings_GIVENMinimalChiConfig_WHENParsed_THENReturnsDirectory(t *testing.T) {
	settings, err := ParseSettings([]byte(`{"chi":{"dir":"./internal/web"}}`))
	if err != nil {
		t.Fatalf("ParseSettings() error = %v", err)
	}
	if got, want := settings.Chi.Dir, "./internal/web"; got != want {
		t.Errorf("Chi.Dir = %q, want %q", got, want)
	}
}

func TestParseSettings_GIVENMissingDirectory_WHENParsed_THENReturnsError(t *testing.T) {
	_, err := ParseSettings([]byte(`{"chi":{}}`))
	if err == nil || !strings.Contains(err.Error(), "chi.dir") {
		t.Fatalf("ParseSettings() error = %v, want chi.dir error", err)
	}
}

func TestParseSettings_GIVENHostPathMap_WHENParsed_THENUsesHostDirectory(t *testing.T) {
	settings, err := ParseSettings([]byte(`{
		"chi": {"dir": "."},
		"_ktml": {"path_map": {".": "/workspace/project"}}
	}`))
	if err != nil {
		t.Fatalf("ParseSettings() error = %v", err)
	}
	if got, want := settings.Chi.Dir, "/workspace/project"; got != want {
		t.Errorf("Chi.Dir = %q, want %q", got, want)
	}
}

func TestParseSettings_GIVENSchemaViolations_WHENParsed_THENReportsEachWithItsPath(t *testing.T) {
	tests := []struct {
		name     string
		settings string
		want     []string
	}{
		{"non-object", `[]`, []string{"htmx settings: settings: must be a JSON object"}},
		{"unknown top-level", `{"chi":{"dir":"."},"routes":{}}`, []string{`htmx settings: routes: unknown setting; the HTMX plugin accepts "chi"`}},
		{"dir type", `{"chi":{"dir":3}}`, []string{"htmx settings: chi.dir: must be a string"}},
		{"empty dir", `{"chi":{"dir":"  "}}`, []string{"htmx settings: chi.dir: must not be empty"}},
		{"unknown chi key", `{"chi":{"dir":".","dirs":["."]}}`, []string{`htmx settings: chi.dirs: unknown setting; chi accepts "dir" and "patterns"`}},
		{"patterns type", `{"chi":{"dir":".","patterns":[]}}`, []string{"htmx settings: chi.patterns: must be a JSON object"}},
		{"unknown patterns key", `{"chi":{"dir":".","patterns":{"route":{}}}}`, []string{`htmx settings: chi.patterns.route: unknown setting; patterns accepts "defaults", "scope", "mount", and "routes"`}},
		{"defaults type", `{"chi":{"dir":".","patterns":{"defaults":"no","scope":["{router}.Route({prefix}, {routes})"]}}}`, []string{"htmx settings: chi.patterns.defaults: must be true (extend the built-in Chi matchers) or false (replace them)"}},
		{"defaults false alone", `{"chi":{"dir":".","patterns":{"defaults":false}}}`, []string{"htmx settings: chi.patterns.defaults: false disables the built-in Chi matchers, so at least one scope, mount, or routes pattern is required"}},
		{"unknown method", `{"chi":{"dir":".","patterns":{"routes":{"get":["{router}.Get({route}, {handler})"]}}}}`, []string{`htmx settings: chi.patterns.routes.get: unknown HTTP method; use an upper-case method such as GET or POST, or "*" for every method`}},
		{"list type", `{"chi":{"dir":".","patterns":{"scope":"{router}.Route({prefix}, {routes})"}}}`, []string{"htmx settings: chi.patterns.scope: must be an array of pattern strings"}},
		{"element type", `{"chi":{"dir":".","patterns":{"mount":[1]}}}`, []string{"htmx settings: chi.patterns.mount[0]: must be a string"}},
		{"syntax error column", `{"chi":{"dir":".","patterns":{"routes":{"GET":["{router}.Get({route}, {handler}"]}}}}`, []string{"htmx settings: chi.patterns.routes.GET[0]: column 32: missing ',' before newline in argument list; a pattern is a Go call expression such as {router}.Get({route}, {handler})"}},
		{"not a call", `{"chi":{"dir":".","patterns":{"routes":{"GET":["{router}.Get"]}}}}`, []string{"htmx settings: chi.patterns.routes.GET[0]: must be a call expression such as {router}.Get({route}, {handler})"}},
		{"unknown capture", `{"chi":{"dir":".","patterns":{"routes":{"POST":["{router}.Post({path}, {handler})"]}}}}`, []string{"htmx settings: chi.patterns.routes.POST[0]: routes patterns accept only {router}, {route}, {handler}, {_}, and {...}; found {path}"}},
		{"missing capture", `{"chi":{"dir":".","patterns":{"scope":["{router}.Route({prefix}, {_})"]}}}`, []string{"htmx settings: chi.patterns.scope[0]: scope patterns require {router}, {prefix}, {routes}; missing {routes}"}},
		{"duplicate capture", `{"chi":{"dir":".","patterns":{"routes":{"GET":["{router}.Get({route}, {route}, {handler})"]}}}}`, []string{"htmx settings: chi.patterns.routes.GET[0]: column 23: capture {route} appears more than once"}},
		{"capture as method", `{"chi":{"dir":".","patterns":{"routes":{"GET":["{router}.{route}({handler})"]}}}}`, []string{"htmx settings: chi.patterns.routes.GET[0]: column 10: capture {route} cannot name a method or field; captures stand for expressions"}},
		{"invalid capture", `{"chi":{"dir":".","patterns":{"routes":{"GET":["{Router}.Get({route}, {handler})"]}}}}`, []string{"htmx settings: chi.patterns.routes.GET[0]: column 1: invalid capture {Router}; captures are lower-case names such as {router}"}},
		{"rest not last", `{"chi":{"dir":".","patterns":{"routes":{"GET":["{router}.Get({...}, {route}, {handler})"]}}}}`, []string{"htmx settings: chi.patterns.routes.GET[0]: column 14: {...} must be the last argument"}},
		{"unsupported syntax", `{"chi":{"dir":".","patterns":{"routes":{"GET":["{router}.Get({route} + \"/x\", {handler})"]}}}}`, []string{"htmx settings: chi.patterns.routes.GET[0]: column 14: unsupported syntax *ast.BinaryExpr in a pattern; use calls, selectors, identifiers, literals, and captures"}},
		{"several at once", `{"chi":{"dir":3,"extra":true}}`, []string{"htmx settings: chi.dir: must be a string", `htmx settings: chi.extra: unknown setting; chi accepts "dir" and "patterns"`}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseSettings([]byte(test.settings))
			if err == nil {
				t.Fatalf("ParseSettings() error = nil, want %q", test.want)
			}
			if got := strings.Split(err.Error(), "\n"); strings.Join(got, "|") != strings.Join(test.want, "|") {
				t.Errorf("errors =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(test.want, "\n"))
			}
		})
	}
}

func TestParseSettings_GIVENValidPatterns_WHENParsed_THENCompilesEachKind(t *testing.T) {
	settings, err := ParseSettings([]byte(`{"chi":{"dir":".","patterns":{
		"defaults": false,
		"scope": ["{router}.Section({prefix}, {routes})"],
		"mount": ["{router}.Attach({prefix}, {handler})", "{router}.Mount({prefix}, {routes})"],
		"routes": {"GET": ["{router}.With({...}).GET({route}, {handler})"], "*": ["{router}.Any({route}, {handler}, {_})"]}
	}}}`))
	if err != nil {
		t.Fatalf("ParseSettings() error = %v", err)
	}
	patterns := settings.Chi.Patterns
	if patterns.Defaults || len(patterns.Scope) != 1 || len(patterns.Mount) != 2 || len(patterns.Routes["GET"]) != 1 || patterns.Routes["*"][0].Method != "*" {
		t.Errorf("patterns = %#v", patterns)
	}
}

func TestParseSettings_GIVENDirInsideWiderReadMount_WHENParsed_THENResolvesAgainstThatMount(t *testing.T) {
	settings, err := ParseSettings([]byte(`{
		"chi": {"dir": "./internal/web"},
		"_ktml": {"path_map": {".": "/workspace/project", "internal": "/workspace/project/internal"}}
	}`))
	if err != nil {
		t.Fatalf("ParseSettings() error = %v", err)
	}
	if got, want := settings.Chi.Dir, "/workspace/project/internal/web"; got != want {
		t.Errorf("Chi.Dir = %q, want %q", got, want)
	}
}
