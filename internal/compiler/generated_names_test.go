package compiler_test

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
)

func TestWorkspace_GIVENDeclarationsWithOneGeneratedGoName_WHENLoaded_THENReportsBothDeclarations(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string]string
		markers []string // file:text of each reported range
	}{
		{
			name:    "case-only component names",
			files:   map[string]string{"rows.ktml": "Row { div }\nrow { span }\n"},
			markers: []string{"rows.ktml:Row", "rows.ktml:row"},
		},
		{
			name:    "same component name in two files of a package",
			files:   map[string]string{"a.ktml": "Row { div }\n", "b.ktml": "Row { span }\n"},
			markers: []string{"a.ktml:Row", "b.ktml:Row"},
		},
		{
			name:    "component named after another's props",
			files:   map[string]string{"a.ktml": "Page { div }\nPageProps { span }\n"},
			markers: []string{"a.ktml:Page", "a.ktml:PageProps"},
		},
		{
			name:    "component named after another's render function",
			files:   map[string]string{"a.ktml": "Page { div }\nRenderPage { span }\n"},
			markers: []string{"a.ktml:Page", "a.ktml:RenderPage"},
		},
		{
			name:    "record collides with component",
			files:   map[string]string{"a.ktmlschema": "record Card []", "card.ktml": "import { Card } from \"./a.ktmlschema\"\nCard [c: Card] { div }"},
			markers: []string{"a.ktmlschema:Card", "card.ktml:Card"},
		},
		{
			name:    "state variants collide with a record",
			files:   map[string]string{"a.ktmlschema": "record PayDue []\nstate Pay {\n\tDue [at: string]\n}\n"},
			markers: []string{"a.ktmlschema:Pay", "a.ktmlschema:PayDue"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := writeWorkspace(t, test.files)
			snapshot, err := compiler.NewWorkspace(dir).Index()
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for name := range test.files {
				for _, diagnostic := range snapshot.Diagnostics(filepath.Join(dir, name)) {
					if diagnostic.Code != "generated-name-collision" {
						t.Errorf("unexpected diagnostic %s: %s", diagnostic.Code, diagnostic.Message)
						continue
					}
					got = append(got, name+":"+test.files[name][diagnostic.Start:diagnostic.End])
				}
			}
			sort.Strings(got)
			if strings.Join(got, ",") != strings.Join(test.markers, ",") {
				t.Fatalf("collisions = %v, want %v", got, test.markers)
			}
			if snapshot.Valid() {
				t.Fatal("snapshot is valid")
			}
		})
	}
}
