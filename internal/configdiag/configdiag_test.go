package configdiag

import (
	"encoding/json"
	"errors"
	"testing"
)

const config = `{
  "resolution": {"lsp": ["*"]},
  "plugin": {
    "htmx": {
      "path": "./plugins/htmx.wasm",
      "limits": {"memory_mb": -1},
      "settings": {
        "chi": {
          "dir": "",
          "patterns": {
            "routes": {"GET": ["{router}.Get({route}, {handler})", "{router}.Get({x}})"], "*": [1]}
          }
        },
        "extra": true
      }
    }
  }
}`

func spell(diagnostic Diagnostic) string {
	return config[diagnostic.Start:diagnostic.End]
}

func TestLocate_GIVENPaths_WHENLocated_THENReturnsTheValueOrItsDeepestAncestor(t *testing.T) {
	tests := []struct {
		path []string
		want string
	}{
		{ParsePath("plugin.htmx.settings.chi.dir"), `""`},
		{ParsePath("plugin.htmx.limits.memory_mb"), `-1`},
		{ParsePath("plugin.htmx.settings.chi.patterns.routes.GET[1]"), `"{router}.Get({x}})"`},
		{ParsePath("plugin.htmx.settings.chi.patterns.routes.*[0]"), `1`},
		{ParsePath("plugin.htmx.settings.extra"), `true`},
		{ParsePath("resolution.lsp[0]"), `"*"`},
	}
	for _, test := range tests {
		start, end := Locate([]byte(config), test.path)
		if got := config[start:end]; got != test.want {
			t.Errorf("Locate(%v) = %q, want %q", test.path, got, test.want)
		}
	}
	start, end := Locate([]byte(config), ParsePath("plugin.htmx.settings.missing"))
	if got := config[start:end]; got[0] != '{' || got[len(got)-1] != '}' {
		t.Errorf("missing member located at %q, want the settings object", got)
	}
}

func TestPlugin_GIVENSettingsErrors_WHENConverted_THENRangesEachAtItsValue(t *testing.T) {
	err := errors.New("ktml_configure failed: panic: htmx settings: chi.dir: must not be empty\n" +
		"htmx settings: chi.patterns.routes.GET[1]: column 17: unmatched }\n" +
		"htmx settings: chi.patterns.routes.*[0]: must be a string\n" +
		"htmx settings: extra: unknown setting; the HTMX plugin accepts \"chi\"")
	diagnostics := Plugin([]byte(config), "htmx", err)
	want := []string{`""`, `}`, `1`, `true`}
	if len(diagnostics) != len(want) {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	for index, diagnostic := range diagnostics {
		if got := spell(diagnostic); got != want[index] || diagnostic.Code != "plugin-settings" {
			t.Errorf("diagnostic %d = %q (%s), want %q", index, got, diagnostic.Message, want[index])
		}
	}
	if spell(diagnostics[1]) != "}" || config[diagnostics[1].Start-1] != '}' {
		t.Errorf("column diagnostic points at %d", diagnostics[1].Start)
	}
}

func TestPlugin_GIVENAnErrorWithoutSettingsPaths_WHENConverted_THENRangesAtThePlugin(t *testing.T) {
	diagnostics := Plugin([]byte(config), "htmx", errors.New("failed to read plugin wasm: no such file"))
	if len(diagnostics) != 1 || diagnostics[0].Code != "plugin-load" || spell(diagnostics[0])[0] != '{' {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func TestConfig_GIVENLoadErrors_WHENConverted_THENRangesAtThePathOrOffset(t *testing.T) {
	diagnostics := Config([]byte(config), &PathError{Path: ParsePath("plugin.htmx.limits.memory_mb"), Message: "must not be negative"})
	if len(diagnostics) != 1 || spell(diagnostics[0]) != "-1" {
		t.Fatalf("path diagnostics = %#v", diagnostics)
	}
	var target struct{}
	syntax := json.Unmarshal([]byte(`{"a": }`), &target)
	diagnostics = Config([]byte(`{"a": }`), syntax)
	if len(diagnostics) != 1 || diagnostics[0].Code != "config-syntax" || `{"a": }`[diagnostics[0].Start] != '}' {
		t.Fatalf("syntax diagnostics = %#v", diagnostics)
	}
}
