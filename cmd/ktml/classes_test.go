package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeClassesProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"ui/card.ktml":                  "Card [on: boolean, color: string] {\n\tdiv (class=\"p-4 flex\" if on { class=\"ring-2\" }) {\n\t\tp class=\"text-{color}-500 font-bold\" \"x\"\n\t}\n}\n",
		"web/page.ktml":                 "Page [] {\n\tmain class=\"flex min-h-screen\" \"y\"\n}\n",
		"node_modules/pkg/skip.ktml":    "Skip [] {\n\tp class=\"skipped\" \"z\"\n}\n",
		".hidden/skip.ktml":             "Skip [] {\n\tp class=\"hidden-dir\" \"z\"\n}\n",
		"ui/notes.txt":                  "class=\"not-ktml\"\n",
		"testdata/fixture/fixture.ktml": "Skip [] {\n\tp class=\"fixture\" \"z\"\n}\n",
	}
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestClassesCmd_GIVENAProject_WHENListed_THENPrintsEachClassOnceInOrder(t *testing.T) {
	root := writeClassesProject(t)
	var stdout, stderr bytes.Buffer
	if code := classesCmd(&stdout, &stderr, []string{root}); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if got, want := stdout.String(), "flex\nfont-bold\nmin-h-screen\np-4\nring-2\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestClassesCmd_GIVENAnOutputFile_WHENUnchanged_THENLeavesTheFileUntouched(t *testing.T) {
	root := writeClassesProject(t)
	out := filepath.Join(t.TempDir(), "ktml-classes.txt")
	var stdout, stderr bytes.Buffer
	if code := classesCmd(&stdout, &stderr, []string{"-o", out, root}); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "flex\nfont-bold\nmin-h-screen\np-4\nring-2\n"; got != want {
		t.Errorf("file = %q, want %q", got, want)
	}
	// Tailwind's watcher rebuilds on every write, so an unchanged list must
	// not be rewritten.
	old := time.Unix(1_000_000, 0)
	if err := os.Chtimes(out, old, old); err != nil {
		t.Fatal(err)
	}
	if code := classesCmd(&stdout, &stderr, []string{"-o", out, root}); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(old) {
		t.Errorf("unchanged class list was rewritten")
	}
}

func TestClassesCmd_GIVENCheck_WHENAClassIsBuiltFromParts_THENReportsItAndExitsOne(t *testing.T) {
	root := writeClassesProject(t)
	var stdout, stderr bytes.Buffer
	code := classesCmd(&stdout, &stderr, []string{"-check", root})
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr = %s", code, stderr.String())
	}
	want := filepath.Join(root, "ui", "card.ktml") + ":3:12: [tailwind-dynamic-class] class text-{color}-500 is built from {color}, so Tailwind cannot find it; write whole class names, for example in the presenter, or list them with @source inline(...)\n"
	if got := stdout.String(); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestClassesCmd_GIVENJSON_THENListsTokensAndDynamicConstructionPerFile(t *testing.T) {
	root := writeClassesProject(t)
	var stdout, stderr bytes.Buffer
	if code := classesCmd(&stdout, &stderr, []string{"-json", root}); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	var files []struct {
		Path   string `json:"path"`
		Tokens []struct {
			Class       string `json:"class"`
			Line        int    `json:"line"`
			Column      int    `json:"column"`
			Conditional bool   `json:"conditional"`
		} `json:"tokens"`
		Dynamic []struct {
			Text    string `json:"text"`
			Partial bool   `json:"partial"`
		} `json:"dynamic"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &files); err != nil {
		t.Fatalf("json: %v\n%s", err, stdout.String())
	}
	if len(files) != 2 || files[0].Path != filepath.Join(root, "ui", "card.ktml") {
		t.Fatalf("files = %+v", files)
	}
	card := files[0]
	if len(card.Tokens) != 4 || card.Tokens[2].Class != "ring-2" || !card.Tokens[2].Conditional || card.Tokens[2].Line != 2 {
		t.Errorf("card tokens = %+v", card.Tokens)
	}
	if len(card.Dynamic) != 1 || card.Dynamic[0].Text != "text-{color}-500" || !card.Dynamic[0].Partial {
		t.Errorf("card dynamic = %+v", card.Dynamic)
	}
}
