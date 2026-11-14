// Package explorer is the KTML Component Explorer (PLAN Phase 5): a
// development-only server that lists the stories of a workspace, renders
// each one through the development interpreter in an isolated preview
// frame, derives typed controls from the component's inputs, and applies
// story and component edits through the same HMR swap and patch path as
// `ktml dev`. Only the ktml command links it; application binaries never do.
package explorer

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/karl-tye/ktml"
	"github.com/karl-tye/ktml/dev/hmr"
	"github.com/karl-tye/ktml/dev/interp"
	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/config"
	"github.com/karl-tye/ktml/internal/dev/livereload"
	"github.com/karl-tye/ktml/internal/dev/watcher"
	"github.com/karl-tye/ktml/internal/gopkg"
)

//go:embed assets
var assets embed.FS

// Prefix is the path prefix of the Explorer's own routes.
const Prefix = "/_explorer/"

// Options configure a Server.
type Options struct {
	// Root is the KTML workspace root.
	Root string
	// Config is the explorer section of ktml.config.json; nil uses the
	// bundled HTMX and Alpine.js and no stylesheet.
	Config *config.Explorer
	// Logf reports problems; nil means log.Printf.
	Logf func(format string, args ...any)
}

// Server is one Explorer: the HMR registry and interpreter program of its
// previews, a diagnostics view of the workspace, and the livereload hub the
// previews' client scripts connect to.
type Server struct {
	options Options
	root    string
	config  config.Explorer
	hmr     atomic.Pointer[hmr.HMR]
	static  *os.Root

	// mu serializes workspace changes and guards the fields after it.
	mu          sync.Mutex
	workspace   *compiler.Workspace
	version     int
	generation  uint64
	renderError map[string]renderFailure

	lr *livereload.Server

	// reach is the last reachability analysis, nil until the first;
	// reanalyze requests the next.
	reach      atomic.Pointer[reachability]
	reachError string
	// reachLogged is set once the first analysis is reported.
	reachLogged bool
	reanalyze   chan struct{}

	subscribers sync.Map // chan event -> struct{}

	// requests lists the previews' HTMX requests, which neutralize answers.
	requests requestLog

	// ensure serializes EnsureStory.
	ensure sync.Mutex
}

// event is one server-sent event to the open interfaces.
type event struct{ name, data string }

// renderFailure is the last render error of a story's preview.
type renderFailure struct {
	Message string
	Range   compiler.SourceRange
}

// New indexes the workspace and compiles its interpreter program.
func New(options Options) (*Server, error) {
	root, err := filepath.Abs(options.Root)
	if err != nil {
		return nil, err
	}
	options.Root = root
	if options.Logf == nil {
		options.Logf = log.Printf
	}
	s := &Server{options: options, root: root, workspace: compiler.NewWorkspace(root), renderError: make(map[string]renderFailure), reanalyze: make(chan struct{}, 1)}
	if options.Config != nil {
		s.config = *options.Config
	}
	if static := s.config.Static; static != nil {
		dir := filepath.Join(root, filepath.FromSlash(static.Dir))
		if s.static, err = os.OpenRoot(dir); err != nil {
			return nil, fmt.Errorf("explorer: static dir: %w", err)
		}
	}
	if _, err := s.workspace.Index(); err != nil {
		return nil, fmt.Errorf("explorer: index %s: %w", root, err)
	}
	registry, err := s.newHMR()
	if err != nil {
		return nil, err
	}
	s.hmr.Store(registry)
	return s, nil
}

// newHMR creates the registry and interpreter program of the previews from
// the files on disk. Previews are never generated code: every story and
// component renders through the interpreter.
func (s *Server) newHMR() (*hmr.HMR, error) {
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return nil, err
	}
	return hmr.New(hmr.Options{
		Root:   s.root,
		Token:  hex.EncodeToString(token),
		Interp: interp.Options{Packages: s.packages},
		Logf:   s.options.Logf,
	})
}

// registry is the current HMR registry and program.
func (s *Server) registry() *hmr.HMR { return s.hmr.Load() }

// Close releases the static directory.
func (s *Server) Close() error {
	if s.static != nil {
		return s.static.Close()
	}
	return nil
}

// ErrNotLoopback is returned for a listen address that is not loopback.
var ErrNotLoopback = errors.New("explorer: the Explorer only binds to a loopback address (127.0.0.1, ::1, or localhost)")

// Listen opens a TCP listener on a loopback address. Any other address is
// refused: the Explorer renders development fixtures and must not be
// reachable from the network.
func Listen(address string) (net.Listener, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if !loopbackHost(host) {
		return nil, ErrNotLoopback
	}
	return net.Listen("tcp", address)
}

func loopbackHost(host string) bool { return config.LoopbackHost(host) }

// Serve serves the Explorer on a loopback listener until ctx ends. Previews
// are rendered by an internal loopback backend behind the livereload proxy,
// exactly as `ktml dev` proxies an application, so the injected client,
// its session, and its patches are the same.
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	host, _, err := net.SplitHostPort(listener.Addr().String())
	if err != nil || !loopbackHost(host) {
		return ErrNotLoopback
	}
	backendListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	backend := &http.Server{Handler: s.backendHandler(), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = backend.Serve(backendListener) }()
	defer backend.Close()

	s.lr = livereload.NewServer("http://" + backendListener.Addr().String())
	s.lr.OnSession(func(session string, connected bool) {
		if connected {
			s.registry().Registry().Connect(session)
		} else {
			s.registry().Registry().Disconnect(session)
		}
	})
	front := &http.Server{Handler: loopbackOnly(s.lr), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = front.Serve(listener) }()
	defer front.Close()

	go s.analyzeLoop(ctx)
	s.requestAnalysis()

	events := watcher.New(s.root, []string{"*.ktml", "*.ktmlschema"}, nil, 50*time.Millisecond).Start(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-events:
			if !ok {
				<-ctx.Done()
				return nil
			}
			s.Apply(ctx, event.Files)
		}
	}
}

// backendHandler serves the internal preview backend: the Explorer's
// handler behind its HMR registry. It has the front server's Host check: the
// livereload proxy forwards the browser's loopback Host, and a DNS rebinding
// page that finds the backend's port is refused.
func (s *Server) backendHandler() http.Handler {
	handler := s.Handler()
	return loopbackOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.registry().Handler(handler).ServeHTTP(w, r)
	}))
}

// loopbackOnly refuses requests whose Host is not a loopback name, so a DNS
// rebinding page cannot reach the Explorer through the browser.
func loopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if split, _, err := net.SplitHostPort(r.Host); err == nil {
			host = split
		}
		if !loopbackHost(host) {
			http.Error(w, "the Explorer only answers loopback hosts", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Apply validates changed KTML sources and swaps them into the preview
// program through the HMR path. A valid change patches every live preview
// that renders it; a diagnostic keeps the last valid program and shows the
// diagnostic in the previews' overlay and in the interface.
func (s *Server) Apply(ctx context.Context, files []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var swap []hmr.SwapFile
	removed, components := false, false
	for _, file := range files {
		if !strings.HasSuffix(file, ".ktml") && !compiler.IsSchemaPath(file) {
			continue
		}
		path, err := filepath.Abs(file)
		if err != nil {
			continue
		}
		source, err := os.ReadFile(path)
		if err != nil {
			removed = true
			continue
		}
		s.version++
		if _, err := s.workspace.Update(path, s.version, source); err != nil {
			s.options.Logf("ktml explore: %s: %v", path, err)
			continue
		}
		swap = append(swap, hmr.SwapFile{Path: path, Source: string(source)})
		components = components || !compiler.IsStoryPath(path)
	}
	defer s.notify()
	if removed || components {
		// Calls between components may have changed what routes reach.
		defer s.requestAnalysis()
	}
	if removed {
		// A removed Document cannot be swapped out of a program; start again
		// from the files on disk and reload every preview.
		s.restart()
		return
	}
	if len(swap) == 0 {
		return
	}
	snapshot := s.workspace.Snapshot()
	if !snapshot.Valid() {
		s.showError(s.formatErrors(snapshot))
		return
	}
	s.generation++
	response, err := s.registry().Swap(ctx, hmr.SwapRequest{Generation: s.generation, Files: swap})
	if err != nil {
		s.showError(err.Error())
		return
	}
	if s.lr == nil {
		return
	}
	s.lr.ClearError()
	if len(response.Reload) > 0 {
		s.lr.FullReload()
		return
	}
	for _, patch := range response.Patches {
		s.lr.Patch(patch.Session, patch.Identity, patch.HTML, patch.Document)
	}
}

func (s *Server) restart() {
	s.workspace = compiler.NewWorkspace(s.root)
	if _, err := s.workspace.Index(); err != nil {
		s.options.Logf("ktml explore: %v", err)
		return
	}
	snapshot := s.workspace.Snapshot()
	if !snapshot.Valid() {
		s.showError(s.formatErrors(snapshot))
		return
	}
	registry, err := s.newHMR()
	if err != nil {
		s.showError(err.Error())
		return
	}
	s.hmr.Store(registry)
	if s.lr != nil {
		s.lr.ClearError()
		s.lr.FullReload()
	}
}

// packages names a directory's Go package for component IDs. A workspace
// outside a Go module still previews; its IDs come from its directories.
func (s *Server) packages(dir string) (string, error) {
	pkg, err := gopkg.Resolve(dir)
	if err != nil || pkg.ImportPath == "" {
		relative, _ := filepath.Rel(s.root, dir)
		return "ktml.explorer/" + filepath.ToSlash(relative), nil
	}
	return pkg.ImportPath, nil
}

func (s *Server) showError(message string) {
	if s.lr != nil {
		s.lr.ShowError(message)
	}
}

// notify tells every open interface that the workspace changed.
func (s *Server) notify() { s.broadcast("changed", "1") }

// broadcast sends an event to every open interface. A requests event never
// takes a stream's last free slot, so polling cannot crowd out a workspace
// change.
func (s *Server) broadcast(name, data string) {
	data = strings.NewReplacer("\r", "", "\n", "").Replace(data)
	s.subscribers.Range(func(key, _ any) bool {
		events := key.(chan event)
		if name == "requests" && len(events) >= cap(events)-1 {
			return true
		}
		select {
		case events <- event{name, data}:
		default:
		}
		return true
	})
}

// Handler serves the interface, previews, edits, and assets, and answers
// the previews' HTMX requests with 204 No Content. It must be
// wrapped by the HMR handler so previews render in page sessions.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.serveIndex)
	mux.HandleFunc("GET "+Prefix+"preview", s.servePreview)
	mux.HandleFunc("POST "+Prefix+"edit", s.serveEdit)
	mux.HandleFunc("GET "+Prefix+"state", s.serveState)
	mux.HandleFunc("GET "+Prefix+"open", s.serveOpen)
	mux.HandleFunc("GET "+Prefix+"events", s.serveEvents)
	mux.HandleFunc("GET "+Prefix+"requests", s.serveRequests)
	mux.HandleFunc("DELETE "+Prefix+"requests", s.clearRequests)
	mux.Handle("GET "+Prefix+"assets/", http.StripPrefix(Prefix, http.FileServerFS(assets)))
	if static := s.config.Static; static != nil && s.static != nil {
		prefix := strings.TrimSuffix(static.URL, "/") + "/"
		mux.Handle("GET "+prefix, http.StripPrefix(prefix, http.FileServerFS(s.static.FS())))
	}
	return s.neutralize(mux)
}

// storyKey is the stable URL key of a story: its Document path relative to
// the root, then Component.Name.
func (s *Server) storyKey(story compiler.Story) string {
	relative, err := filepath.Rel(s.root, story.File)
	if err != nil {
		relative = story.File
	}
	return filepath.ToSlash(relative) + "#" + story.ComponentName + "." + story.Name
}

// lookup finds a story of the current program by its key.
func (s *Server) lookup(key string) (*interp.Program, compiler.Story, compiler.Component, bool) {
	program := s.registry().Program()
	if program == nil {
		return nil, compiler.Story{}, compiler.Component{}, false
	}
	for _, story := range program.Stories() {
		if s.storyKey(story) == key {
			component, ok := program.Snapshot().Component(story.Component)
			return program, story, component, ok
		}
	}
	return program, compiler.Story{}, compiler.Component{}, false
}

// decodeValues reads the base64url JSON values of a request.
func decodeValues(encoded string) (map[string]any, error) {
	values := make(map[string]any)
	if encoded == "" {
		return values, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("invalid values: %w", err)
	}
	if err := decodeJSON(raw, &values); err != nil {
		return nil, fmt.Errorf("invalid values: %w", err)
	}
	return values, nil
}

func encodeValues(values map[string]any) string {
	raw, _ := json.Marshal(values)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// previewURL is the frame URL of a story with values.
func (s *Server) previewURL(key string, values map[string]any) string {
	query := url.Values{"story": {key}}
	if values != nil {
		query.Set("v", encodeValues(values))
	}
	return Prefix + "preview?" + query.Encode()
}

// currentValues merges the inputs a viewer changed over the story's values,
// dropping inputs the component no longer has. Inputs the viewer did not
// change follow the story document, so a story edit still reaches them.
func currentValues(component compiler.Component, story compiler.Story, requested map[string]any) map[string]any {
	values := storyValues(component, story)
	for name, value := range requested {
		if _, known := values[name]; known {
			values[name] = value
		}
	}
	return values
}

func (s *Server) servePreview(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("story")
	program, story, component, ok := s.lookup(key)
	if !ok {
		http.Error(w, "unknown story "+key, http.StatusNotFound)
		return
	}
	requested, err := decodeValues(r.URL.Query().Get("v"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Only the inputs the viewer changed override the story, so story edits
	// still reach the others when the preview is patched.
	typer := newTyper(program.Snapshot(), story)
	overrides, _ := typer.overrides(component, requested)
	var body bytes.Buffer
	props := program.StoryProps(story.ID, overrides)
	renderErr := s.registry().RenderComponent(r.Context(), &body, props, nil)
	s.mu.Lock()
	if renderErr != nil {
		s.renderError[key] = s.renderFailure(program, story, renderErr)
	} else {
		delete(s.renderError, key)
	}
	s.mu.Unlock()
	if renderErr != nil {
		s.notify()
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if renderErr == nil && isDocument(body.String()) {
		_, _ = w.Write(body.Bytes())
		return
	}
	_ = previewPage.Execute(w, previewData{
		Title:       story.ComponentName + "." + story.Name,
		Stylesheets: s.config.Stylesheets,
		Scripts:     s.scripts(),
		BodyClass:   s.config.BodyClass,
		Body:        trusted(body.String()),
		Error:       errorText(renderErr),
	})
}

// scripts are the configured preview scripts, or the bundled HTMX and
// Alpine.js.
func (s *Server) scripts() []string {
	if len(s.config.Scripts) > 0 {
		return s.config.Scripts
	}
	return []string{Prefix + "assets/htmx.min.js", Prefix + "assets/alpine.min.js"}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// renderFailure locates a render error at the component, input, or story
// it names.
func (s *Server) renderFailure(program *interp.Program, story compiler.Story, err error) renderFailure {
	failure := renderFailure{Message: err.Error(), Range: story.NameRange}
	var renderError *ktml.RenderError
	if !errors.As(err, &renderError) {
		return failure
	}
	id, ok := program.Symbol(renderError.Component)
	if !ok {
		return failure
	}
	if component, ok := program.Snapshot().Component(id); ok {
		failure.Range = component.Range
		for _, input := range component.Inputs {
			if input.Name == strings.SplitN(renderError.Input, ".", 2)[0] {
				failure.Range = input.Range
			}
		}
	} else if found, ok := program.Snapshot().Story(id); ok {
		failure.Range = found.NameRange
	}
	return failure
}

func isDocument(markup string) bool {
	trimmed := strings.TrimLeft(markup, " \t\r\n")
	return len(trimmed) >= 9 && strings.EqualFold(trimmed[:9], "<!doctype")
}

// editRequest is the body of POST /_explorer/edit.
type editRequest struct {
	Story  string         `json:"story"`
	Values map[string]any `json:"values"`
	Edit   Edit           `json:"edit"`
}

// editResponse returns the inputs the viewer changed after an edit, the
// controls when the edit changed their shape, and the preview to load.
type editResponse struct {
	Values   map[string]any `json:"values"`
	Controls string         `json:"controls,omitempty"`
	Preview  string         `json:"preview"`
	Error    string         `json:"error,omitempty"`
}

func (s *Server) serveEdit(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var request editRequest
	if err := decodeJSON(raw, &request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	program, story, component, ok := s.lookup(request.Story)
	if !ok {
		http.Error(w, "unknown story "+request.Story, http.StatusNotFound)
		return
	}
	typer := newTyper(program.Snapshot(), story)
	values := currentValues(component, story, request.Values)
	changed := make(map[string]any)
	for name := range request.Values {
		if _, known := values[name]; known {
			changed[name] = values[name]
		}
	}
	response := editResponse{Values: changed}
	if err := typer.apply(component, values, request.Edit); err != nil {
		response.Error = err.Error()
	} else if name, ok := request.Edit.Path[0].(string); ok {
		changed[name] = values[name]
	}
	_, failures := typer.overrides(component, values)
	if request.Edit.structural() || len(failures) > 0 || response.Error != "" {
		response.Controls = typer.controls(component, story, values, failures)
	}
	response.Preview = s.previewURL(request.Story, changed)
	writeJSON(w, response)
}

// stateResponse refreshes the interface after a workspace change, and
// swaps in a story chosen in the tree without a reload: Story is the
// selected story's key, the default story when none is named.
type stateResponse struct {
	Story       string         `json:"story"`
	Title       string         `json:"title"`
	Source      string         `json:"source"`
	Navigation  string         `json:"navigation"`
	Diagnostics string         `json:"diagnostics"`
	Controls    string         `json:"controls"`
	Values      map[string]any `json:"values"`
	Preview     string         `json:"preview"`
}

func (s *Server) serveState(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("story")
	requested, err := decodeValues(r.URL.Query().Get("v"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	view := s.view(key, requested)
	writeJSON(w, stateResponse{
		Story:       view.Story,
		Title:       view.Title,
		Source:      view.Source,
		Navigation:  string(view.Navigation),
		Diagnostics: string(view.Diagnostics),
		Controls:    string(view.Controls),
		Values:      view.Values,
		Preview:     view.Preview,
	})
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(value)
}

func (s *Server) serveEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush()
	}
	events := make(chan event, 4)
	s.subscribers.Store(events, struct{}{})
	defer s.subscribers.Delete(events)
	for {
		select {
		case <-r.Context().Done():
			return
		case e := <-events:
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.name, e.data)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request) {
	requested, err := decodeValues(r.URL.Query().Get("v"))
	if err != nil {
		requested = nil
	}
	view := s.view(r.URL.Query().Get("story"), requested)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set(livereload.NoClientHeader, "1")
	_ = indexPage.Execute(w, view)
}

// view is everything the interface shows for one selected story.
type view struct {
	Root        string
	Story       string
	Title       string
	Source      string
	Navigation  trusted
	Diagnostics trusted
	Controls    trusted
	Values      map[string]any
	ValuesJSON  template.JS
	Preview     string
}

func (s *Server) view(key string, requested map[string]any) view {
	program := s.registry().Program()
	result := view{Root: s.root}
	if key == "" && program != nil {
		for _, story := range program.Stories() {
			if story.Default {
				key = s.storyKey(story)
				break
			}
		}
	}
	program, story, component, ok := s.lookup(key)
	result.Navigation = trusted(renderNavigation(s.navigation(program, key)))
	if ok {
		typer := newTyper(program.Snapshot(), story)
		values := currentValues(component, story, requested)
		_, failures := typer.overrides(component, values)
		result.Story, result.Title = key, story.ComponentName+"."+story.Name
		result.Source = s.location(story.File, program.Snapshot().Source(story.File), story.NameRange.Start)
		result.Controls = trusted(typer.controls(component, story, values, failures))
		result.Values = make(map[string]any)
		for name := range requested {
			if _, known := values[name]; known {
				result.Values[name] = values[name]
			}
		}
		result.Preview = s.previewURL(key, result.Values)
	}
	encoded, _ := json.Marshal(result.Values)
	// encoding/json escapes <, >, and &, so the JSON is safe in a script.
	result.ValuesJSON = template.JS(encoded)
	result.Diagnostics = trusted(s.renderDiagnostics(key))
	return result
}
