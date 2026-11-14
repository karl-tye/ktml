package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/config"
	"github.com/karl-tye/ktml/internal/generator/gogen"
	"github.com/karl-tye/ktml/internal/gopkg"
	"github.com/karl-tye/ktml/internal/textposition"
)

// GoplsPinnedVersion is the gopls release KTML is tested with.
const GoplsPinnedVersion = "v0.23.0"

// DefaultGoplsCommand starts gopls from PATH.
var DefaultGoplsCommand = []string{"gopls"}

const goplsSource = "gopls"

// GoplsAdapter runs gopls as a child server over the Go that KTML generates
// for the open documents' packages, so Go type errors in generated code show
// on the .ktml and .ktmlschema ranges they were generated from, and Host
// Schema symbols navigate and rename into handwritten Go.
//
// The adapter generates the production variant of every KTML document in the
// package of each open document from the editor's buffers, and opens it in
// gopls as an overlay of the generated file's own path. gopls therefore type
// checks the code the current buffers would generate, never a stale file on
// disk. Each overlay keeps the source map it was generated with (gogen's line
// directives, which gopls does not apply), and gopls diagnostics are mapped
// only when they carry the overlay's current version. When a document stops
// compiling, its overlay is stale: its diagnostics are withdrawn and none are
// mapped until it compiles again.
//
// gopls runs as a forwarder to a shared gopls daemon (gopls -remote), so
// several ktml lsp sessions, and an editor's gopls started with
// -remote=auto, share one gopls's cache and memory. The daemon keeps each
// client's overlays in its own session: the editor never sees ours. When
// the daemon is unreachable or fails, the adapter runs a private gopls.
type GoplsAdapter struct {
	root    string
	command []string
	// remote is the configured daemon address, "" for a private gopls.
	remote string

	// startMu serializes starting gopls.
	startMu sync.Mutex
	// refreshMu serializes refreshes. Each generates from the buffers as
	// they are when it begins; one that ran beside a later one, such as
	// the regeneration after a failed daemon beside an edit, could
	// otherwise store its older overlays last.
	refreshMu sync.Mutex

	mu sync.Mutex
	// documents holds the editor's KTML buffers by path.
	documents map[string]goplsDocument
	// generated holds the overlays by Go file URI.
	generated map[string]*generatedGo
	// snapshots holds the snapshot each KTML document was last generated
	// from, by path.
	snapshots map[string]*compiler.Snapshot
	// reports holds gopls's last diagnostics by Go URI; the KTML
	// diagnostics are derived from them and the current overlays by remap.
	reports map[string]goplsReport
	// mapped holds the KTML diagnostics last published, encoded, by KTML
	// URI.
	mapped map[string]string

	server *childServer
	// servingAddress is the daemon address server forwards to, "" for a
	// private gopls.
	servingAddress string
	// private is set once the daemon failed: gopls runs privately.
	private  bool
	startErr error
	warned   bool
	// outbox keeps notifications and requests to gopls in order.
	outbox chan func()

	published chan []byte
	queue     []([]byte)
	queued    chan struct{}
	stop      chan struct{}
	closeOnce sync.Once
}

type goplsDocument struct {
	version int
	source  []byte
}

// generatedGo is one overlay: the Go generated for a KTML document.
type generatedGo struct {
	uri    string
	source string
	// sourceText is the KTML text it was generated from.
	sourceText []byte
	text       []byte
	mappings   []gogen.SourceMapping
	version    int
	// openedIn is the gopls the overlay is open in; a gopls that replaced
	// it gets the overlay again.
	openedIn *childServer
	stale    bool
}

// NewGoplsAdapter returns the adapter for the workspace root. gopls starts
// with the first KTML document of a Go module.
func NewGoplsAdapter(root string, settings *config.Gopls) *GoplsAdapter {
	command := DefaultGoplsCommand
	if settings != nil && len(settings.Command) > 0 {
		command = settings.Command
	}
	adapter := &GoplsAdapter{
		root:      root,
		command:   command,
		remote:    settings.GoplsRemote(),
		documents: make(map[string]goplsDocument),
		generated: make(map[string]*generatedGo),
		snapshots: make(map[string]*compiler.Snapshot),
		reports:   make(map[string]goplsReport),
		mapped:    make(map[string]string),
		outbox:    make(chan func(), 256),
		published: make(chan []byte, 16),
		queued:    make(chan struct{}, 1),
		stop:      make(chan struct{}),
	}
	go adapter.deliver()
	go adapter.drain()
	return adapter
}

// goplsCommand returns command as a forwarder to the daemon at remote,
// with -remote before any subcommand. A command that names its own remote
// is kept.
func goplsCommand(command []string, remote string) []string {
	if remote == "" || len(command) == 0 {
		return command
	}
	for _, arg := range command[1:] {
		if arg == "-remote" || strings.HasPrefix(arg, "-remote=") || arg == "--remote" || strings.HasPrefix(arg, "--remote=") {
			return command
		}
	}
	return append([]string{command[0], "-remote=" + remote}, command[1:]...)
}

// dialDaemon checks that the daemon at an explicit address listens. gopls
// starts an auto daemon itself.
func dialDaemon(remote string) error {
	if remote == "auto" || strings.HasPrefix(remote, "auto;") {
		return nil
	}
	network, address := "tcp", remote
	if path, ok := strings.CutPrefix(remote, "unix;"); ok {
		network, address = "unix", path
	}
	connection, err := net.DialTimeout(network, address, time.Second)
	if err != nil {
		return err
	}
	return connection.Close()
}

// serving returns the daemon address gopls forwards to, or "" for a
// private gopls.
func (a *GoplsAdapter) serving() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.servingAddress
}

// Published delivers the adapter's diagnostics and messages.
func (a *GoplsAdapter) Published() <-chan []byte { return a.published }

// Close stops gopls.
func (a *GoplsAdapter) Close() error {
	var err error
	a.closeOnce.Do(func() {
		close(a.stop)
		// A gopls being started is stopped too.
		a.startMu.Lock()
		defer a.startMu.Unlock()
		a.mu.Lock()
		server := a.server
		a.mu.Unlock()
		if server != nil {
			err = server.Close()
		}
	})
	return err
}

// publish queues a notification for the host in order, without blocking:
// the host may be waiting for this adapter's answer to a message.
func (a *GoplsAdapter) publish(notification []byte) {
	a.mu.Lock()
	a.queue = append(a.queue, notification)
	a.mu.Unlock()
	select {
	case a.queued <- struct{}{}:
	default:
	}
}

func (a *GoplsAdapter) deliver() {
	for {
		select {
		case <-a.queued:
		case <-a.stop:
			return
		}
		for {
			a.mu.Lock()
			if len(a.queue) == 0 {
				a.mu.Unlock()
				break
			}
			next := a.queue[0]
			a.queue = a.queue[1:]
			a.mu.Unlock()
			select {
			case a.published <- next:
			case <-a.stop:
				return
			}
		}
	}
}

// ExecuteLSP answers one client message.
func (a *GoplsAdapter) ExecuteLSP(ctx context.Context, method string, payload []byte) ([]byte, error) {
	var params struct {
		ID           json.RawMessage `json:"id"`
		TextDocument struct {
			URI     string `json:"uri"`
			Text    string `json:"text"`
			Version int    `json:"version"`
		} `json:"textDocument"`
		ContentChanges []struct {
			Text string `json:"text"`
		} `json:"contentChanges"`
		Position lspPosition `json:"position"`
		Context  struct {
			IncludeDeclaration bool `json:"includeDeclaration"`
		} `json:"context"`
		NewName string `json:"newName"`
		Changes []struct {
			URI string `json:"uri"`
		} `json:"changes"`
	}
	if len(payload) > 0 && json.Unmarshal(payload, &params) != nil {
		return nil, nil
	}
	uri := params.TextDocument.URI
	path, isKTML := ktmlPath(uri)
	switch method {
	case "textDocument/didOpen":
		if isKTML {
			a.update(path, params.TextDocument.Version, []byte(params.TextDocument.Text))
		}
	case "textDocument/didChange":
		if isKTML && len(params.ContentChanges) > 0 {
			a.update(path, params.TextDocument.Version, []byte(params.ContentChanges[len(params.ContentChanges)-1].Text))
		}
	case "textDocument/didClose":
		if isKTML {
			a.mu.Lock()
			delete(a.documents, path)
			a.mu.Unlock()
			a.refresh(filepath.Dir(path))
		}
	case "workspace/didChangeWatchedFiles":
		a.watched(payload, params.Changes)
	case "textDocument/definition":
		if isKTML {
			return rpcResult(params.ID, a.goUsages(ctx, path, params.Position, false)), nil
		}
	case "textDocument/references":
		if isKTML {
			return rpcResult(params.ID, a.goUsages(ctx, path, params.Position, true)), nil
		}
	case "textDocument/rename":
		if isKTML {
			return rpcResult(params.ID, a.rename(ctx, path, params.Position, params.NewName)), nil
		}
	case "textDocument/hover":
		if isKTML {
			if hover := a.hover(ctx, path, params.Position); hover != nil {
				return rpcResult(params.ID, hover), nil
			}
		}
	}
	return nil, nil
}

// ktmlPath returns the path of a KTML or Host Schema document URI. Stories
// generate no Go.
func ktmlPath(uri string) (string, bool) {
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != "file" {
		return "", false
	}
	path := filepath.FromSlash(parsed.Path)
	if (!strings.HasSuffix(path, ".ktml") && !compiler.IsSchemaPath(path)) || compiler.IsStoryPath(path) {
		return "", false
	}
	return path, true
}

func fileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

func (a *GoplsAdapter) update(path string, version int, source []byte) {
	a.mu.Lock()
	a.documents[path] = goplsDocument{version: version, source: source}
	a.mu.Unlock()
	a.refresh(filepath.Dir(path))
}

// watched forwards file changes to gopls, which reads handwritten Go from
// disk, and regenerates the packages whose KTML sources changed on disk.
func (a *GoplsAdapter) watched(payload []byte, changes []struct {
	URI string `json:"uri"`
}) {
	dirs := make(map[string]bool)
	for _, change := range changes {
		if path, ok := ktmlPath(change.URI); ok {
			dirs[filepath.Dir(path)] = true
		}
	}
	for _, dir := range sortedKeys(dirs) {
		a.refresh(dir)
	}
	if server := a.running(); server != nil {
		forwarded := withoutID(payload)
		a.send(func() { _ = server.Notify("workspace/didChangeWatchedFiles", forwarded) })
	}
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// refresh generates the Go of every KTML document in dir, and in the
// directories it imports from, from the current buffers, and opens or
// updates the changed overlays in gopls.
func (a *GoplsAdapter) refresh(dir string) {
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	visited := make(map[string]bool)
	a.refreshPackage(dir, visited)
	// Packages whose documents import from dir call its generated Go.
	a.mu.Lock()
	dependents := make(map[string]bool)
	for source, snapshot := range a.snapshots {
		for _, dependency := range snapshot.Dependencies(source) {
			if filepath.Dir(dependency) == dir && filepath.Dir(source) != dir {
				dependents[filepath.Dir(source)] = true
			}
		}
	}
	a.mu.Unlock()
	for _, dependent := range sortedKeys(dependents) {
		a.refreshPackage(dependent, visited)
	}
}

// generatedIn reports whether the documents of dir have overlays.
func (a *GoplsAdapter) generatedIn(dir string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, overlay := range a.generated {
		if filepath.Dir(overlay.source) == dir {
			return true
		}
	}
	return false
}

func (a *GoplsAdapter) refreshPackage(dir string, visited map[string]bool) {
	if visited[dir] {
		return
	}
	visited[dir] = true
	pkg, err := gopkg.Resolve(dir)
	if err != nil {
		// Not in a Go module: there is no Go to check.
		return
	}
	a.mu.Lock()
	documents := make(map[string]goplsDocument, len(a.documents))
	for path, document := range a.documents {
		documents[path] = document
	}
	a.mu.Unlock()
	sources := make(map[string]bool)
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if _, ok := ktmlPath(fileURI(path)); ok && !entry.IsDir() {
			sources[path] = true
		}
	}
	for path := range documents {
		if filepath.Dir(path) == dir {
			sources[path] = true
		}
	}
	// Overlays whose document is gone, deleted or closed unsaved, are
	// closed so gopls reads the disk again.
	a.mu.Lock()
	var gone []string
	closed := make(map[string]*childServer)
	for uri, overlay := range a.generated {
		if filepath.Dir(overlay.source) == dir && !sources[overlay.source] {
			gone = append(gone, uri)
			if overlay.openedIn != nil {
				closed[uri] = overlay.openedIn
			}
			delete(a.generated, uri)
			delete(a.snapshots, overlay.source)
		}
	}
	a.mu.Unlock()
	for _, uri := range sortedMapKeys(closed) {
		server := closed[uri]
		a.send(func() {
			_ = server.Notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": uri}})
		})
	}
	if len(gone) > 0 {
		a.remap()
	}
	if len(sources) == 0 {
		return
	}
	workspace := compiler.NewWorkspace(dir)
	for path, document := range documents {
		_ = workspace.Overlay(path, document.version, document.source)
	}
	options := gogen.Options{Package: pkg.Name, Packages: func(dir string) (gogen.GoPackage, error) {
		resolved, err := gopkg.Resolve(dir)
		return gogen.GoPackage{ImportPath: resolved.ImportPath, Name: resolved.Name}, err
	}}
	server := a.ensureServer()
	imported := make(map[string]bool)
	var notifications []func()
	for _, source := range sortedKeys(sources) {
		uri := fileURI(gogen.OutputFile(source, false))
		var text []byte
		var mappings []gogen.SourceMapping
		snapshot, err := workspace.Load(source)
		if err == nil && snapshot.Valid() {
			if compiler.IsSchemaPath(source) {
				text, mappings, err = gogen.GenerateSchema(snapshot, source, options)
			} else {
				text, mappings, err = gogen.GenerateFileMap(snapshot, source, options)
			}
		}
		a.mu.Lock()
		current := a.generated[uri]
		if current == nil {
			current = &generatedGo{uri: uri, source: source}
			a.generated[uri] = current
		}
		if snapshot == nil || !snapshot.Valid() || err != nil {
			// The document does not compile: its last overlay no longer
			// matches its text, so its diagnostics are withdrawn.
			current.stale = true
			delete(a.snapshots, source)
			a.mu.Unlock()
			continue
		}
		a.snapshots[source] = snapshot
		for _, dependency := range snapshot.Dependencies(source) {
			if filepath.Dir(dependency) != dir {
				imported[filepath.Dir(dependency)] = true
			}
		}
		changed := current.openedIn != server || current.stale || !bytes.Equal(current.text, text)
		current.stale = false
		current.sourceText = snapshot.Source(source)
		current.text, current.mappings = text, mappings
		if !changed {
			a.mu.Unlock()
			continue
		}
		// A new text is a new version even while no gopls runs, so a
		// report of the old text never maps through the new map.
		current.version++
		if server == nil {
			a.mu.Unlock()
			continue
		}
		version, opened := current.version, current.openedIn == server
		current.openedIn = server
		a.mu.Unlock()
		content := string(text)
		if opened {
			notifications = append(notifications, func() {
				_ = server.Notify("textDocument/didChange", map[string]any{"textDocument": map[string]any{"uri": uri, "version": version}, "contentChanges": []any{map[string]any{"text": content}}})
			})
		} else {
			notifications = append(notifications, func() {
				_ = server.Notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "languageId": "go", "version": version, "text": content}})
			})
		}
	}
	// Generated code calls the Go generated for the documents it imports,
	// which may not be on disk yet; gopls gets it first, so it never checks
	// this package without it. Their own changes refresh them.
	for _, dependency := range sortedKeys(imported) {
		if !a.generatedIn(dependency) {
			a.refreshPackage(dependency, visited)
		}
	}
	for _, notification := range notifications {
		a.send(notification)
	}
	a.remap()
}

// remap derives the KTML diagnostics from gopls's last reports and the
// current overlays, and publishes those of every KTML document that changed.
func (a *GoplsAdapter) remap() {
	a.mu.Lock()
	byKTML := make(map[string][]any)
	for _, goURI := range sortedMapKeys(a.reports) {
		for _, diagnostic := range a.mapReportLocked(goURI, a.reports[goURI]) {
			byKTML[diagnostic.uri] = append(byKTML[diagnostic.uri], diagnostic.value)
		}
	}
	var notifications [][]byte
	uris := make(map[string]bool)
	for uri := range byKTML {
		uris[uri] = true
	}
	for uri := range a.mapped {
		uris[uri] = true
	}
	for _, uri := range sortedKeys(uris) {
		diagnostics := byKTML[uri]
		if diagnostics == nil {
			diagnostics = []any{}
		}
		encoded, _ := json.Marshal(diagnostics)
		if previous, ok := a.mapped[uri]; ok && previous == string(encoded) || !ok && len(diagnostics) == 0 {
			continue
		}
		if len(diagnostics) == 0 {
			delete(a.mapped, uri)
		} else {
			a.mapped[uri] = string(encoded)
		}
		notification, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": publishDiagnosticsMethod, "params": map[string]any{"uri": uri, "diagnostics": diagnostics}})
		notifications = append(notifications, notification)
	}
	a.mu.Unlock()
	for _, notification := range notifications {
		a.publish(notification)
	}
}

func sortedMapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (a *GoplsAdapter) send(message func()) {
	select {
	case a.outbox <- message:
	case <-a.stop:
	}
}

func (a *GoplsAdapter) running() *childServer {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.server == nil || a.server.Err() != nil {
		return nil
	}
	return a.server
}

// request sends a request to gopls after every notification queued before
// it, so gopls answers over the overlays the request was made against.
func (a *GoplsAdapter) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	server := a.running()
	if server == nil {
		a.mu.Lock()
		err := a.startErr
		a.mu.Unlock()
		if err == nil {
			err = fmt.Errorf("gopls is not running")
		}
		return nil, err
	}
	type answer struct {
		result json.RawMessage
		err    error
	}
	answered := make(chan answer, 1)
	a.send(func() {
		result, err := server.Request(ctx, method, params, 30*time.Second)
		answered <- answer{result, err}
	})
	select {
	case result := <-answered:
		return result.result, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-a.stop:
		return nil, fmt.Errorf("closed")
	}
}

// ensureServer starts gopls once, as a forwarder to the configured daemon
// or, when the daemon is unreachable, privately. A gopls that cannot start
// is reported once; KTML's own features keep working.
func (a *GoplsAdapter) ensureServer() *childServer {
	a.startMu.Lock()
	defer a.startMu.Unlock()
	select {
	case <-a.stop:
		return nil
	default:
	}
	a.mu.Lock()
	if a.server != nil || a.startErr != nil {
		defer a.mu.Unlock()
		if a.server != nil && a.server.Err() != nil {
			// A failed daemon is replaced by a private gopls once its
			// failure is handled.
			if a.servingAddress == "" {
				a.warnLocked(a.server.Err())
			}
			return nil
		}
		return a.server
	}
	remote := a.remote
	if a.private {
		remote = ""
	}
	a.mu.Unlock()
	if remote != "" {
		if err := dialDaemon(remote); err != nil {
			a.fallBack(remote, err)
			remote = ""
		}
	}
	root := fileURI(a.root)
	initialize := map[string]any{
		"processId": os.Getpid(),
		"rootUri":   root,
		"workspaceFolders": []any{
			map[string]any{"uri": root, "name": filepath.Base(a.root)},
		},
		"capabilities": map[string]any{
			"workspace": map[string]any{
				"configuration":          true,
				"workspaceFolders":       true,
				"didChangeWatchedFiles":  map[string]any{"dynamicRegistration": true},
				"workspaceEdit":          map[string]any{"documentChanges": false},
				"didChangeConfiguration": map[string]any{"dynamicRegistration": false},
			},
			"textDocument": map[string]any{
				"publishDiagnostics": map[string]any{"relatedInformation": true, "versionSupport": true},
				"rename":             map[string]any{"prepareSupport": true},
			},
		},
	}
	server, err := startChildServer("gopls", goplsCommand(a.command, remote), a.root, initialize, a.serverRequest, a.serverNotification)
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		a.startErr = err
		a.warnLocked(err)
		return nil
	}
	a.server, a.servingAddress = server, remote
	go a.watch(server, remote)
	return server
}

// watch handles the end of a gopls the adapter did not close. A forwarder
// whose daemon is unreachable or failed is replaced by a private gopls,
// which gets every overlay again; a private gopls that fails is reported.
func (a *GoplsAdapter) watch(server *childServer, remote string) {
	select {
	case <-server.Done():
	case <-a.stop:
		return
	}
	err := server.Err()
	if err == nil || err.Error() == "closed" {
		return
	}
	if remote == "" {
		a.mu.Lock()
		a.warnLocked(err)
		a.mu.Unlock()
		return
	}
	select {
	case <-a.stop:
		return
	default:
	}
	a.fallBack(remote, err)
	_ = server.Close()
	a.mu.Lock()
	if a.server == server {
		a.server, a.servingAddress = nil, ""
	}
	// The daemon's diagnostics are gone with it; the private gopls
	// reports again.
	a.reports = make(map[string]goplsReport)
	dirs := make(map[string]bool)
	for path := range a.documents {
		dirs[filepath.Dir(path)] = true
	}
	for _, overlay := range a.generated {
		dirs[filepath.Dir(overlay.source)] = true
	}
	a.mu.Unlock()
	a.remap()
	for _, dir := range sortedKeys(dirs) {
		a.refresh(dir)
	}
}

// fallBack switches to a private gopls and logs why.
func (a *GoplsAdapter) fallBack(remote string, cause error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.private {
		return
	}
	a.private = true
	message := fmt.Sprintf("gopls daemon %s unavailable (%v); using a private gopls", remote, cause)
	fmt.Fprintln(os.Stderr, "ktml lsp: "+message)
	notification, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "window/logMessage", "params": map[string]any{"type": 3, "message": message}})
	a.queue = append(a.queue, notification)
	select {
	case a.queued <- struct{}{}:
	default:
	}
}

// drain sends the queued messages to gopls in order. Each message names
// the gopls it was made for; one made for a gopls that has since stopped
// fails without reaching its replacement.
func (a *GoplsAdapter) drain() {
	for {
		select {
		case message := <-a.outbox:
			message()
		case <-a.stop:
			return
		}
	}
}

// warnLocked tells the user once that gopls is unavailable.
func (a *GoplsAdapter) warnLocked(err error) {
	if a.warned {
		return
	}
	a.warned = true
	message := fmt.Sprintf("gopls unavailable (%v): Go type errors in generated code are not shown on KTML lines, and Go references are not included. Install it with `go install golang.org/x/tools/gopls@%s`, or set gopls.command in ktml.config.json.", err, GoplsPinnedVersion)
	fmt.Fprintln(os.Stderr, "ktml lsp: "+message)
	notification, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "window/showMessage", "params": map[string]any{"type": 3, "message": message}})
	a.queue = append(a.queue, notification)
	select {
	case a.queued <- struct{}{}:
	default:
	}
}

// serverRequest answers gopls's requests: its configuration is the default,
// and registrations and progress tokens are accepted.
func (a *GoplsAdapter) serverRequest(method string, params json.RawMessage) any {
	if method != "workspace/configuration" {
		return nil
	}
	var request struct {
		Items []json.RawMessage `json:"items"`
	}
	_ = json.Unmarshal(params, &request)
	results := make([]any, len(request.Items))
	for index := range results {
		results[index] = map[string]any{}
	}
	return results
}

func (a *GoplsAdapter) serverNotification(method string, params json.RawMessage) {
	switch method {
	case publishDiagnosticsMethod:
		a.diagnostics(params)
	case "window/logMessage", "window/showMessage":
		var logged struct {
			Type    int    `json:"type"`
			Message string `json:"message"`
		}
		if json.Unmarshal(params, &logged) == nil && logged.Type <= 1 {
			fmt.Fprintln(os.Stderr, "gopls: "+logged.Message)
		}
	}
}

type goplsDiagnostic struct {
	Range              lspRange        `json:"range"`
	Severity           int             `json:"severity"`
	Code               json.RawMessage `json:"code,omitempty"`
	Source             string          `json:"source"`
	Message            string          `json:"message"`
	RelatedInformation []struct {
		Location struct {
			URI   string   `json:"uri"`
			Range lspRange `json:"range"`
		} `json:"location"`
		Message string `json:"message"`
	} `json:"relatedInformation"`
}

// goplsReport is gopls's last diagnostics of one Go file. related holds,
// for each related location in a generated overlay, the overlay text it
// covered when the report arrived.
type goplsReport struct {
	version     *int
	diagnostics []goplsDiagnostic
	related     map[string]string
}

type mappedDiagnostic struct {
	uri   string
	value any
}

// diagnostics records gopls's diagnostics of one Go file and remaps.
func (a *GoplsAdapter) diagnostics(params json.RawMessage) {
	var published struct {
		URI         string            `json:"uri"`
		Version     *int              `json:"version"`
		Diagnostics []goplsDiagnostic `json:"diagnostics"`
	}
	if json.Unmarshal(params, &published) != nil || !strings.HasSuffix(published.URI, ".go") {
		return
	}
	a.mu.Lock()
	report := goplsReport{version: published.Version, diagnostics: published.Diagnostics, related: make(map[string]string)}
	for _, diagnostic := range published.Diagnostics {
		for _, information := range diagnostic.RelatedInformation {
			if target := a.generated[information.Location.URI]; target != nil {
				report.related[relatedKey(information.Location.URI, information.Location.Range)] = rangeText(target.text, information.Location.Range)
			}
		}
	}
	if len(published.Diagnostics) == 0 {
		delete(a.reports, published.URI)
	} else {
		a.reports[published.URI] = report
	}
	a.mu.Unlock()
	a.remap()
}

func relatedKey(uri string, location lspRange) string {
	return fmt.Sprintf("%s:%d:%d-%d:%d", uri, location.Start.Line, location.Start.Character, location.End.Line, location.End.Character)
}

func rangeText(text []byte, location lspRange) string {
	start := textposition.Offset(text, location.Start.Line, location.Start.Character)
	end := textposition.Offset(text, location.End.Line, location.End.Character)
	if start < 0 || end < start || end > len(text) {
		return ""
	}
	return string(text[start:end])
}

// mapReportLocked maps one Go file's report onto KTML. The diagnostics of a
// generated overlay map through its source map, only for the overlay's
// current version. Diagnostics of handwritten Go, such as a presenter, map
// where they relate to generated code, such as a declaration the presenter
// collides with, only while the overlay still has the text they related to.
// Stale overlays map nothing.
func (a *GoplsAdapter) mapReportLocked(goURI string, report goplsReport) []mappedDiagnostic {
	overlay := a.generated[goURI]
	if overlay != nil && (overlay.stale || report.version != nil && *report.version != overlay.version) {
		return nil
	}
	var mapped []mappedDiagnostic
	for _, diagnostic := range report.diagnostics {
		// Hints and suggestions about generated code are not actionable.
		if diagnostic.Severity == 0 || diagnostic.Severity > 2 {
			continue
		}
		if overlay != nil {
			ktmlURI, ktmlRange := a.mapLocked(overlay, diagnostic.Range)
			value := map[string]any{
				"range":    ktmlRange,
				"severity": diagnostic.Severity,
				"source":   goplsSource,
				"message":  diagnostic.Message,
				"relatedInformation": append([]any{map[string]any{
					"location": map[string]any{"uri": goURI, "range": diagnostic.Range},
					"message":  "generated Go",
				}}, a.relatedLocked(diagnostic)...),
			}
			if len(diagnostic.Code) > 0 {
				value["code"] = diagnostic.Code
			}
			mapped = append(mapped, mappedDiagnostic{ktmlURI, value})
			continue
		}
		for _, related := range diagnostic.RelatedInformation {
			target := a.generated[related.Location.URI]
			if target == nil || target.stale {
				continue
			}
			expected, recorded := report.related[relatedKey(related.Location.URI, related.Location.Range)]
			if !recorded || expected == "" || rangeText(target.text, related.Location.Range) != expected {
				continue
			}
			ktmlURI, ktmlRange := a.mapLocked(target, related.Location.Range)
			line, character := diagnostic.Range.Start.Line+1, diagnostic.Range.Start.Character+1
			mapped = append(mapped, mappedDiagnostic{ktmlURI, map[string]any{
				"range":    ktmlRange,
				"severity": diagnostic.Severity,
				"source":   goplsSource,
				"message":  fmt.Sprintf("%s (%s:%d:%d)", diagnostic.Message, goFileName(goURI), line, character),
				"relatedInformation": []any{map[string]any{
					"location": map[string]any{"uri": goURI, "range": diagnostic.Range},
					"message":  diagnostic.Message,
				}},
			}})
		}
	}
	return mapped
}

// relatedLocked maps a diagnostic's related locations in generated overlays
// to KTML and keeps the others.
func (a *GoplsAdapter) relatedLocked(diagnostic goplsDiagnostic) []any {
	var related []any
	for _, information := range diagnostic.RelatedInformation {
		location := map[string]any{"uri": information.Location.URI, "range": information.Location.Range}
		if target := a.generated[information.Location.URI]; target != nil && !target.stale {
			uri, mapped := a.mapLocked(target, information.Location.Range)
			location = map[string]any{"uri": uri, "range": mapped}
		}
		related = append(related, map[string]any{"location": location, "message": information.Message})
	}
	return related
}

func goFileName(uri string) string {
	parsed, err := url.Parse(uri)
	if err != nil {
		return uri
	}
	return filepath.Base(parsed.Path)
}

// mapLocked maps a range of generated Go to the KTML range of the construct
// it was generated from: the mapping on the same line at or before its
// start, else the first mapping on that line, else the nearest one before
// it. Code before every mapping, such as imports, maps to the document's
// start.
func (a *GoplsAdapter) mapLocked(overlay *generatedGo, goRange lspRange) (string, lspRange) {
	ktmlURI := fileURI(overlay.source)
	offset := textposition.Offset(overlay.text, goRange.Start.Line, goRange.Start.Character)
	mapping, ok := mappingAt(overlay.text, overlay.mappings, offset)
	if !ok {
		return ktmlURI, lspRange{}
	}
	return ktmlURI, byteRange(overlay.sourceText, mapping.Source.Start, mapping.Source.End)
}

func mappingAt(text []byte, mappings []gogen.SourceMapping, offset int) (gogen.SourceMapping, bool) {
	lineStart := bytes.LastIndexByte(text[:min(offset, len(text))], '\n') + 1
	lineEnd := len(text)
	if end := bytes.IndexByte(text[min(offset, len(text)):], '\n'); end >= 0 {
		lineEnd = offset + end
	}
	var before, first *gogen.SourceMapping
	for index := range mappings {
		mapping := &mappings[index]
		if mapping.Go >= lineStart && mapping.Go < lineEnd {
			if first == nil {
				first = mapping
			}
			if mapping.Go <= offset {
				before = mapping
			}
		}
	}
	switch {
	case before != nil:
		return *before, true
	case first != nil:
		return *first, true
	}
	for index := len(mappings) - 1; index >= 0; index-- {
		if mappings[index].Go < lineStart {
			return mappings[index], true
		}
	}
	return gogen.SourceMapping{}, false
}
