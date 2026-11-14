package htmx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/karl-tye/ktml/internal/symbolindex"
	"github.com/karl-tye/ktml/internal/textposition"
)

// The plugin runs in two roles. The serving instance answers LSP requests
// from the route index it holds; it never scans Go itself. It hands the
// scans to the host as background jobs ($/ktml/pendingJob), which the host
// runs on a worker instance of this plugin ($/ktml/runJob) and returns
// ($/ktml/jobResult). The worker keeps the Go workspace, so a refresh
// re-parses only the files that changed.
var lspState = struct {
	sync.Mutex
	settings Settings
	index    RouteIndex
	// ready is false until the first scan result arrives; until then no
	// route diagnostics are published, so nothing is falsely unknown.
	ready       bool
	err         error
	configured  bool
	pendingScan bool
	pendingGo   map[string]bool
	jobRunning  bool
	documents   map[string]string
	publishedGo map[string]bool
	ktmlFiles   map[string]*ktmlFile
	// indexedGo are the Go documents whose routes the symbol index holds.
	indexedGo map[string]bool
}{documents: make(map[string]string), publishedGo: make(map[string]bool), ktmlFiles: make(map[string]*ktmlFile), pendingGo: make(map[string]bool)}

// workerState is the worker role's Go workspace.
var workerState struct {
	sync.Mutex
	workspace *goWorkspace
}

type ktmlFile struct {
	modTime time.Time
	size    int64
	source  string
}

// Configure validates the settings and queues the initial scan. It never
// scans, so loading the plugin stays fast.
func Configure(data []byte) error {
	settings, err := ParseSettings(data)
	lspState.Lock()
	defer lspState.Unlock()
	if err != nil {
		lspState.err = err
		return err
	}
	lspState.settings = settings
	lspState.configured = true
	lspState.index, lspState.ready, lspState.err = RouteIndex{}, false, nil
	lspState.pendingScan, lspState.jobRunning = true, false
	lspState.pendingGo = make(map[string]bool)
	lspState.publishedGo = make(map[string]bool)
	lspState.ktmlFiles = make(map[string]*ktmlFile)
	lspState.indexedGo = nil
	workerState.Lock()
	workerState.workspace = newGoWorkspace(settings.Chi)
	workerState.Unlock()
	return nil
}

func HandleLSP(method string, payload []byte) ([]byte, error) {
	switch method {
	case "textDocument/didOpen", "textDocument/didChange", "textDocument/didClose":
		return updateDocument(method, payload)
	case "workspace/didChangeWatchedFiles":
		return nil, watchedFilesChanged(payload)
	case pendingJobMethod:
		return pendingJob()
	case runJobMethod:
		return runJob(payload)
	case jobResultMethod:
		return jobResult(payload)
	case "textDocument/completion":
		return completion(payload)
	case "textDocument/definition":
		return definition(payload)
	case "textDocument/references":
		return references(payload)
	case "textDocument/hover":
		return hover(payload)
	case "textDocument/prepareRename":
		return prepareRename(payload)
	case "textDocument/rename":
		return rename(payload)
	case symbolindex.Method:
		return indexDocuments(payload)
	default:
		return nil, nil
	}
}

const (
	pendingJobMethod = "$/ktml/pendingJob"
	runJobMethod     = "$/ktml/runJob"
	jobResultMethod  = "$/ktml/jobResult"
)

type routeJob struct {
	// Kind is "scan" for the whole of chi.dir or "refresh" for Paths.
	Kind  string   `json:"kind"`
	Paths []string `json:"paths,omitempty"`
}

type routeJobResult struct {
	Index RouteIndex `json:"index"`
	Error string     `json:"error,omitempty"`
}

// watchedFilesChanged queues the changed Go files under chi.dir for the
// next refresh job.
func watchedFilesChanged(payload []byte) error {
	var params struct {
		Changes []struct {
			URI string `json:"uri"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(payload, &params); err != nil {
		return err
	}
	lspState.Lock()
	defer lspState.Unlock()
	dir := filepath.Clean(lspState.settings.Chi.Dir)
	for _, change := range params.Changes {
		path, err := uriPath(change.URI)
		if err != nil || !strings.HasSuffix(path, ".go") {
			continue
		}
		if relative, err := filepath.Rel(dir, path); err == nil && !strings.HasPrefix(relative, "..") {
			lspState.pendingGo[path] = true
		}
	}
	return nil
}

func pendingJob() ([]byte, error) {
	lspState.Lock()
	defer lspState.Unlock()
	if !lspState.configured || lspState.jobRunning {
		return nil, nil
	}
	var job routeJob
	switch {
	case lspState.pendingScan:
		job = routeJob{Kind: "scan"}
		lspState.pendingScan = false
		lspState.pendingGo = make(map[string]bool)
	case len(lspState.pendingGo) > 0:
		job = routeJob{Kind: "refresh"}
		for path := range lspState.pendingGo {
			job.Paths = append(job.Paths, path)
		}
		sort.Strings(job.Paths)
		lspState.pendingGo = make(map[string]bool)
	default:
		return nil, nil
	}
	lspState.jobRunning = true
	return json.Marshal(job)
}

// runJob is the worker role: it scans or refreshes the Go workspace.
func runJob(payload []byte) ([]byte, error) {
	var job routeJob
	if err := json.Unmarshal(payload, &job); err != nil {
		return nil, err
	}
	workerState.Lock()
	defer workerState.Unlock()
	if workerState.workspace == nil {
		return json.Marshal(routeJobResult{Error: "htmx plugin is not configured"})
	}
	var index RouteIndex
	var err error
	if job.Kind == "refresh" {
		index, err = workerState.workspace.refreshPaths(job.Paths)
	} else {
		index, _, err = workerState.workspace.refresh()
	}
	result := routeJobResult{Index: index}
	if err != nil {
		result.Error = err.Error()
	}
	return json.Marshal(result)
}

// jobResult installs a job's index and republishes every open document's
// diagnostics and the Go files' analysis problems.
func jobResult(payload []byte) ([]byte, error) {
	var delivered struct {
		Result routeJobResult `json:"result"`
		Error  string         `json:"error"`
	}
	if err := json.Unmarshal(payload, &delivered); err != nil {
		return nil, err
	}
	lspState.Lock()
	defer lspState.Unlock()
	lspState.jobRunning = false
	switch {
	case delivered.Error != "":
		lspState.err = fmt.Errorf("route discovery failed: %s", delivered.Error)
	case delivered.Result.Error != "":
		lspState.err = errors.New(delivered.Result.Error)
	default:
		lspState.index, lspState.err = delivered.Result.Index, nil
	}
	lspState.ready = true
	return marshalNotifications(allDiagnosticsLocked(""))
}

func uriPath(uri string) (string, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return "", err
	}
	return url.PathUnescape(parsed.Path)
}

func updateDocument(method string, payload []byte) ([]byte, error) {
	var request struct {
		TextDocument struct {
			URI  string `json:"uri"`
			Text string `json:"text"`
		} `json:"textDocument"`
		ContentChanges []struct {
			Text string `json:"text"`
		} `json:"contentChanges"`
	}
	if err := json.Unmarshal(payload, &request); err != nil {
		return nil, err
	}

	lspState.Lock()
	defer lspState.Unlock()
	uri := request.TextDocument.URI
	if method == "textDocument/didClose" {
		delete(lspState.documents, uri)
		return marshalNotifications([]map[string]any{publishDiagnostics(uri, nil)})
	}
	text := request.TextDocument.Text
	if len(request.ContentChanges) > 0 {
		text = request.ContentChanges[len(request.ContentChanges)-1].Text
	}
	lspState.documents[uri] = text
	return marshalNotifications([]map[string]any{publishDiagnostics(uri, currentDiagnosticsLocked(text))})
}

// allDiagnosticsLocked publishes every open document, first, then Go files
// with route analysis problems, clearing Go files that no longer have any.
func allDiagnosticsLocked(first string) []map[string]any {
	uris := make([]string, 0, len(lspState.documents))
	for uri := range lspState.documents {
		if uri != first {
			uris = append(uris, uri)
		}
	}
	sort.Strings(uris)
	if first != "" {
		uris = append([]string{first}, uris...)
	}
	notifications := make([]map[string]any, 0, len(uris))
	for _, uri := range uris {
		notifications = append(notifications, publishDiagnostics(uri, currentDiagnosticsLocked(lspState.documents[uri])))
	}
	byURI := make(map[string][]lspDiagnostic)
	for _, diagnostic := range lspState.index.Diagnostics {
		if diagnostic.Code == "go-parse" || diagnostic.Code == "go-read" {
			// gopls reports Go syntax errors.
			continue
		}
		byURI[diagnostic.Location.URI] = append(byURI[diagnostic.Location.URI], lspDiagnostic{
			Range:    diagnostic.Location.Range,
			Severity: 2,
			Code:     diagnostic.Code,
			Source:   "ktml-htmx",
			Message:  diagnostic.Message,
		})
	}
	goURIs := make([]string, 0, len(byURI)+len(lspState.publishedGo))
	for uri := range byURI {
		goURIs = append(goURIs, uri)
	}
	for uri := range lspState.publishedGo {
		if _, ok := byURI[uri]; !ok {
			goURIs = append(goURIs, uri)
		}
	}
	sort.Strings(goURIs)
	published := make(map[string]bool, len(byURI))
	for _, uri := range goURIs {
		notifications = append(notifications, publishDiagnostics(uri, byURI[uri]))
		if len(byURI[uri]) > 0 {
			published[uri] = true
		}
	}
	lspState.publishedGo = published
	return notifications
}

func currentDiagnosticsLocked(source string) []lspDiagnostic {
	if !lspState.ready {
		return nil
	}
	return documentDiagnostics(source, lspState.index, lspState.err)
}

type lspDiagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity"`
	Code     string `json:"code"`
	Source   string `json:"source"`
	Message  string `json:"message"`
}

func documentDiagnostics(source string, index RouteIndex, indexErr error) []lspDiagnostic {
	if indexErr != nil {
		return nil
	}
	var diagnostics []lspDiagnostic
	for _, attribute := range scanAttributes(source) {
		for _, problem := range diagnoseAttribute(attribute, index) {
			diagnostics = append(diagnostics, lspDiagnostic{
				Range:    offsetRange(source, problem.start, problem.end),
				Severity: problem.severity,
				Code:     problem.code,
				Source:   "ktml-htmx",
				Message:  problem.message,
			})
		}
	}
	return diagnostics
}

func publishDiagnostics(uri string, diagnostics []lspDiagnostic) map[string]any {
	if diagnostics == nil {
		diagnostics = []lspDiagnostic{}
	}
	return map[string]any{
		"jsonrpc": "2.0",
		"method":  "textDocument/publishDiagnostics",
		"params": map[string]any{
			"uri":         uri,
			"diagnostics": diagnostics,
		},
	}
}

func marshalNotifications(notifications []map[string]any) ([]byte, error) {
	if len(notifications) == 1 {
		return json.Marshal(notifications[0])
	}
	return json.Marshal(notifications)
}

// requestState returns the index, whether it is ready, and a snapshot of
// the open documents.
func requestState() (RouteIndex, map[string]string, Settings, error) {
	lspState.Lock()
	defer lspState.Unlock()
	documents := make(map[string]string, len(lspState.documents))
	for uri, source := range lspState.documents {
		documents[uri] = source
	}
	if !lspState.configured && lspState.err == nil {
		return RouteIndex{}, documents, lspState.settings, errors.New("htmx plugin is not configured")
	}
	return lspState.index, documents, lspState.settings, lspState.err
}

func indexReady() bool {
	lspState.Lock()
	defer lspState.Unlock()
	return lspState.ready
}

func completion(payload []byte) ([]byte, error) {
	request, err := parseTextDocumentRequest(payload)
	if err != nil {
		return nil, err
	}
	source, err := documentSource(request.URI)
	if err != nil {
		return completionResponse(request.ID, []map[string]any{})
	}
	offset := positionOffset(source, request.Position)
	method, prefix, ok := completionContext(source, offset)
	if !ok {
		return completionResponse(request.ID, []map[string]any{})
	}
	index, _, _, stateErr := requestState()
	if stateErr != nil {
		return errorResponse(request.ID, stateErr), nil
	}
	replace := offsetRange(source, offset-len(prefix), offset)
	items := make([]map[string]any, 0)
	seen := make(map[string]bool)
	for _, route := range index.Routes {
		if !methodAllows(route, method) || route.Subtree || strings.HasSuffix(route.Path, "*") {
			continue
		}
		if !strings.HasPrefix(route.Path, prefix) || seen[route.Path] {
			continue
		}
		seen[route.Path] = true
		items = append(items, map[string]any{
			"label":            route.Path,
			"kind":             12,
			"detail":           route.Method + " " + route.Handler,
			"filterText":       route.Path,
			"insertTextFormat": 2,
			"textEdit":         map[string]any{"range": replace, "newText": routeSnippet(route.Path)},
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i]["label"].(string) < items[j]["label"].(string)
	})
	return response(request.ID, map[string]any{"isIncomplete": !indexReady(), "items": items})
}

// routeSnippet turns each path parameter into a tab stop whose default is
// the parameter itself, ready to be replaced by a KTML interpolation.
func routeSnippet(path string) string {
	var builder strings.Builder
	stop := 1
	for _, segment := range routeSegments(path) {
		builder.WriteByte('/')
		text := strings.NewReplacer(`\`, `\\`, `$`, `\$`, `}`, `\}`).Replace(segment.Text)
		if segment.Dynamic && segment.Text != "*" {
			fmt.Fprintf(&builder, "${%d:%s}", stop, text)
			stop++
			continue
		}
		builder.WriteString(text)
	}
	if path == "/" {
		return "/"
	}
	return builder.String()
}

func completionResponse(id json.RawMessage, items []map[string]any) ([]byte, error) {
	return response(id, map[string]any{"isIncomplete": false, "items": items})
}

func definition(payload []byte) ([]byte, error) {
	request, err := parseTextDocumentRequest(payload)
	if err != nil {
		return nil, err
	}
	source, err := documentSource(request.URI)
	if err != nil {
		return response(request.ID, nil)
	}
	attribute, ok := attributeAt(source, positionOffset(source, request.Position))
	if !ok {
		return response(request.ID, nil)
	}
	index, _, _, stateErr := requestState()
	if stateErr != nil {
		return errorResponse(request.ID, stateErr), nil
	}
	routes := attributeRoutes(attribute, index)
	switch len(routes) {
	case 0:
		return response(request.ID, nil)
	case 1:
		return response(request.ID, routes[0].Registration)
	}
	locations := make([]Location, 0, len(routes))
	for _, route := range routes {
		locations = append(locations, route.Registration)
	}
	return response(request.ID, locations)
}

// attributeRoutes returns the routes an attribute reaches with its method,
// or, when none does, with any method.
func attributeRoutes(attribute htmxAttribute, index RouteIndex) []Route {
	url, ok := parseAttributeURL(attribute.Value, attribute.Literal)
	if !ok {
		return nil
	}
	compatible, other := routesFor(url, attribute.Method, index.Routes)
	if len(compatible) > 0 {
		return compatible
	}
	return other
}

func hover(payload []byte) ([]byte, error) {
	request, err := parseTextDocumentRequest(payload)
	if err != nil {
		return nil, err
	}
	source, err := documentSource(request.URI)
	if err != nil {
		return response(request.ID, nil)
	}
	attribute, ok := attributeAt(source, positionOffset(source, request.Position))
	if !ok {
		return response(request.ID, nil)
	}
	index, _, _, stateErr := requestState()
	if stateErr != nil {
		return response(request.ID, nil)
	}
	routes := attributeRoutes(attribute, index)
	if len(routes) == 0 {
		return response(request.ID, nil)
	}
	var lines []string
	for _, route := range routes {
		lines = append(lines, fmt.Sprintf("`%s %s` → `%s` (%s:%d)", route.Method, route.Path, route.Handler, filepath.Base(strings.TrimPrefix(route.Registration.URI, "file://")), route.Registration.Range.Start.Line+1))
	}
	return response(request.ID, map[string]any{
		"contents": map[string]any{"kind": "markdown", "value": strings.Join(lines, "\n\n")},
		"range":    offsetRange(source, attribute.ValueStart, attribute.ValueEnd),
	})
}

// routeTarget is the set of routes a references or rename request is about,
// found from a Chi registration or an HTMX attribute.
type routeTarget struct {
	Routes []Route
	Range  Range
}

func targetAt(index RouteIndex, uri string, position Position) (routeTarget, bool) {
	// The innermost registration call under the cursor wins; a registration
	// reached under several prefixes contributes one route per prefix.
	var innermost *Route
	for i, route := range index.Routes {
		if route.Declaration.URI != uri || !positionInRange(position, route.Declaration.Range) {
			continue
		}
		if innermost == nil || positionInRange(route.Declaration.Range.Start, innermost.Declaration.Range) && route.Declaration.Range != innermost.Declaration.Range {
			innermost = &index.Routes[i]
		}
	}
	if innermost != nil {
		var routes []Route
		for _, route := range index.Routes {
			if route.Registration == innermost.Registration {
				routes = append(routes, route)
			}
		}
		return routeTarget{Routes: routes, Range: innermost.Registration.Range}, true
	}
	var routes []Route
	source, err := documentSource(uri)
	if err != nil {
		return routeTarget{}, false
	}
	attribute, ok := attributeAt(source, positionOffset(source, position))
	if !ok {
		return routeTarget{}, false
	}
	routes = attributeRoutes(attribute, index)
	if len(routes) == 0 {
		return routeTarget{}, false
	}
	return routeTarget{Routes: routes, Range: offsetRange(source, attribute.ValueStart, attribute.ValueEnd)}, true
}

// consumer is an HTMX attribute in a KTML document that reaches a route.
type consumer struct {
	URI       string
	Source    string
	Attribute htmxAttribute
	URL       attributeURL
}

func consumersOf(routes []Route, documents map[string]string, settings Settings) []consumer {
	var consumers []consumer
	for uri, source := range ktmlSources(documents, settings) {
		for _, attribute := range scanAttributes(source) {
			url, ok := parseAttributeURL(attribute.Value, attribute.Literal)
			if !ok {
				continue
			}
			for _, route := range routes {
				if methodAllows(route, attribute.Method) && matchRoute(url, route).ok {
					consumers = append(consumers, consumer{URI: uri, Source: source, Attribute: attribute, URL: url})
					break
				}
			}
		}
	}
	sort.Slice(consumers, func(i, j int) bool {
		if consumers[i].URI != consumers[j].URI {
			return consumers[i].URI < consumers[j].URI
		}
		return consumers[i].Attribute.ValueStart < consumers[j].Attribute.ValueStart
	})
	return consumers
}

// ktmlSources returns every KTML document under the plugin's read mounts,
// with open documents taking precedence over disk.
func ktmlSources(documents map[string]string, settings Settings) map[string]string {
	roots := make([]string, 0, len(settings.Host.PathMap))
	for _, root := range settings.Host.PathMap {
		roots = append(roots, root)
	}
	if len(roots) == 0 && settings.Chi.Dir != "" {
		roots = append(roots, settings.Chi.Dir)
	}
	sort.Strings(roots)
	sources := make(map[string]string)
	lspState.Lock()
	defer lspState.Unlock()
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if entry.IsDir() {
				name := entry.Name()
				if path != root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".ktml") || len(sources) >= 10000 {
				return nil
			}
			uri := fileURI(path)
			if _, ok := sources[uri]; ok {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return nil
			}
			cached := lspState.ktmlFiles[path]
			if cached == nil || !cached.modTime.Equal(info.ModTime()) || cached.size != info.Size() {
				content, err := os.ReadFile(path)
				if err != nil {
					return nil
				}
				cached = &ktmlFile{modTime: info.ModTime(), size: info.Size(), source: string(content)}
				lspState.ktmlFiles[path] = cached
			}
			sources[uri] = cached.source
			return nil
		})
	}
	for uri, source := range documents {
		sources[uri] = source
	}
	return sources
}

func references(payload []byte) ([]byte, error) {
	request, err := parseReferenceRequest(payload)
	if err != nil {
		return nil, err
	}
	index, documents, settings, stateErr := requestState()
	if stateErr != nil {
		return errorResponse(request.ID, stateErr), nil
	}
	target, ok := targetAt(index, request.URI, request.Position)
	if !ok {
		return response(request.ID, []Location{})
	}
	locations := make([]Location, 0)
	if request.IncludeDeclaration {
		seen := make(map[Location]bool)
		for _, route := range target.Routes {
			if !seen[route.Registration] {
				seen[route.Registration] = true
				locations = append(locations, route.Registration)
			}
		}
	}
	for _, consumer := range consumersOf(target.Routes, documents, settings) {
		locations = append(locations, Location{
			URI:   consumer.URI,
			Range: offsetRange(consumer.Source, consumer.Attribute.ValueStart, consumer.Attribute.ValueEnd),
		})
	}
	return response(request.ID, locations)
}

// renamePlan checks that a route can be renamed safely: every registration
// is a string literal under a single prefix and all share one path.
func renamePlan(target routeTarget) (path string, err error) {
	for _, route := range target.Routes {
		switch {
		case route.Subtree || strings.Contains(route.Path, "*"):
			return "", fmt.Errorf("%s %s is a wildcard route and cannot be renamed", route.Method, route.Path)
		case !route.Editable:
			return "", fmt.Errorf("%s %s is not registered with a string literal under a single prefix, so it cannot be renamed safely", route.Method, route.Path)
		case route.SlashOptional:
			return "", fmt.Errorf("%s %s is registered as \"/\" below its prefix; rename the enclosing Route prefix instead", route.Method, route.Path)
		case path != "" && route.Path != path:
			return "", fmt.Errorf("the cursor reaches several routes (%s and %s); rename from the Chi registration", path, route.Path)
		}
		path = route.Path
	}
	if path == "" {
		return "", errors.New("no Chi route to rename")
	}
	return path, nil
}

func prepareRename(payload []byte) ([]byte, error) {
	request, err := parseTextDocumentRequest(payload)
	if err != nil {
		return nil, err
	}
	index, _, _, stateErr := requestState()
	if stateErr != nil {
		return errorResponse(request.ID, stateErr), nil
	}
	target, ok := targetAt(index, request.URI, request.Position)
	if !ok {
		return response(request.ID, nil)
	}
	path, err := renamePlan(target)
	if err != nil {
		return requestFailed(request.ID, err), nil
	}
	return response(request.ID, map[string]any{"range": target.Range, "placeholder": path})
}

func rename(payload []byte) ([]byte, error) {
	request, newName, err := parseRenameRequest(payload)
	if err != nil {
		return nil, err
	}
	index, documents, settings, stateErr := requestState()
	if stateErr != nil {
		return errorResponse(request.ID, stateErr), nil
	}
	target, ok := targetAt(index, request.URI, request.Position)
	if !ok {
		return response(request.ID, nil)
	}
	oldPath, err := renamePlan(target)
	if err != nil {
		return requestFailed(request.ID, err), nil
	}
	if err := validateRenamedPath(oldPath, newName); err != nil {
		return requestFailed(request.ID, err), nil
	}

	changes := make(map[string][]map[string]any)
	seen := make(map[Location]bool)
	for _, route := range target.Routes {
		if seen[route.Registration] {
			continue
		}
		seen[route.Registration] = true
		prefix := strings.TrimSuffix(route.Prefix, "/")
		if prefix != "" && !strings.HasPrefix(newName, prefix+"/") {
			return requestFailed(request.ID, fmt.Errorf("%s is registered below the prefix %s; a new path must keep that prefix", oldPath, route.Prefix)), nil
		}
		changes[route.Registration.URI] = append(changes[route.Registration.URI], map[string]any{
			"range":   route.Registration.Range,
			"newText": quoteLiteral(strings.TrimPrefix(newName, prefix), route.LiteralRaw),
		})
	}
	for _, consumer := range consumersOf(target.Routes, documents, settings) {
		rewritten, ok := rewriteAttribute(consumer.URL, oldPath, newName)
		if !ok {
			return requestFailed(request.ID, fmt.Errorf("%s in %s cannot be rewritten for %s", consumer.Attribute.Value, consumer.URI, newName)), nil
		}
		if consumer.Attribute.Literal && containsInterpolation(rewritten) {
			return requestFailed(request.ID, fmt.Errorf("%s in %s is a literal value", consumer.Attribute.Value, consumer.URI)), nil
		}
		changes[consumer.URI] = append(changes[consumer.URI], map[string]any{
			"range":   offsetRange(consumer.Source, consumer.Attribute.ValueStart, consumer.Attribute.ValueEnd),
			"newText": rewritten,
		})
	}
	applyRename(seen, newName)
	return response(request.ID, map[string]any{"changes": changes})
}

// applyRename updates the index for the edits a rename returns, so the new
// path navigates before the editor saves the Go file and a rescan sees it.
func applyRename(registrations map[Location]bool, newName string) {
	lspState.Lock()
	defer lspState.Unlock()
	for i := range lspState.index.Routes {
		route := &lspState.index.Routes[i]
		if !registrations[route.Registration] {
			continue
		}
		literal := strings.TrimPrefix(newName, strings.TrimSuffix(route.Prefix, "/"))
		oldWidth := route.Registration.Range.End.Character - route.Registration.Range.Start.Character
		newWidth := len(quoteLiteral(literal, route.LiteralRaw))
		route.Path, route.Literal, route.Params = newName, literal, routeParams(newName)
		if route.Registration.Range.Start.Line == route.Registration.Range.End.Line {
			route.Registration.Range.End.Character = route.Registration.Range.Start.Character + newWidth
			if route.Declaration.Range.End.Line == route.Registration.Range.End.Line {
				route.Declaration.Range.End.Character += newWidth - oldWidth
			}
		}
	}
}

// validateRenamedPath accepts a rooted Chi pattern with the same parameters.
func validateRenamedPath(oldPath, newPath string) error {
	// Handlers read parameters by name, so a rename keeps them in order.
	if !strings.HasPrefix(newPath, "/") || strings.ContainsAny(newPath, "\"`\r\n?#") {
		return fmt.Errorf("%q is not a Chi route path; it must begin with / and contain no quotes, query, or fragment", newPath)
	}
	if strings.Contains(newPath, "*") {
		return fmt.Errorf("%q adds a wildcard; renaming cannot change the route's shape", newPath)
	}
	oldParams, newParams := routeParams(oldPath), routeParams(newPath)
	if strings.Join(oldParams, ",") != strings.Join(newParams, ",") {
		return fmt.Errorf("a route rename must keep the path parameters %v of %s in order, but %s has %v; handlers read parameters by name", oldParams, oldPath, newPath, newParams)
	}
	return nil
}

func parseRenameRequest(payload []byte) (textDocumentRequest, string, error) {
	request, err := parseTextDocumentRequest(payload)
	if err != nil {
		return textDocumentRequest{}, "", err
	}
	var rename struct {
		NewName string `json:"newName"`
	}
	if err := json.Unmarshal(payload, &rename); err != nil {
		return textDocumentRequest{}, "", err
	}
	return request, rename.NewName, nil
}

type referenceRequest struct {
	textDocumentRequest
	IncludeDeclaration bool
}

func parseReferenceRequest(payload []byte) (referenceRequest, error) {
	request, err := parseTextDocumentRequest(payload)
	if err != nil {
		return referenceRequest{}, err
	}
	var context struct {
		Context struct {
			IncludeDeclaration bool `json:"includeDeclaration"`
		} `json:"context"`
	}
	if err := json.Unmarshal(payload, &context); err != nil {
		return referenceRequest{}, err
	}
	return referenceRequest{textDocumentRequest: request, IncludeDeclaration: context.Context.IncludeDeclaration}, nil
}

func positionInRange(position Position, target Range) bool {
	return comparePosition(position, target.Start) >= 0 && comparePosition(position, target.End) <= 0
}

func comparePosition(left, right Position) int {
	if left.Line != right.Line {
		return left.Line - right.Line
	}
	return left.Character - right.Character
}

type textDocumentRequest struct {
	ID       json.RawMessage
	URI      string
	Position Position
}

func parseTextDocumentRequest(payload []byte) (textDocumentRequest, error) {
	var request struct {
		ID           json.RawMessage `json:"id"`
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		Position Position `json:"position"`
	}
	if err := json.Unmarshal(payload, &request); err != nil {
		return textDocumentRequest{}, err
	}
	return textDocumentRequest{ID: request.ID, URI: request.TextDocument.URI, Position: request.Position}, nil
}

func documentSource(uri string) (string, error) {
	lspState.Lock()
	text, ok := lspState.documents[uri]
	lspState.Unlock()
	if ok {
		return text, nil
	}
	parsed, err := url.Parse(uri)
	if err != nil {
		return "", err
	}
	path, err := url.PathUnescape(parsed.Path)
	if err != nil {
		return "", err
	}
	content, err := os.ReadFile(path)
	return string(content), err
}

func completionContext(source string, offset int) (method, prefix string, ok bool) {
	start := strings.LastIndex(source[:offset], "\n") + 1
	match := completeAttributePattern.FindStringSubmatch(source[start:offset])
	if len(match) != 4 {
		return "", "", false
	}
	prefix = match[2]
	if prefix == "" {
		prefix = match[3]
	}
	return strings.ToUpper(match[1]), prefix, true
}

func positionOffset(source string, position Position) int {
	return textposition.Offset([]byte(source), position.Line, position.Character)
}

func offsetRange(source string, start, end int) Range {
	return Range{Start: offsetPosition(source, start), End: offsetPosition(source, end)}
}

func offsetPosition(source string, offset int) Position {
	line, character := textposition.Position([]byte(source), offset)
	return Position{Line: line, Character: character}
}

func response(id json.RawMessage, result any) ([]byte, error) {
	return json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func requestFailed(id json.RawMessage, err error) []byte {
	result, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": -32803, "message": err.Error()},
	})
	return result
}

func errorResponse(id json.RawMessage, err error) []byte {
	result, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    -32002,
			"message": fmt.Sprintf("HTMX route index unavailable: %v", err),
		},
	})
	return result
}
