package gogen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/generator/gogen"
)

// generateWorkspaceSource generates one component document through a
// workspace snapshot, so contextual input types resolve.
func generateWorkspaceSource(t *testing.T, source string) []byte {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "input.ktml")
	if err := os.WriteFile(file, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := compiler.NewWorkspace(dir).Index()
	if err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	if err := snapshot.Err(); err != nil {
		t.Fatalf("diagnostics: %v", err)
	}
	generated, err := gogen.GenerateFile(snapshot, file, gogen.Options{Package: "generated"})
	if err != nil {
		t.Fatalf("GenerateFile() error = %v", err)
	}
	return generated
}

func TestGenerate_GIVENScriptAttributes_WHENRendered_THENEscapesTrustedScriptJSONAndBooleansForTheAttribute(t *testing.T) {
	generated := generateWorkspaceSource(t, `Panel [open: boolean, handler: TrustedScript, state: JSONValue] {
	div (
		x-data="{state}"
		@click="{handler}"
		:disabled="{open}"
		x-show="!{open} && ready"
		x-init="boot({state}); {handler}"
		onclick="go('static')"
		data-config="{state}"
		hidden="{open}"
	) {}
}`)
	if !strings.Contains(string(generated), "rt.ScriptAttr(") || !strings.Contains(string(generated), "rt.ScriptBool(") || !strings.Contains(string(generated), "rt.JSONAttr(") {
		t.Errorf("generated source lacks the script attribute escapers:\n%s", generated)
	}
	compileGenerated(t, generated, `package generated

import (
	"context"
	"strings"
	"testing"

	"github.com/karl-tye/ktml"
)

func TestScriptAttributes(t *testing.T) {
	state := ktml.TrustJSON(`+"`"+`{"q":"\"><script>x</script>","a":'b'}`+"`"+`)
	handler := ktml.TrustScript(`+"`"+`select("a<b", 'c') && x > 1`+"`"+`)
	for _, open := range []bool{true, false} {
		var output strings.Builder
		if err := (Panel{Open: open, Handler: handler, State: state}).Render(context.Background(), &output); err != nil { t.Fatal(err) }
		literal := "false"
		hidden := ""
		if open {
			literal, hidden = "true", " hidden"
		}
		want := "<div" +
			" x-data=\"{&#34;q&#34;:&#34;\\&#34;&gt;&lt;script&gt;x&lt;/script&gt;&#34;,&#34;a&#34;:&#39;b&#39;}\"" +
			" @click=\"select(&#34;a&lt;b&#34;, &#39;c&#39;) &amp;&amp; x &gt; 1\"" +
			" :disabled=\"" + literal + "\"" +
			" x-show=\"!" + literal + " &amp;&amp; ready\"" +
			" x-init=\"boot({&#34;q&#34;:&#34;\\&#34;&gt;&lt;script&gt;x&lt;/script&gt;&#34;,&#34;a&#34;:&#39;b&#39;}); select(&#34;a&lt;b&#34;, &#39;c&#39;) &amp;&amp; x &gt; 1\"" +
			" onclick=\"go(&#39;static&#39;)\"" +
			" data-config=\"{&#34;q&#34;:&#34;\\&#34;&gt;&lt;script&gt;x&lt;/script&gt;&#34;,&#34;a&#34;:&#39;b&#39;}\"" +
			hidden + "></div>"
		if got := output.String(); got != want {
			t.Fatalf("open=%v rendered\n%s\nwant\n%s", open, got, want)
		}
		if strings.Contains(output.String(), "<script") || strings.Contains(output.String(), "'") {
			t.Fatalf("unescaped markup: %s", output.String())
		}
	}
}
`)
}

func TestGenerate_GIVENStringInScriptAttributeWithoutSnapshot_WHENGenerated_THENFailsBeforeOutput(t *testing.T) {
	result := compiler.CompileSyntax("input.ktml", []byte(`Panel [name: string] {
	button @click="select('{name}')"
}`))
	if result.HasErrors() {
		t.Fatalf("CompileSyntax() diagnostics = %#v", result.Diagnostics)
	}
	generated, err := gogen.Generate(result.Document, gogen.Options{Package: "generated"})
	if err == nil || !strings.Contains(err.Error(), "script attribute @click") {
		t.Fatalf("Generate() = %s, %v; want a script attribute error", generated, err)
	}
}
