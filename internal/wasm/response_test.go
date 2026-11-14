package wasm_test

import (
	"os"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/buildstate"
	"github.com/karl-tye/ktml/internal/wasm"
)

func TestProcessPluginResponse(t *testing.T) {
	t.Run("GIVEN a successful plugin execution that returns a valid structured response containing file content and a target destination path WHEN the system processes the plugin response THEN the file content is successfully written and saved to the specified target destination path on the local file system", func(t *testing.T) {
		tempDir := t.TempDir()
		t.Chdir(tempDir)
		targetPath := "output.html"
		jsonResponse := []byte(`{"content":"<h1>Hello</h1>","output_path":"` + strings.ReplaceAll(targetPath, "\\", "\\\\") + `","errors":[]}`)

		err := wasm.ProcessPluginResponse(&buildstate.Writer{Name: "html", Roots: []buildstate.OutputRoot{{Dir: ".", Suffixes: []string{".html"}}}}, jsonResponse)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		content, err := os.ReadFile(targetPath)
		if err != nil {
			t.Fatalf("failed to read written file: %v", err)
		}

		if string(content) != "<h1>Hello</h1>" {
			t.Errorf("expected <h1>Hello</h1>, got %s", string(content))
		}
	})
	t.Run("GIVEN a response targeting a path outside the plugin's declared outputs WHEN processed THEN it is refused naming the plugin and the path", func(t *testing.T) {
		t.Chdir(t.TempDir())
		if err := os.WriteFile("ktml.config.json", []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, target := range []string{"ktml.config.json", "page.go"} {
			err := wasm.ProcessPluginResponse(&buildstate.Writer{Name: "html", Roots: []buildstate.OutputRoot{{Dir: ".", Suffixes: []string{".html"}}}}, []byte(`{"content":"x","output_path":"`+target+`"}`))
			if err == nil || !strings.Contains(err.Error(), `plugin "html"`) || !strings.Contains(err.Error(), target) {
				t.Fatalf("ProcessPluginResponse(%s) error = %v; want a refusal naming the plugin and path", target, err)
			}
		}
		if content, _ := os.ReadFile("ktml.config.json"); string(content) != "{}" {
			t.Fatalf("ktml.config.json = %q; want it unchanged", content)
		}
	})
}
