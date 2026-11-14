package compiler_test

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspace_GIVENVoidElementWithContent_WHENLoaded_THENReportsVoidContentAtTheName(t *testing.T) {
	tests := []struct {
		name, body, span string
	}{
		{name: "text", body: `br { "x" }`, span: "br"},
		{name: "element", body: `img (src="/a.png" alt="a") { span "x" }`, span: "img"},
		{name: "nested", body: "div {\n\t\tinput { \"x\" }\n\t}", span: "input"},
		{name: "in a condition", body: `if on { hr { "x" } }`, span: "hr"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := "Page [on: boolean] {\n\t" + test.body + "\n}\n"
			dir := writeWorkspace(t, map[string]string{"page.ktml": source})
			diagnostics := loadEntry(t, dir, "page.ktml").Diagnostics(filepath.Join(dir, "page.ktml"))
			if len(diagnostics) != 1 || diagnostics[0].Code != "void-content" {
				t.Fatalf("diagnostics = %v, want one void-content", codes(diagnostics))
			}
			if got := source[diagnostics[0].Start:diagnostics[0].End]; got != test.span {
				t.Errorf("range = %q, want %q", got, test.span)
			}
			if message := diagnostics[0].Message; !strings.Contains(message, "void element") {
				t.Errorf("message = %q", message)
			}
		})
	}
}

func TestWorkspace_GIVENVoidElementsWithoutContent_WHENLoaded_THENReportsNothing(t *testing.T) {
	source := "Page [] {\n\tbr\n\timg (src=\"/a.png\" alt=\"a\")\n\tinput type=\"text\"\n\tdiv { hr }\n}\n"
	dir := writeWorkspace(t, map[string]string{"page.ktml": source})
	if diagnostics := loadEntry(t, dir, "page.ktml").Diagnostics(filepath.Join(dir, "page.ktml")); len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", codes(diagnostics))
	}
}
