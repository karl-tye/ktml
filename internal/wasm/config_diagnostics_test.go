package wasm

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/karl-tye/ktml/internal/configdiag"
)

// TestPlugin_GIVENInvalidHTMXSettings_WHENLoaded_THENTheErrorRangesEachViolationInTheConfig
// drives the real HTMX plugin, so the settings error text the host parses is
// the one the guest reports.
func TestPlugin_GIVENInvalidHTMXSettings_WHENLoaded_THENTheErrorRangesEachViolationInTheConfig(t *testing.T) {
	source := `{
  "plugin": {
    "htmx": {
      "path": "./plugins/htmx.wasm",
      "settings": {
        "chi": {
          "dir": "",
          "patterns": {"routes": {"GET": ["{router}.Get({x}})"], "get": []}}
        },
        "chi_dir": "./internal/web"
      }
    }
  }
}`
	var config struct {
		Plugin map[string]struct {
			Settings map[string]any `json:"settings"`
		} `json:"plugin"`
	}
	if err := json.Unmarshal([]byte(source), &config); err != nil {
		t.Fatal(err)
	}
	_, err := NewPluginWithOptions(context.Background(), buildHtmxWasm(t), Options{Settings: config.Plugin["htmx"].Settings})
	if err == nil {
		t.Fatal("NewPluginWithOptions() succeeded with invalid settings")
	}
	diagnostics := configdiag.Plugin([]byte(source), "htmx", err)
	got := map[string]bool{}
	for _, diagnostic := range diagnostics {
		got[source[diagnostic.Start:diagnostic.End]] = true
	}
	for _, want := range []string{`""`, `}`, `[]`, `"./internal/web"`} {
		if !got[want] {
			t.Errorf("no diagnostic at %s; diagnostics = %#v (error %v)", want, diagnostics, err)
		}
	}
}
