package e2e_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestNeovim_GIVENThePlugin_WHENAKTMLFileOpens_THENTheParserQueriesAndLanguageServerApply
// runs editors/nvim in a clean headless Neovim: the tree-sitter parser
// loads, highlights, injections, folds, and locals apply, and the real ktml
// lsp attaches in the project root.
func TestNeovim_GIVENThePlugin_WHENAKTMLFileOpens_THENTheParserQueriesAndLanguageServerApply(t *testing.T) {
	if _, err := exec.LookPath("nvim"); err != nil {
		t.Skip("skipping: nvim is not installed")
	}
	repository, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	prepareBinaries(t)
	parser := filepath.Join(t.TempDir(), "ktml.so")
	grammar := filepath.Join(repository, "tree-sitter-ktml")
	var build *exec.Cmd
	if _, err := exec.LookPath("tree-sitter"); err == nil {
		build = exec.Command("tree-sitter", "build", "-o", parser, grammar)
	} else if _, err := exec.LookPath("cc"); err == nil {
		build = exec.Command("cc", "-shared", "-fPIC", "-O2", "-I", filepath.Join(grammar, "src"), filepath.Join(grammar, "src", "parser.c"), filepath.Join(grammar, "src", "scanner.c"), "-o", parser)
	} else {
		t.Skip("skipping: neither the tree-sitter CLI nor cc is installed to build the parser")
	}
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build parser: %v\n%s", err, output)
	}

	// The project is a temporary directory with the repository's plugins.
	project := t.TempDir()
	config := `{
  "plugin": {
    "gohtml": {"path": "` + filepath.Join(repository, "plugins", "gohtml.wasm") + `", "allow_read": ["."]},
    "alpine": {"path": "` + filepath.Join(repository, "plugins", "alpine.wasm") + `", "allow_read": ["."]}
  },
  "resolution": {"lsp": ["gohtml", "alpine"]}
}`
	writeFile(t, filepath.Join(project, "ktml.config.json"), config)
	writeFile(t, filepath.Join(project, "sample.ktml"), string(readFile(t, filepath.Join("testdata", "nvim", "sample.ktml"))))
	binary, err := filepath.Abs("ktml")
	if err != nil {
		t.Fatal(err)
	}
	check, err := filepath.Abs(filepath.Join("testdata", "nvim", "check.lua"))
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "result.json")
	command := exec.Command("nvim", "--clean", "--headless", "-c", "luafile "+check)
	command.Dir = project
	command.Env = append(os.Environ(),
		"KTML_NVIM="+filepath.Join(repository, "editors", "nvim"),
		"KTML_BIN="+binary,
		"KTML_PARSER="+parser,
		"KTML_FILE="+filepath.Join(project, "sample.ktml"),
		"KTML_OUT="+out,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("nvim: %v\n%s", err, output)
	}
	var result struct {
		Error       string          `json:"error"`
		Filetype    string          `json:"filetype"`
		Parser      bool            `json:"parser"`
		HasError    bool            `json:"has_error"`
		Highlighter bool            `json:"highlighter"`
		Import      []string        `json:"import"`
		Element     []string        `json:"element"`
		Condition   []string        `json:"condition"`
		Injections  map[string]int  `json:"injections"`
		Locals      []string        `json:"locals"`
		Queries     map[string]bool `json:"queries"`
		Fold        string          `json:"fold"`
		Foldmethod  string          `json:"foldmethod"`
		LSPAttached bool            `json:"lsp_attached"`
		LSPRoot     string          `json:"lsp_root"`
		Symbols     []string        `json:"symbols"`
	}
	if err := json.Unmarshal(readFile(t, out), &result); err != nil {
		t.Fatalf("decode %s: %v", readFile(t, out), err)
	}
	if result.Error != "" {
		t.Fatalf("check failed: %s", result.Error)
	}
	if result.Filetype != "ktml" || !result.Parser || result.HasError || !result.Highlighter {
		t.Errorf("filetype %q, parser %v, parse error %v, highlighter %v; want ktml, a clean parse, and highlighting", result.Filetype, result.Parser, result.HasError, result.Highlighter)
	}
	for name, captures := range map[string]struct {
		got  []string
		want string
	}{
		"import":    {result.Import, "keyword.import"},
		"element":   {result.Element, "tag"},
		"condition": {result.Condition, "keyword.conditional"},
	} {
		if !slices.Contains(captures.got, captures.want) {
			t.Errorf("%s captures = %v, want %s", name, captures.got, captures.want)
		}
	}
	// x-data and @click are JavaScript; hx-vals and the JSON script are
	// JSON, and the JSON script is not also JavaScript; style is CSS.
	for language, want := range map[string]int{"javascript": 2, "json": 2, "css": 1} {
		if got := result.Injections[language]; got != want {
			t.Errorf("%s injections = %d, want %d (all: %v)", language, got, want, result.Injections)
		}
	}
	for _, want := range []string{"local.definition.parameter:title", "local.definition.var:item"} {
		if !slices.Contains(result.Locals, want) {
			t.Errorf("locals = %v, want %s", result.Locals, want)
		}
	}
	for _, name := range []string{"highlights", "injections", "folds", "indents", "locals"} {
		if !result.Queries[name] {
			t.Errorf("query %s did not load", name)
		}
	}
	if !strings.HasPrefix(result.Fold, ">") || result.Foldmethod != "expr" {
		t.Errorf("fold at line 5 = %q with foldmethod %q, want a fold start with expr folding", result.Fold, result.Foldmethod)
	}
	if !result.LSPAttached || result.LSPRoot != project {
		t.Fatalf("ktml lsp attached %v at %q, want %q", result.LSPAttached, result.LSPRoot, project)
	}
	if !slices.Contains(result.Symbols, "Card") {
		t.Errorf("document symbols = %v, want Card", result.Symbols)
	}
}
