package e2e_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/karl-tye/ktml/e2e-test/internal/app"
	"github.com/karl-tye/ktml/internal/plugin/gohtml"
	"github.com/karl-tye/ktml/internal/plugin/htmx"
)

var buildOnce sync.Once
var buildErr error

func TestMain(m *testing.M) {
	// gopls -remote=auto, the gopls adapter's default, puts its daemon's
	// socket in XDG_RUNTIME_DIR: a directory of the run's own keeps every
	// ktml lsp out of the user's daemon, and its daemons are stopped below.
	runtimeDir, err := os.MkdirTemp("", "ktmle")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	goplsRuntimeDir = runtimeDir
	_ = os.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	code := m.Run()
	stopGoplsDaemons(runtimeDir)
	_ = os.RemoveAll(runtimeDir)
	for _, path := range []string{"components.html", "components.ktml.go", "index.html", "index.ktml.go", "ktml"} {
		_ = os.Remove(path)
	}
	_ = os.RemoveAll(".ktml")
	os.Exit(code)
}

func TestApplication_GIVENFullPageRequest_WHENRendered_THENReturnsDashboardShell(t *testing.T) {
	prepareGeneratedFixture(t)
	handler := app.NewEnquiryHandler()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()

	app.Routes(handler).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	assertContains(t, response.Body.String(), "Operations Dashboard")
	assertContains(t, response.Body.String(), `hx-get="/enquiries"`)
	assertContains(t, response.Body.String(), `id="enquiry-count"`)
}

func TestApplication_GIVENHTMXListRequest_WHENRendered_THENReturnsRowsAndOOBCount(t *testing.T) {
	prepareGeneratedFixture(t)
	handler := app.NewEnquiryHandler()
	request := httptest.NewRequest(http.MethodGet, "/enquiries", nil)
	request.Header.Set("HX-Request", "true")
	response := httptest.NewRecorder()

	app.Routes(handler).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	assertContains(t, response.Body.String(), `id="enquiry-1"`)
	assertContains(t, response.Body.String(), "Boiler pressure is dropping")
	assertContains(t, response.Body.String(), `hx-swap-oob`)
	assertContains(t, response.Body.String(), "3 active enquiries")
}

func TestApplication_GIVENCreateRequest_WHENHandled_THENReturnsSwapAndOOBFragments(t *testing.T) {
	prepareGeneratedFixture(t)
	handler := app.NewEnquiryHandler()
	form := url.Values{"customer": {"Katherine Johnson"}, "summary": {"Heating system inspection"}}
	request := httptest.NewRequest(http.MethodPost, "/enquiries", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("HX-Request", "true")
	response := httptest.NewRecorder()

	app.Routes(handler).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	assertContains(t, response.Body.String(), "Katherine Johnson")
	assertContains(t, response.Body.String(), "4 active enquiries")
	assertContains(t, response.Body.String(), `id="enquiry-inputs"`)
	if got := len(handler.Store.List()); got != 4 {
		t.Errorf("store count = %d, want 4", got)
	}
}

func TestHTMXPlugin_GIVENFixtureRouter_WHENDiscovered_THENIndexesNestedChiRoutes(t *testing.T) {
	index, err := htmx.DiscoverRoutes(".")
	if err != nil {
		t.Fatalf("DiscoverRoutes() error = %v", err)
	}
	want := map[string]bool{
		"GET /":                      false,
		"GET /enquiries":             false,
		"POST /enquiries":            false,
		"PUT /enquiries/{id}/status": false,
		"GET /static/htmx.min.js":    false,
	}
	for _, route := range index.Routes {
		key := route.Method + " " + route.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for route, found := range want {
		if !found {
			t.Errorf("route %s was not discovered; index = %#v", route, index.Routes)
		}
	}
}

func TestLSP_GIVENHTMXAttribute_WHENCompleting_THENReturnsChiRoute(t *testing.T) {
	prepareBinaries(t)
	command := exec.Command("./ktml", "lsp")
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe() error = %v", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe() error = %v", err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatalf("start LSP: %v", err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	})
	reader := bufio.NewReader(stdout)

	writeLSP(t, stdin, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]any{"rootUri": "file://" + fixtureDirectory(t)},
	})
	initialize := readLSP(t, reader)
	if initialize["id"] != float64(1) {
		t.Fatalf("initialize response = %#v", initialize)
	}

	writeLSP(t, stdin, map[string]any{
		"jsonrpc": "2.0",
		"method":  "textDocument/didOpen",
		"params": map[string]any{
			"textDocument": map[string]any{
				"uri":        "file:///tmp/editor.ktml",
				"languageId": "ktml",
				"version":    1,
				"text":       `form hx-post="/enq`,
			},
		},
	})
	// gohtml and htmx both publish for the document; the host merges them into
	// one notification so neither erases the other's diagnostics.
	opened := readLSPNotification(t, reader, "file:///tmp/editor.ktml")
	if encoded, _ := json.Marshal(opened); !bytes.Contains(encoded, []byte(`"source":"ktml"`)) {
		t.Fatalf("didOpen diagnostics = %s, want the merged notification with gohtml's syntax diagnostics", encoded)
	}
	appPath := fixtureDirectory(t) + "/internal/app/app.go"
	appURI := "file://" + appPath
	chiRouteLine, chiRouteCharacter := sourcePosition(t, appPath, `r.Get("/enquiries"`)
	chiRouteCharacter += len(`r.Get("`)

	// The route index builds in the background; until it is ready completion
	// answers isIncomplete, and the client asks again.
	var encoded []byte
	for attempt := 0; ; attempt++ {
		writeLSP(t, stdin, map[string]any{
			"jsonrpc": "2.0",
			"id":      2,
			"method":  "textDocument/completion",
			"params": map[string]any{
				"textDocument": map[string]any{"uri": "file:///tmp/editor.ktml"},
				"position":     map[string]any{"line": 0, "character": 19},
			},
		})
		completion := readLSP(t, reader)
		encoded, _ = json.Marshal(completion["result"])
		if bytes.Contains(encoded, []byte(`/enquiries`)) || attempt == 100 {
			break
		}
		if !bytes.Contains(encoded, []byte(`"isIncomplete":true`)) {
			t.Fatalf("completion before the index was ready is not marked incomplete: %s", encoded)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !bytes.Contains(encoded, []byte(`/enquiries`)) {
		t.Fatalf("completion result does not contain /enquiries: %s", encoded)
	}

	indexPath := fixtureDirectory(t) + "/index.ktml"
	line, character := sourcePosition(t, indexPath, "EnquiryCount count")
	definitionParams := map[string]any{
		"id":           3,
		"textDocument": map[string]any{"uri": "file://" + indexPath},
		"position":     map[string]any{"line": line, "character": character},
	}
	directPayload, err := json.Marshal(definitionParams)
	if err != nil {
		t.Fatalf("marshal definition request: %v", err)
	}
	directDefinition, err := gohtml.HandleLSP("textDocument/definition", directPayload)
	if err != nil {
		t.Fatalf("direct definition error: %v", err)
	}
	if !bytes.Contains(directDefinition, []byte(`/components.ktml`)) {
		t.Fatalf("direct EnquiryCount definition = %s", directDefinition)
	}
	writeLSP(t, stdin, map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "textDocument/definition",
		"params":  definitionParams,
	})
	definition := readLSP(t, reader)
	result, ok := definition["result"].(map[string]any)
	if !ok || !strings.HasSuffix(fmt.Sprint(result["uri"]), "/components.ktml") {
		t.Fatalf("EnquiryCount definition = %#v", definition)
	}

	routeLine, routeCharacter := sourcePosition(t, indexPath, `hx-get="/enquiries"`)
	routeCharacter += len(`hx-get="`)
	writeLSP(t, stdin, map[string]any{
		"jsonrpc": "2.0",
		"id":      4,
		"method":  "textDocument/definition",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": "file://" + indexPath},
			"position":     map[string]any{"line": routeLine, "character": routeCharacter},
		},
	})
	routeDefinition := readLSP(t, reader)
	routeResult, ok := routeDefinition["result"].(map[string]any)
	if !ok || !strings.HasSuffix(fmt.Sprint(routeResult["uri"]), "/internal/app/app.go") {
		t.Fatalf("hx-get definition = %#v", routeDefinition)
	}
	routeRange, ok := routeResult["range"].(map[string]any)
	if !ok {
		t.Fatalf("hx-get definition has no range: %#v", routeDefinition)
	}
	routeStart, ok := routeRange["start"].(map[string]any)
	if !ok {
		t.Fatalf("hx-get definition has no start position: %#v", routeDefinition)
	}
	appLines := strings.Count(string(readFile(t, fixtureDirectory(t)+"/internal/app/app.go")), "\n") + 1
	if line, ok := routeStart["line"].(float64); !ok || int(line) >= appLines {
		t.Fatalf("hx-get definition line is outside app.go: %#v", routeDefinition)
	}

	writeLSP(t, stdin, map[string]any{
		"jsonrpc": "2.0",
		"method":  "textDocument/didOpen",
		"params": map[string]any{
			"textDocument": map[string]any{
				"uri":        "file://" + indexPath,
				"languageId": "ktml",
				"version":    1,
				"text":       string(readFile(t, indexPath)),
			},
		},
	})

	writeLSP(t, stdin, map[string]any{
		"jsonrpc": "2.0",
		"id":      5,
		"method":  "textDocument/prepareRename",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": appURI},
			"position":     map[string]any{"line": chiRouteLine, "character": chiRouteCharacter},
		},
	})
	preparedRename := readLSP(t, reader)
	preparedResult, ok := preparedRename["result"].(map[string]any)
	if !ok || preparedResult["placeholder"] != "/enquiries" {
		t.Fatalf("Chi route prepareRename = %#v", preparedRename)
	}

	writeLSP(t, stdin, map[string]any{
		"jsonrpc": "2.0",
		"id":      6,
		"method":  "textDocument/rename",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": appURI},
			"position":     map[string]any{"line": chiRouteLine, "character": chiRouteCharacter},
			"newName":      "/requests",
		},
	})
	renameResponse := readLSP(t, reader)
	renameResult, ok := renameResponse["result"].(map[string]any)
	if !ok {
		t.Fatalf("Chi route rename has no workspace edit: %#v", renameResponse)
	}
	changes, ok := renameResult["changes"].(map[string]any)
	if !ok {
		t.Fatalf("hx-get rename has invalid changes: %#v", renameResponse)
	}
	ktmlEdits, ok := changes["file://"+indexPath].([]any)
	if !ok || len(ktmlEdits) != 2 {
		t.Fatalf("hx-get rename does not edit KTML consumer: %#v", renameResponse)
	}
	for _, rawEdit := range ktmlEdits {
		edit, ok := rawEdit.(map[string]any)
		if !ok || edit["newText"] != "/requests" {
			t.Fatalf("hx-get rename includes conflicting KTML edit: %#v", renameResponse)
		}
	}
	chiEdits, ok := changes[appURI].([]any)
	if !ok || len(chiEdits) != 1 {
		t.Fatalf("hx-get rename does not edit Chi registration: %#v", renameResponse)
	}
	chiEdit, ok := chiEdits[0].(map[string]any)
	if !ok || chiEdit["newText"] != `"/requests"` {
		t.Fatalf("hx-get rename includes invalid Chi edit: %#v", renameResponse)
	}

	renamedSource := strings.ReplaceAll(string(readFile(t, indexPath)), `hx-get="/enquiries"`, `hx-get="/requests"`)
	writeLSP(t, stdin, map[string]any{
		"jsonrpc": "2.0",
		"method":  "textDocument/didChange",
		"params": map[string]any{
			"textDocument":   map[string]any{"uri": "file://" + indexPath, "version": 2},
			"contentChanges": []map[string]any{{"text": renamedSource}},
		},
	})
	writeLSP(t, stdin, map[string]any{
		"jsonrpc": "2.0",
		"id":      7,
		"method":  "textDocument/definition",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": "file://" + indexPath},
			"position":     map[string]any{"line": routeLine, "character": routeCharacter},
		},
	})
	renamedDefinition := readLSP(t, reader)
	renamedDefinitionResult, ok := renamedDefinition["result"].(map[string]any)
	if !ok || !strings.HasSuffix(fmt.Sprint(renamedDefinitionResult["uri"]), "/internal/app/app.go") {
		t.Fatalf("renamed hx-get definition = %#v", renamedDefinition)
	}
}

func TestDevWorkflow_GIVENADashboardPage_WHENUsed_THENSupportsHTMXAndReload(t *testing.T) {
	prepareBinaries(t)
	devURL := startServer(t)

	browser := rod.New().MustConnect()
	t.Cleanup(browser.MustClose)
	page := browser.MustPage(devURL)
	page.MustWaitLoad()

	t.Run("loads HTMX fragments", func(t *testing.T) {
		assertElementText(t, page, "#page-title", "Operations Dashboard")
		assertElementText(t, page, "#enquiry-1", "Boiler pressure is dropping")
		assertElementText(t, page, "#enquiry-count", "3 active enquiries")
	})

	t.Run("creates and converts an enquiry through HTMX", func(t *testing.T) {
		page.MustElement("#enquiry-customer").MustInput("Katherine Johnson")
		page.MustElement("#enquiry-summary").MustInput("Heating system inspection")
		page.MustElement("#create-enquiry").MustClick()
		assertElementText(t, page, "#enquiry-4", "Katherine Johnson")
		assertElementText(t, page, "#enquiry-count", "4 active enquiries")
		page.MustElement("#convert-4").MustClick()
		assertElementText(t, page, "#enquiry-4", "Converted")
	})

	t.Run("reloads the page after a layout change", func(t *testing.T) {
		assertHotReload := injectHotReloadFlag(page)
		reset := replaceInFile(t, "index.ktml", "Operations Dashboard", "Dispatch Dashboard")
		t.Cleanup(reset)
		assertElementText(t, page, "#page-title", "Dispatch Dashboard")
		assertHotReload(t, page, false)
		reset()
		assertElementText(t, page, "#page-title", "Operations Dashboard")
	})

	// The gohtml app has no development registry (no ktmldev build, no
	// hmr.Install), so a compatible change falls back to rebuild and reload.
	// Live patching is covered by TestHMR against the hmrapp fixture. The
	// restart also resets the in-memory store to its three enquiries.
	t.Run("rebuilds and reloads a compatible component change without a control channel", func(t *testing.T) {
		assertHotReload := injectHotReloadFlag(page)
		assertElementText(t, page, "#enquiry-count", "3 active enquiries")
		reset := replaceInFile(t, "components.ktml", "active enquiries", "live enquiries")
		t.Cleanup(reset)
		assertElementText(t, page, "#enquiry-count", "3 live enquiries")
		assertHotReload(t, page, false)
		reset()
		assertElementText(t, page, "#enquiry-count", "3 active enquiries")
	})
}

func prepareGeneratedFixture(t *testing.T) {
	t.Helper()
	prepareBinaries(t)
	for _, file := range []string{"components.ktml", "index.ktml"} {
		command := exec.Command("./ktml", "generate", file)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("generate %s: %v\n%s", file, err, output)
		}
	}
}

func prepareBinaries(t *testing.T) {
	t.Helper()
	buildOnce.Do(func() {
		commands := []*exec.Cmd{
			exec.Command("go", "build", "-o", "ktml", "../cmd/ktml"),
			exec.Command("go", "build", "-buildmode=c-shared", "-o", "../plugins/gohtml.wasm", "../cmd/plugins/gohtml"),
			exec.Command("go", "build", "-buildmode=c-shared", "-o", "../plugins/htmx.wasm", "../cmd/plugins/htmx"),
			exec.Command("go", "build", "-buildmode=c-shared", "-o", "../plugins/alpine.wasm", "../cmd/plugins/alpine"),
		}
		for _, command := range commands[1:] {
			command.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
		}
		for _, command := range commands {
			if output, err := command.CombinedOutput(); err != nil {
				buildErr = fmt.Errorf("%s: %w\n%s", command.String(), err, output)
				return
			}
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
}

// startServer runs `ktml dev` on ephemeral ports and returns its URL. Ports
// are never fixed: other local servers (for example on 8080) must be neither
// reused nor signalled. Cleanup only terminates processes this test started.
func startServer(t *testing.T) string {
	t.Helper()
	devPort, backendPort := freePort(t), freePort(t)
	useDevConfig(t, devPort, backendPort)

	command := exec.Command("./ktml", "dev")
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatalf("start dev server: %v", err)
	}
	exited := make(chan struct{})
	go func() {
		_ = command.Wait()
		close(exited)
	}()
	t.Cleanup(func() { stopDevServer(t, command, exited) })

	devURL := fmt.Sprintf("http://127.0.0.1:%d", devPort)
	for range 100 {
		response, err := http.Get(devURL + "/health-check")
		if err == nil {
			response.Body.Close()
			if response.StatusCode != http.StatusBadGateway {
				return devURL
			}
		}
		select {
		case <-exited:
			t.Fatal("dev server exited before it was ready")
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatal("dev server never started")
	return ""
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve ephemeral port: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

// useDevConfig points the fixture's dev config at the chosen ports for the
// duration of the test, restoring the committed config afterwards.
func useDevConfig(t *testing.T, devPort, backendPort int) {
	t.Helper()
	original := readFile(t, "ktml.config.json")
	var cfg map[string]any
	if err := json.Unmarshal(original, &cfg); err != nil {
		t.Fatalf("parse ktml.config.json: %v", err)
	}
	devCfg, ok := cfg["dev"].(map[string]any)
	if !ok {
		t.Fatal("ktml.config.json has no dev section")
	}
	devCfg["port"] = devPort
	devCfg["proxy_target"] = fmt.Sprintf("http://127.0.0.1:%d", backendPort)
	devCfg["command"] = []string{"{binary}", "-addr", fmt.Sprintf("127.0.0.1:%d", backendPort)}
	updated, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("encode dev config: %v", err)
	}
	writeFile(t, "ktml.config.json", string(updated))
	t.Cleanup(func() { writeFile(t, "ktml.config.json", string(original)) })
}

type startedProcess struct {
	pid       int
	startTime string
}

// stopDevServer interrupts `ktml dev` so it stops its own backend, then
// terminates any backend it left behind. Only children of the dev server,
// recorded before it exits and re-verified by PID and start time, are signalled.
func stopDevServer(t *testing.T, command *exec.Cmd, exited <-chan struct{}) {
	t.Helper()
	children := childProcesses(command.Process.Pid)
	_ = command.Process.Signal(os.Interrupt)
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Log("dev server ignored interrupt; killing it")
		_ = command.Process.Kill()
		<-exited
	}
	for _, child := range children {
		if processStartTime(child.pid) != child.startTime {
			continue
		}
		t.Logf("terminating backend %d left by the dev server", child.pid)
		// The runner starts the backend as its own process-group leader.
		if pgid, err := syscall.Getpgid(child.pid); err == nil && pgid == child.pid {
			_ = syscall.Kill(-child.pid, syscall.SIGKILL)
		} else {
			_ = syscall.Kill(child.pid, syscall.SIGKILL)
		}
	}
}

func childProcesses(parent int) []startedProcess {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var children []startedProcess
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		fields := procStatFields(pid)
		if len(fields) > 19 && fields[1] == strconv.Itoa(parent) {
			children = append(children, startedProcess{pid: pid, startTime: fields[19]})
		}
	}
	return children
}

func processStartTime(pid int) string {
	if fields := procStatFields(pid); len(fields) > 19 {
		return fields[19]
	}
	return ""
}

// procStatFields returns /proc/<pid>/stat fields after the command name, so
// index 0 is the state, 1 the parent PID, and 19 the start time.
func procStatFields(pid int) []string {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return nil
	}
	end := bytes.LastIndexByte(stat, ')')
	if end < 0 {
		return nil
	}
	return strings.Fields(string(stat[end+1:]))
}

func writeLSP(t *testing.T, writer io.Writer, message map[string]any) {
	t.Helper()
	payload, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("marshal LSP message: %v", err)
	}
	if _, err := fmt.Fprintf(writer, "Content-Length: %d\r\n\r\n%s", len(payload), payload); err != nil {
		t.Fatalf("write LSP message: %v", err)
	}
}

// readLSP returns the next response, skipping notifications and requests
// from the server, which arrive whenever background work completes.
func readLSP(t *testing.T, reader *bufio.Reader) map[string]any {
	t.Helper()
	for {
		message := readLSPMessage(t, reader)
		if _, isResponse := message["id"]; isResponse && message["method"] == nil {
			return message
		}
	}
}

// readLSPNotification returns the next diagnostics published for uri.
func readLSPNotification(t *testing.T, reader *bufio.Reader, uri string) map[string]any {
	t.Helper()
	for {
		message := readLSPMessage(t, reader)
		params, _ := message["params"].(map[string]any)
		if message["method"] == "textDocument/publishDiagnostics" && params["uri"] == uri {
			return message
		}
	}
}

func readLSPMessage(t *testing.T, reader *bufio.Reader) map[string]any {
	t.Helper()
	var contentLength int
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read LSP header: %v", err)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if value, ok := strings.CutPrefix(line, "Content-Length:"); ok {
			contentLength, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				t.Fatalf("parse Content-Length: %v", err)
			}
		}
	}
	payload := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, payload); err != nil {
		t.Fatalf("read LSP payload: %v", err)
	}
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode LSP payload: %v", err)
	}
	return message
}

func injectHotReloadFlag(page *rod.Page) func(*testing.T, *rod.Page, bool) {
	page.MustEval(`() => window.HOT_RELOAD_FLAG = true`)
	return func(t *testing.T, page *rod.Page, exists bool) {
		t.Helper()
		isHot := page.MustEval(`() => !!window.HOT_RELOAD_FLAG`).Bool()
		if isHot != exists {
			t.Fatalf("hot reload flag = %t, want %t", isHot, exists)
		}
	}
}

func replaceInFile(t *testing.T, path, old, replacement string) func() {
	t.Helper()
	original := string(readFile(t, path))
	if !strings.Contains(original, old) {
		t.Fatalf("%s does not contain %q", path, old)
	}
	writeFile(t, path, strings.Replace(original, old, replacement, 1))
	var once sync.Once
	return func() {
		once.Do(func() { writeFile(t, path, original) })
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return content
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func assertElementText(t *testing.T, page *rod.Page, selector, text string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if _, err := page.Context(ctx).ElementR(selector, text); err != nil {
		t.Fatalf("find %s containing %q: %v", selector, text, err)
	}
}

func assertContains(t *testing.T, content, expected string) {
	t.Helper()
	if !strings.Contains(content, expected) {
		t.Errorf("response does not contain %q:\n%s", expected, content)
	}
}

func fixtureDirectory(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	return dir
}

func sourcePosition(t *testing.T, path, target string) (line, character int) {
	t.Helper()
	content := string(readFile(t, path))
	offset := strings.Index(content, target)
	if offset < 0 {
		t.Fatalf("%s does not contain %q", path, target)
	}
	before := content[:offset]
	line = strings.Count(before, "\n")
	if lastNewline := strings.LastIndex(before, "\n"); lastNewline >= 0 {
		character = len(before) - lastNewline - 1
	} else {
		character = len(before)
	}
	return line, character
}
