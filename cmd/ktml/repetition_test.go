package main

import (
	"os"
	"strings"
	"testing"
)

func TestGenerateFile_GIVENInvalidKeyedRepetition_WHENGenerated_THENReportsTheDiagnosticAndWritesNothing(t *testing.T) {
	tests := []struct{ name, body, code string }{
		{name: "key not rooted", body: `for item in items key other {}`, code: "[key-not-rooted]"},
		{name: "not a collection", body: `for item in title key item {}`, code: "[repetition-not-collection]"},
		{name: "number key", body: `for item in scores key item {}`, code: "[invalid-key-type]"},
		{name: "optional collection", body: `for item in maybe key item {}`, code: "[optional-collection]"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			source := "List [items: [string], scores: [number], maybe?: [string], title: string, other: string] {\n\t" + test.body + "\n}\n"
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
