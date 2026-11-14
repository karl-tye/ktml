package main

import (
	"reflect"
	"testing"

	"github.com/karl-tye/ktml/internal/config"
)

func TestPluginOptions_GIVENHTMLExtensionAttributes_WHENLoadingAPlugin_THENTheHostPassesThem(t *testing.T) {
	cfg, err := config.Parse([]byte(`{"plugin": {"gohtml": {"path": "g.wasm", "allow_read": ["."], "settings": {"a": 1}}}, "html": {"extension_attributes": ["hx-require"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	options := pluginOptions(cfg, cfg.Plugin["gohtml"])
	if !reflect.DeepEqual(options.AllowRead, []string{"."}) || options.Settings["a"] != float64(1) {
		t.Errorf("options = %+v, want the plugin's own allow_read and settings", options)
	}
	want := map[string]any{"html": map[string]any{"extension_attributes": []string{"hx-require"}}}
	if !reflect.DeepEqual(options.Host, want) {
		t.Errorf("host = %#v, want %#v", options.Host, want)
	}
}

func TestPluginOptions_GIVENNoHTMLSection_WHENLoadingAPlugin_THENPassesNoHostSettings(t *testing.T) {
	cfg, err := config.Parse([]byte(`{"plugin": {"gohtml": {"path": "g.wasm"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if options := pluginOptions(cfg, cfg.Plugin["gohtml"]); options.Host != nil {
		t.Errorf("host = %#v, want nil", options.Host)
	}
}
