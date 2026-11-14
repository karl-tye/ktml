package lsp

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/config"
)

// goplsRuntimeDir is where -remote=auto puts the gopls daemon's socket
// during these tests: a short directory of its own, so that tests never
// join the user's own daemon, and every daemon they start is found and
// stopped. Unix socket paths are limited to about 100 bytes.
var goplsRuntimeDir string

// goplsRuntimeDirEnv hands the directory to the helper processes tests
// start from this binary, which run TestMain too: the parent owns it.
const goplsRuntimeDirEnv = "KTML_TEST_GOPLS_RUNTIME_DIR"

func TestMain(m *testing.M) {
	if dir := os.Getenv(goplsRuntimeDirEnv); dir != "" {
		goplsRuntimeDir = dir
		os.Exit(m.Run())
	}
	dir, err := os.MkdirTemp("", "ktmlg")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	goplsRuntimeDir = dir
	_ = os.Setenv("XDG_RUNTIME_DIR", dir)
	_ = os.Setenv(goplsRuntimeDirEnv, dir)
	code := m.Run()
	stopDaemonsIn(dir)
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// daemonsIn returns the gopls daemons listening on a socket in dir: the
// processes started for these tests, and no others.
func daemonsIn(dir string) []int {
	entries, _ := os.ReadDir("/proc")
	var pids []int
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
				pids = append(pids, pid)
				break
			}
		}
	}
	return pids
}

// stopDaemonsIn stops the daemons listening in dir, with their process
// groups: gopls starts an auto daemon as the leader of its own session.
func stopDaemonsIn(dir string) {
	for _, pid := range daemonsIn(dir) {
		if group, err := syscall.Getpgid(pid); err == nil && group == pid {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		} else {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(daemonsIn(dir)) > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
}

// startGoplsDaemon starts a gopls daemon on a new socket and stops it when
// the test ends. It returns the socket's remote address and a function
// that stops the daemon early.
func startGoplsDaemon(t *testing.T) (string, func()) {
	t.Helper()
	dir, err := os.MkdirTemp(goplsRuntimeDir, "d")
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "gopls.sock")
	command := exec.Command("gopls", "serve", "-listen=unix;"+socket, "-listen.timeout=1m")
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
			_ = command.Wait()
			_ = os.RemoveAll(dir)
		})
	}
	t.Cleanup(stop)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("gopls daemon did not listen on %s", socket)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return "unix;" + socket, stop
}

// goplsSessions returns the number of sessions the daemon at remote has,
// not counting the query's own.
func goplsSessions(t *testing.T, remote string) int {
	t.Helper()
	output, err := exec.Command("gopls", "-remote="+remote, "remote", "sessions").Output()
	if err != nil {
		t.Fatalf("gopls remote sessions: %v", err)
	}
	var state struct {
		Clients []json.RawMessage `json:"clients"`
	}
	if err := json.Unmarshal(output, &state); err != nil {
		t.Fatalf("gopls remote sessions: %v: %s", err, output)
	}
	return len(state.Clients) - 1
}

func goplsHostWith(t *testing.T, root string, settings *config.Gopls) (*tailwindClient, *GoplsAdapter) {
	t.Helper()
	adapter := NewGoplsAdapter(root, settings)
	client := startTailwindHost(t, adapter)
	client.request("initialize", map[string]any{"capabilities": map[string]any{}})
	client.send(map[string]any{"method": "initialized", "params": map[string]any{}})
	return client, adapter
}

func TestGoplsDaemon_GIVENTwoKTMLSessionsAndOneDaemon_WHENEachOpensADocument_THENBothGetMappedDiagnostics(t *testing.T) {
	requireGopls(t)
	remote, _ := startGoplsDaemon(t)
	first := viewsModule(t, map[string]string{"views/card.go": "package views\n\ntype Card struct{}\n"})
	second := viewsModule(t, map[string]string{"views/title.go": "package views\n\nfunc (v CardView) Title() string { return \"\" }\n"})
	firstClient, firstAdapter := goplsHostWith(t, first, &config.Gopls{Remote: remote})
	secondClient, secondAdapter := goplsHostWith(t, second, &config.Gopls{Remote: remote})
	firstClient.open(filepath.Join(first, "views", "view.ktml"))
	secondClient.open(filepath.Join(second, "views", "view.ktml"))

	diagnostics := firstClient.awaitDiagnostics(filepath.Join(first, "views", "view.ktml"), hasDiagnostic("Card redeclared in this block"))
	diagnostic, _ := diagnosticAt(diagnostics, "Card redeclared")
	if line, column := rangeStart(diagnostic); line != 2 || column != 0 {
		t.Errorf("first session: diagnostic at %d:%d; want 2:0", line, column)
	}
	diagnostics = secondClient.awaitDiagnostics(filepath.Join(second, "views", "view.ktml"), hasDiagnostic("cannot use props.View.Title"))
	diagnostic, _ = diagnosticAt(diagnostics, "cannot use props.View.Title")
	if line, column := rangeStart(diagnostic); line != 4 || column != 6 {
		t.Errorf("second session: diagnostic at %d:%d; want 4:6", line, column)
	}
	// Each session sees only its own module's error.
	if _, ok := diagnosticAt(secondClient.latest[fileURI(filepath.Join(second, "views", "view.ktml"))], "Card redeclared"); ok {
		t.Error("the second session reports the first session's error")
	}

	for name, adapter := range map[string]*GoplsAdapter{"first": firstAdapter, "second": secondAdapter} {
		if got := adapter.serving(); got != remote {
			t.Errorf("%s adapter serves through %q; want the daemon %q", name, got, remote)
		}
	}
	if sessions := goplsSessions(t, remote); sessions != 2 {
		t.Errorf("daemon has %d sessions; want the 2 ktml sessions", sessions)
	}
}

// TestGoplsDaemon_GIVENAKTMLSessionAndAnEditorSession_WHENKTMLOpensOverlays_THENTheEditorDoesNotSeeThem
// connects an editor-like client to the daemon ktml uses. The module has no
// generated Go on disk: ktml's overlay declares CardView, so the presenter
// type checks in ktml's session, while the editor sees the disk, where
// CardView is undeclared.
func TestGoplsDaemon_GIVENAKTMLSessionAndAnEditorSession_WHENKTMLOpensOverlays_THENTheEditorDoesNotSeeThem(t *testing.T) {
	requireGopls(t)
	remote, _ := startGoplsDaemon(t)
	root := viewsModule(t, nil)
	client, _ := goplsHostWith(t, root, &config.Gopls{Remote: remote})
	schema := filepath.Join(root, "views", "view.ktmlschema")
	client.open(schema)
	client.open(filepath.Join(root, "views", "view.ktml"))
	references := goplsPosition(fileURI(schema), 1, 2)
	references["context"] = map[string]any{"includeDeclaration": false}
	if got := locationsIn(t, client.request("textDocument/references", references)); strings.Join(got, " ") != "present.go:5:3" {
		t.Fatalf("ktml references = %v; want the presenter's assignment, through the overlay", got)
	}

	published := make(chan []string, 64)
	presenter := filepath.Join(root, "views", "present.go")
	editor, err := startChildServer("editor", []string{"gopls", "-remote=" + remote}, root, map[string]any{
		"processId":        os.Getpid(),
		"rootUri":          fileURI(root),
		"workspaceFolders": []any{map[string]any{"uri": fileURI(root), "name": "app"}},
		"capabilities":     map[string]any{"workspace": map[string]any{"configuration": true}},
	}, func(string, json.RawMessage) any { return nil }, func(method string, params json.RawMessage) {
		var report struct {
			URI         string `json:"uri"`
			Diagnostics []struct {
				Message string `json:"message"`
			} `json:"diagnostics"`
		}
		if method != publishDiagnosticsMethod || json.Unmarshal(params, &report) != nil || report.URI != fileURI(presenter) {
			return
		}
		var messages []string
		for _, diagnostic := range report.Diagnostics {
			messages = append(messages, diagnostic.Message)
		}
		published <- messages
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = editor.Close() })
	_ = editor.Notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": fileURI(presenter), "languageId": "go", "version": 1, "text": goplsPresenter}})
	deadline := time.After(time.Minute)
	var last []string
	for !strings.Contains(strings.Join(last, "\n"), "undefined: CardView") {
		select {
		case last = <-published:
		case <-deadline:
			t.Fatalf("the editor's diagnostics of present.go = %v; want undefined: CardView, as on disk", last)
		}
	}
	result, err := editor.Request(t.Context(), "textDocument/definition", goplsPosition(fileURI(presenter), 3, 42), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if string(result) != "null" && string(result) != "[]" {
		t.Errorf("the editor's definition of CardView = %s; want none: the generated declaration is ktml's overlay", result)
	}
	if sessions := goplsSessions(t, remote); sessions != 2 {
		t.Errorf("daemon has %d sessions; want ktml's and the editor's", sessions)
	}
}

func TestGoplsDaemon_GIVENAnUnreachableDaemon_WHENADocumentIsOpen_THENAPrivateGoplsMapsDiagnostics(t *testing.T) {
	requireGopls(t)
	root := viewsModule(t, map[string]string{"views/card.go": "package views\n\ntype Card struct{}\n"})
	remote := "unix;" + filepath.Join(goplsRuntimeDir, "no-such-daemon.sock")
	client, adapter := goplsHostWith(t, root, &config.Gopls{Remote: remote})
	started := time.Now()
	client.open(filepath.Join(root, "views", "view.ktml"))
	client.awaitDiagnostics(filepath.Join(root, "views", "view.ktml"), hasDiagnostic("Card redeclared in this block"))
	if elapsed := time.Since(started); elapsed > 30*time.Second {
		t.Errorf("fallback took %v", elapsed)
	}
	if got := adapter.serving(); got != "" {
		t.Errorf("adapter serves through %q; want a private gopls", got)
	}
	logged := client.notification("window/logMessage", func(map[string]any) bool { return true })
	if text, _ := logged["params"].(map[string]any)["message"].(string); !strings.Contains(text, remote) || !strings.Contains(text, "private gopls") {
		t.Errorf("log message = %q; want the daemon's address and the private fallback", text)
	}
}

func TestGoplsDaemon_GIVENADaemonThatStops_WHENTheDocumentIsEdited_THENAPrivateGoplsTakesOver(t *testing.T) {
	requireGopls(t)
	remote, stop := startGoplsDaemon(t)
	root := viewsModule(t, map[string]string{"views/card.go": "package views\n\ntype Card struct{}\n"})
	client, adapter := goplsHostWith(t, root, &config.Gopls{Remote: remote})
	document := filepath.Join(root, "views", "view.ktml")
	client.open(document)
	client.awaitDiagnostics(document, hasDiagnostic("Card redeclared in this block"))

	stop()
	moved := "// A comment moves every declaration down two lines.\n\n" + goplsComponents
	client.change(document, 2, moved)
	// The edit alone publishes "Card redeclared" on line 4: until the
	// adapter sees the forwarder exit, it remaps the daemon's last report
	// of card.go, whose related text the new overlay still has, through
	// the new source map. Once it switches to a private gopls it drops the
	// daemon's reports, which publishes no diagnostics; what follows is
	// the private gopls's.
	dropped := false
	client.awaitDiagnostics(document, func(diagnostics []any) bool {
		if len(diagnostics) == 0 {
			dropped = true
			return false
		}
		diagnostic, ok := diagnosticAt(diagnostics, "Card redeclared")
		if !dropped || !ok {
			return false
		}
		line, _ := rangeStart(diagnostic)
		return line == 4
	})
	if got := adapter.serving(); got != "" {
		t.Errorf("adapter serves through %q; want a private gopls", got)
	}
}

func TestGoplsCommand_GIVENARemote_WHENBuilt_THENTheFlagPrecedesTheSubcommand(t *testing.T) {
	tests := []struct {
		command []string
		remote  string
		want    string
	}{
		{[]string{"gopls"}, "auto", "gopls -remote=auto"},
		{[]string{"/bin/gopls", "serve"}, "unix;/tmp/g.sock", "/bin/gopls -remote=unix;/tmp/g.sock serve"},
		{[]string{"gopls", "-remote=localhost:4389"}, "auto", "gopls -remote=localhost:4389"},
		{[]string{"gopls"}, "", "gopls"},
	}
	for _, tt := range tests {
		if got := strings.Join(goplsCommand(tt.command, tt.remote), " "); got != tt.want {
			t.Errorf("goplsCommand(%v, %q) = %q; want %q", tt.command, tt.remote, got, tt.want)
		}
	}
}

// TestGoplsDaemon_GIVENADaemonThatStops_WHENAnEditArrivesDuringTheFallback_THENTheDiagnosticsFollowTheEdit
// edits the document while the adapter switches to a private gopls. The
// switch regenerates every overlay from the buffers as they were when it
// began: here a long document, slow to generate, which the edit replaces
// with a short one. The edit's diagnostics must win.
func TestGoplsDaemon_GIVENADaemonThatStops_WHENAnEditArrivesDuringTheFallback_THENTheDiagnosticsFollowTheEdit(t *testing.T) {
	requireGopls(t)
	remote, stop := startGoplsDaemon(t)
	var long strings.Builder
	long.WriteString(goplsComponents)
	for index := range 1500 {
		fmt.Fprintf(&long, "\nFiller%d [label: string] {\n\tp \"{label}\"\n}\n", index)
	}
	root := viewsModule(t, map[string]string{
		"views/card.go":   "package views\n\ntype Card struct{}\n",
		"views/view.ktml": long.String(),
	})
	client, adapter := goplsHostWith(t, root, &config.Gopls{Remote: remote})
	document := filepath.Join(root, "views", "view.ktml")
	client.open(document)
	client.awaitDiagnostics(document, hasDiagnostic("Card redeclared in this block"))

	stop()
	deadline := time.Now().Add(time.Minute)
	for adapter.serving() != "" {
		if time.Now().After(deadline) {
			t.Fatalf("adapter serves through %q; want a private gopls", adapter.serving())
		}
		time.Sleep(time.Millisecond)
	}
	client.change(document, 2, "// A comment moves every declaration down two lines.\n\n"+goplsComponents)
	redeclaredOn := func(diagnostics []any) int {
		diagnostic, ok := diagnosticAt(diagnostics, "Card redeclared")
		if !ok {
			return -1
		}
		line, _ := rangeStart(diagnostic)
		return line
	}
	client.awaitDiagnostics(document, func(diagnostics []any) bool { return redeclaredOn(diagnostics) == 4 })
	// The switch may still publish what it generated from the long
	// document; the diagnostics must stay on the edit's line once the
	// adapter is quiet.
	for quiet := time.After(5 * time.Second); ; {
		select {
		case message := <-client.messages:
			params, _ := message["params"].(map[string]any)
			if message["method"] == publishDiagnosticsMethod && params["uri"] == fileURI(document) {
				diagnostics, _ := params["diagnostics"].([]any)
				client.latest[fileURI(document)] = diagnostics
				quiet = time.After(5 * time.Second)
			}
			continue
		case <-quiet:
		}
		break
	}
	if line := redeclaredOn(client.latest[fileURI(document)]); line != 4 {
		t.Errorf("Card redeclared is reported on line %d after the edit; want 4: the switch to a private gopls regenerated the document from its text before the edit", line)
	}
}
