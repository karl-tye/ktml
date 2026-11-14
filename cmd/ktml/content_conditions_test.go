package main

import (
	"os"
	"strings"
	"testing"
)

func TestGenerateFile_GIVENInvalidContentOrCondition_WHENGenerated_THENReportsTheDiagnosticAndWritesNothing(t *testing.T) {
	tests := []struct{ name, body, code string }{
		{name: "comparison", body: `if title == "x" {}`, code: "[condition-comparison]"},
		{name: "literal condition", body: `if true {}`, code: "[condition-literal]"},
		{name: "non-boolean condition", body: `if title {}`, code: "[non-boolean-condition]"},
		{name: "presence of a required input", body: `if title? {}`, code: "[presence-requires-optional]"},
		{name: "unnarrowed optional read", body: `p "{note}"`, code: "[optional-value-access]"},
		{name: "unknown slot", body: "Box {\n\t\tslot sidebar {}\n\t}", code: "[unknown-slot]"},
		{name: "missing content", body: `Modal`, code: "[missing-content]"},
		{name: "detached element body", body: "div\n\t{}", code: "[detached-element-body]"},
		{name: "string in a script", body: `script "go({title})"`, code: "[script-body-value]"},
		{name: "script end tag", body: "script ```</script>```", code: "[raw-text-end-tag]"},
		{name: "style text", body: `style "a > b { }"`, code: "[raw-text-body]"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			source := "Box [extra?: content] { div { extra } }\nModal [children: content] { section { children } }\nList [title: string, note?: string] {\n\t" + test.body + "\n}\n"
			if err := os.WriteFile("list.ktml", []byte(source), 0o600); err != nil {
				t.Fatal(err)
			}
			err := generateFile("list.ktml", false)
			if err == nil || !strings.Contains(err.Error(), test.code) || !strings.Contains(err.Error(), "list.ktml") {
				t.Fatalf("generateFile() error = %v, want %s", err, test.code)
			}
			entries, readErr := os.ReadDir(".")
			if readErr != nil {
				t.Fatal(readErr)
			}
			if len(entries) != 1 {
				t.Errorf("generation wrote %d entries, want only the source", len(entries)-1)
			}
		})
	}
}
