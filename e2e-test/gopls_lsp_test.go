package e2e_test

import (
	"encoding/json"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// copyReviewsModule copies the gopls adapter's reviews fixture, the
// bookshop's reader reviews dashboard, into a module that replaces
// github.com/karl-tye/ktml with this repository.
func copyReviewsModule(t *testing.T, repository string) string {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(repository, "internal", "lsp", "testdata", "gopls", "reviews")
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dir, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if relative == "README.md" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if relative == "go.mod.txt" {
			target = filepath.Join(dir, "go.mod")
			data = append(data, []byte("\nreplace github.com/karl-tye/ktml => "+filepath.ToSlash(repository)+"\n")...)
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func runIn(t *testing.T, dir string, name string, args ...string) (string, error) {
	t.Helper()
	command := exec.Command(name, args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	return string(output), err
}

// lineOf returns the one-based line of the first occurrence of text.
func lineOf(t *testing.T, path, text string) int {
	t.Helper()
	content := string(readFile(t, path))
	index := strings.Index(content, text)
	if index < 0 {
		t.Fatalf("%s lacks %q", path, text)
	}
	return strings.Count(content[:index], "\n") + 1
}

// generatedPosition matches an error reported at a generated Go file.
var generatedPosition = regexp.MustCompile(`(?m)^(vet: )?\S*_ktml(schema|_dev)?\.go:\d+`)

// TestGeneratedGo_GIVENTheReviewsModule_WHENAPresenterBreaksTheBuild_THENGoReportsTheKTMLLine
// generates the reviews fixture with ktml generate and builds it
// with the Go toolchain. A presenter method that shadows a record field
// breaks the generated text write, and go build and go vet name the .ktml
// line, in the production and ktmldev variants, before and after an edit
// that moves the line.
func TestGeneratedGo_GIVENTheReviewsModule_WHENAPresenterBreaksTheBuild_THENGoReportsTheKTMLLine(t *testing.T) {
	prepareBinaries(t)
	repository, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := copyReviewsModule(t, repository)
	binary := filepath.Join(repository, "e2e-test", "ktml")
	generate := func() {
		t.Helper()
		if output, err := runIn(t, dir, binary, "generate", "./..."); err != nil {
			t.Fatalf("ktml generate: %v\n%s", err, output)
		}
	}
	generate()
	for _, args := range [][]string{{"build", "./..."}, {"vet", "./..."}, {"build", "-tags", "ktmldev", "./..."}} {
		if output, err := runIn(t, dir, "go", args...); err != nil {
			t.Fatalf("go %s on the reviews fixture: %v\n%s", strings.Join(args, " "), err, output)
		}
	}
	if output, err := runIn(t, dir, "gofmt", "-l", "."); err != nil || strings.TrimSpace(output) != "" {
		t.Fatalf("generated Go is not gofmt-clean: %v\n%s", err, output)
	}

	presenter := filepath.Join(dir, "internal", "reviews", "ui_present.go")
	writeFile(t, presenter, string(readFile(t, presenter))+"\nfunc (v DashboardView) Month() string { return \"\" }\n")
	document := filepath.Join(dir, "internal", "reviews", "ui.ktml")
	check := func(line int) {
		t.Helper()
		want := "internal/reviews/ui.ktml:" + strconv.Itoa(line) + ":"
		for _, args := range [][]string{{"build", "./..."}, {"vet", "./internal/reviews"}, {"build", "-tags", "ktmldev", "./..."}} {
			output, err := runIn(t, dir, "go", args...)
			if err == nil {
				t.Fatalf("go %s succeeded with the presenter error", strings.Join(args, " "))
			}
			// go vet stops at the first type error, the presenter's own.
			if args[0] == "build" && (!strings.Contains(output, want) || !strings.Contains(output, "props.View.Month")) {
				t.Errorf("go %s does not report the error at %s:\n%s", strings.Join(args, " "), want, output)
			}
			if generatedPosition.MatchString(output) {
				t.Errorf("go %s reports a generated-file position:\n%s", strings.Join(args, " "), output)
			}
		}
	}
	check(lineOf(t, document, "Reader Reviews - {view.month}"))

	// Regenerating after an edit moves the reported line with the source.
	writeFile(t, document, strings.Replace(string(readFile(t, document)), "// Page is the reviews page.", "// Page is the reviews page.\n//\n// It renders the dashboard.", 1))
	generate()
	check(lineOf(t, document, "Reader Reviews - {view.month}"))
}

// TestLSP_GIVENTheReviewsModule_WHENAPresenterBreaksTheGeneratedGo_THENKtmlLSPShowsItOnTheKTMLLine
// runs ktml lsp with the gohtml plugin and gopls over the reviews
// fixture: the presenter error shows on the .ktml line, and definition of a
// Host Schema field reaches both its schema declaration and the presenter.
func TestLSP_GIVENTheReviewsModule_WHENAPresenterBreaksTheGeneratedGo_THENKtmlLSPShowsItOnTheKTMLLine(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls is not installed; install it with go install golang.org/x/tools/gopls@v0.23.0")
	}
	prepareBinaries(t)
	repository, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := copyReviewsModule(t, repository)
	writeFile(t, filepath.Join(dir, "ktml.config.json"), `{
  "plugin": {"gohtml": {"path": `+strconv.Quote(filepath.Join(repository, "plugins", "gohtml.wasm"))+`, "allow_read": ["."]}},
  "resolution": {"lsp": ["gohtml"]}
}`)
	presenter := filepath.Join(dir, "internal", "reviews", "ui_present.go")
	writeFile(t, presenter, string(readFile(t, presenter))+"\nfunc (v DashboardView) Month() string { return \"\" }\n")

	client, uri, text := openReviews(t, dir)
	awaitMonthError(t, client, uri, text)

	// The Month method shadows the month field in Go, so navigate from
	// another field of the record.
	definition := client.request("textDocument/definition", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": gatePosition(t, text, "{view.year}", 6)})
	encoded, _ := json.Marshal(definition)
	if !strings.Contains(string(encoded), "reviews.ktmlschema") || !strings.Contains(string(encoded), "ui_present.go") {
		t.Errorf("definition of view.year = %s; want the schema field and the presenter", encoded)
	}
}

// openReviews starts ktml lsp in the reviews module dir and opens ui.ktml.
func openReviews(t *testing.T, dir string) (*gateClient, string, string) {
	t.Helper()
	client := startGateClient(t, dir)
	root := (&url.URL{Scheme: "file", Path: dir}).String()
	client.request("initialize", map[string]any{"rootUri": root, "capabilities": map[string]any{}})
	client.send(map[string]any{"method": "initialized", "params": map[string]any{}})
	path := filepath.Join(dir, "internal", "reviews", "ui.ktml")
	uri := (&url.URL{Scheme: "file", Path: path}).String()
	text := string(readFile(t, path))
	client.send(map[string]any{"method": "textDocument/didOpen", "params": map[string]any{
		"textDocument": map[string]any{"uri": uri, "languageId": "ktml", "version": 1, "text": text},
	}})
	return client, uri, text
}

// awaitMonthError waits for the presenter's Month error on the .ktml line
// that renders {view.month}.
func awaitMonthError(t *testing.T, client *gateClient, uri, text string) {
	t.Helper()
	want := gatePosition(t, text, "{view.month}", 0)
	deadline := time.Now().Add(time.Minute)
	var found map[string]any
	for found == nil && time.Now().Before(deadline) {
		client.waitDiagnostics(uri, func(keys []string) bool {
			for _, key := range keys {
				if strings.HasPrefix(key, "gopls:") {
					return true
				}
			}
			return false
		})
		client.mu.Lock()
		for _, diagnostic := range client.diagnostics[uri] {
			if message, _ := diagnostic["message"].(string); diagnostic["source"] == "gopls" && strings.Contains(message, "props.View.Month") {
				found = diagnostic
			}
		}
		client.mu.Unlock()
	}
	if found == nil {
		t.Fatalf("ktml lsp did not report the presenter error on ui.ktml; diagnostics: %v", client.diagnostics[uri])
	}
	start := found["range"].(map[string]any)["start"].(map[string]any)
	if start["line"] != float64(want["line"].(int)) || start["character"] != float64(want["character"].(int)) {
		t.Errorf("gopls diagnostic starts at %v; want {view.month} at %v", start, want)
	}
}

// goplsRuntimeDir is the run's XDG_RUNTIME_DIR, where -remote=auto puts
// the gopls daemon's socket.
var goplsRuntimeDir string

// goplsDaemons returns the gopls daemons listening on a socket in dir.
func goplsDaemons(dir string) map[int]string {
	entries, _ := os.ReadDir("/proc")
	daemons := make(map[int]string)
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil {
			continue
		}
		args := strings.Split(string(cmdline), "\x00")
		if !strings.Contains(args[0], "gopls") {
			continue
		}
		for index, arg := range args {
			listen, ok := strings.CutPrefix(arg, "-listen=")
			if !ok && arg == "-listen" && index+1 < len(args) {
				listen, ok = args[index+1], true
			}
			if ok && strings.HasPrefix(listen, "unix;"+dir+"/") {
				daemons[pid] = listen
				break
			}
		}
	}
	return daemons
}

// stopGoplsDaemons stops the daemons listening in dir with their process
// groups: gopls starts an auto daemon as the leader of its own session.
func stopGoplsDaemons(dir string) {
	for pid := range goplsDaemons(dir) {
		if group, err := syscall.Getpgid(pid); err == nil && group == pid {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		} else {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(goplsDaemons(dir)) > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
}

// TestLSP_GIVENTwoKtmlLSPProcesses_WHENEachOpensTheReviewsModule_THENTheyShareOneGoplsDaemon
// runs two ktml lsp processes with the default gopls configuration
// (remote "auto"), each over its own copy of the reviews module: both show
// the presenter error on the .ktml line, through one gopls daemon with a
// session for each.
func TestLSP_GIVENTwoKtmlLSPProcesses_WHENEachOpensTheReviewsModule_THENTheyShareOneGoplsDaemon(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls is not installed; install it with go install golang.org/x/tools/gopls@v0.23.0")
	}
	prepareBinaries(t)
	repository, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopGoplsDaemons(goplsRuntimeDir) })
	type session struct {
		client    *gateClient
		uri, text string
	}
	var sessions []session
	for range 2 {
		dir := copyReviewsModule(t, repository)
		writeFile(t, filepath.Join(dir, "ktml.config.json"), `{
  "plugin": {"gohtml": {"path": `+strconv.Quote(filepath.Join(repository, "plugins", "gohtml.wasm"))+`, "allow_read": ["."]}},
  "resolution": {"lsp": ["gohtml"]}
}`)
		presenter := filepath.Join(dir, "internal", "reviews", "ui_present.go")
		writeFile(t, presenter, string(readFile(t, presenter))+"\nfunc (v DashboardView) Month() string { return \"\" }\n")
		client, uri, text := openReviews(t, dir)
		sessions = append(sessions, session{client, uri, text})
	}
	for _, session := range sessions {
		awaitMonthError(t, session.client, session.uri, session.text)
	}

	daemons := goplsDaemons(goplsRuntimeDir)
	if len(daemons) != 1 {
		t.Fatalf("gopls daemons in %s = %v; want one shared daemon", goplsRuntimeDir, daemons)
	}
	for _, listen := range daemons {
		output, err := exec.Command("gopls", "-remote="+listen, "remote", "sessions").Output()
		if err != nil {
			t.Fatalf("gopls remote sessions: %v", err)
		}
		var state struct {
			Clients []json.RawMessage `json:"clients"`
		}
		if err := json.Unmarshal(output, &state); err != nil {
			t.Fatalf("gopls remote sessions: %v: %s", err, output)
		}
		// The query is a session too.
		if got := len(state.Clients) - 1; got != 2 {
			t.Errorf("daemon sessions = %d; want one for each ktml lsp", got)
		}
	}
}
