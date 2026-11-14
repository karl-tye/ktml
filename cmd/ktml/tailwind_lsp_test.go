package main

import (
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/config"
)

func TestTailwindAdapter_GIVENAMissingLanguageServer_THENReportsItOnTheTailwindCommand(t *testing.T) {
	source := []byte(`{
  "tailwind": {
    "command": ["no-such-tailwindcss-language-server", "--stdio"]
  }
}`)
	adapter, diagnostics := tailwindAdapter(&config.Tailwind{Command: []string{"no-such-tailwindcss-language-server", "--stdio"}}, source)
	defer adapter.Close()
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %+v, want one", diagnostics)
	}
	got := diagnostics[0]
	if text := string(source[got.Start:got.End]); !strings.HasPrefix(text, `["no-such-tailwindcss-language-server"`) {
		t.Errorf("diagnostic range = %q, want the command", text)
	}
	if got.Code != "tailwind-server" || !strings.Contains(got.Message, "npm install -g @tailwindcss/language-server@0.16.0") {
		t.Errorf("diagnostic = %+v", got)
	}
}

func TestTailwindAdapter_GIVENAnInstalledLanguageServer_THENReportsNothing(t *testing.T) {
	// sh stands in for an installed server: only the lookup is checked.
	adapter, diagnostics := tailwindAdapter(&config.Tailwind{Command: []string{"sh"}}, []byte(`{"tailwind":{"command":["sh"]}}`))
	defer adapter.Close()
	if len(diagnostics) != 0 {
		t.Errorf("diagnostics = %+v, want none", diagnostics)
	}
}
