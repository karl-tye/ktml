package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestParse_GIVENReloadWatch_WHENParsed_THENKeepsValidPatternsAndReportsBadOnes(t *testing.T) {
	cfg, err := Parse([]byte(`{"dev": {"proxy_target": "http://127.0.0.1:3000", "command": ["shop"], "reload_watch": ["*.css", "*.svg"]}}`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if !reflect.DeepEqual(cfg.Dev.ReloadWatch, []string{"*.css", "*.svg"}) {
		t.Errorf("reload_watch = %q", cfg.Dev.ReloadWatch)
	}

	_, err = Parse([]byte(`{"dev": {"proxy_target": "http://127.0.0.1:3000", "command": ["shop"], "reload_watch": ["*.css", "[a-"]}}`))

	if err == nil || !strings.Contains(err.Error(), "reload_watch") {
		t.Fatalf("Parse() error = %v, want a dev.reload_watch error", err)
	}
}
