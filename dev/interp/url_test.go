package interp_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/dev/interp"
)

func TestNew_GIVENStringInURLAttribute_WHENCompiled_THENFailsWithURLDiagnostic(t *testing.T) {
	for _, body := range []string{`a href="{name}"`, `form action="{name}"`, `img src="{name}"`, `a href="{name}/x"`, `a href="/{name}"`} {
		t.Run(body, func(t *testing.T) {
			root, _, snapshot := indexWorkspace(t, map[string]string{"greeting.ktml": "Greeting [name: string] {\n\t" + body + "\n}\n"})
			var codes []string
			for _, diagnostic := range snapshot.Diagnostics(filepath.Join(root, "greeting.ktml")) {
				codes = append(codes, diagnostic.Code)
			}
			if strings.Join(codes, ",") != "url-attribute-value" {
				t.Fatalf("diagnostics = %v, want url-attribute-value", codes)
			}
			if program, err := interp.New(snapshot, testOptions); err == nil || program != nil {
				t.Fatalf("New() = %v, %v; want an error", program, err)
			}
		})
	}
}

func TestRender_GIVENJavaScriptAfterASafeURLPrefix_WHENRendered_THENStaysInThePath(t *testing.T) {
	_, _, snapshot := indexWorkspace(t, map[string]string{"greeting.ktml": `Greeting [name: string] {
	a href="/people/{name}?q={name}" "x"
}
`})
	program, err := interp.New(snapshot, testOptions)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	output, err := renderString(t, program, Greeting{Name: `javascript:alert("1")`})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if want := `<a href="/people/javascript:alert(&#34;1&#34;)?q=javascript:alert(&#34;1&#34;)">x</a>`; output != want {
		t.Errorf("output = %q, want %q", output, want)
	}
}
