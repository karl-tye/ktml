// Package hmr is the development-only render registry and hot-swap endpoint
// of a KTML application (ADR-0004 decisions 5 and 6). Only ktmldev builds link
// it, from generated *_ktml_dev.go files and one application wiring file:
//
//	//go:build ktmldev
//
//	func devHandler(handler http.Handler) http.Handler { return hmr.Install(handler) }
//
// Install tracks the generated components that handlers render, keeps their
// props server-side, and serves the token-guarded /__ktml/dev/ routes that
// `ktml dev` calls to swap the interpreter program and collect rerendered HTML.
package hmr

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/karl-tye/ktml"
	"github.com/karl-tye/ktml/dev"
	"github.com/karl-tye/ktml/dev/interp"
	"github.com/karl-tye/ktml/internal/compiler"
)

const (
	// TokenEnv and RootEnv are set by `ktml dev` for the application process:
	// the per-run control-channel token and the KTML workspace root.
	TokenEnv = "KTML_DEV_TOKEN"
	RootEnv  = "KTML_DEV_ROOT"
	// TokenHeader carries the control-channel token on /__ktml/dev/ requests.
	TokenHeader = "X-KTML-Dev-Token"
	// SessionHeader names the page session on HTMX requests.
	SessionHeader = "X-KTML-Session"
	// RequestHeader returns the request token of a page response, which the
	// injected client adopts as its session.
	RequestHeader = "X-KTML-Request"
	// Prefix is the path prefix of the control-channel routes.
	Prefix = "/__ktml/dev/"
)

// Options configure an HMR.
type Options struct {
	// Root is the KTML workspace root. Token guards the control channel.
	Root  string
	Token string
	// Limits bound the registry; zero values take the defaults.
	Limits Limits
	// Interp configures the interpreter; Development is always set.
	Interp interp.Options
	// Now is the registry clock; nil means time.Now.
	Now func() time.Time
	// RenderTimeout bounds each rerender after a swap.
	RenderTimeout time.Duration
	// Logf reports problems; nil means log.Printf.
	Logf func(format string, args ...any)
	// Capture configures story capture (`ktml dev --capture-stories`).
	Capture CaptureOptions
}

// HMR is the registry, the current interpreter program, and the control
// channel of one application process.
type HMR struct {
	options  Options
	registry *Registry

	// mu serializes swaps; renders read program without it.
	mu         sync.Mutex
	workspace  *compiler.Workspace
	version    int
	program    atomic.Pointer[interp.Program]
	delegating atomic.Bool
	generation atomic.Uint64

	capture *capture
}

// New indexes the workspace and compiles the interpreter program. A workspace
// that does not compile yet is reported and loaded again by the first swap;
// until then generated code renders as compiled.
func New(options Options) (*HMR, error) {
	if options.Token == "" {
		return nil, errors.New("hmr: a control-channel token is required")
	}
	root, err := filepath.Abs(options.Root)
	if err != nil {
		return nil, err
	}
	options.Root = root
	defaults := DefaultLimits()
	if options.Limits.SessionRoots <= 0 {
		options.Limits.SessionRoots = defaults.SessionRoots
	}
	if options.Limits.Roots <= 0 {
		options.Limits.Roots = defaults.Roots
	}
	if options.Limits.Bytes <= 0 {
		options.Limits.Bytes = defaults.Bytes
	}
	if options.Limits.SessionIdle <= 0 {
		options.Limits.SessionIdle = defaults.SessionIdle
	}
	if options.Limits.Unadopted <= 0 {
		options.Limits.Unadopted = defaults.Unadopted
	}
	if options.Limits.Disconnected <= 0 {
		options.Limits.Disconnected = defaults.Disconnected
	}
	if options.RenderTimeout <= 0 {
		options.RenderTimeout = 5 * time.Second
	}
	if options.Logf == nil {
		options.Logf = log.Printf
	}
	options.Interp.Development = true
	h := &HMR{options: options, registry: NewRegistry(options.Limits, options.Now), workspace: compiler.NewWorkspace(root)}
	h.capture = newCapture(root, options.Capture, options.Logf)
	snapshot, err := h.workspace.Index()
	if err != nil {
		return nil, fmt.Errorf("hmr: index %s: %w", root, err)
	}
	if program, err := interp.New(snapshot, options.Interp); err != nil {
		h.options.Logf("ktml hmr: the interpreter is not ready; generated code renders until the next swap: %v", err)
	} else {
		h.program.Store(program)
	}
	return h, nil
}

// Install wraps an application handler with the development registry when the
// process was started by `ktml dev` (KTML_DEV_TOKEN is set), and installs the
// delegation hook of the ktmldev generated renderers. Otherwise it returns
// handler unchanged. LocalData declares the application's data local, which
// story capture requires.
func Install(handler http.Handler, options ...InstallOption) http.Handler {
	token := os.Getenv(TokenEnv)
	if token == "" {
		return handler
	}
	root := os.Getenv(RootEnv)
	if root == "" {
		root = "."
	}
	var config installConfig
	for _, option := range options {
		option(&config)
	}
	h, err := New(Options{Root: root, Token: token, Capture: captureOptionsFromEnv(config)})
	if err != nil {
		log.Printf("ktml hmr: not installed: %v", err)
		return handler
	}
	dev.SetRenderer(h)
	return h.Handler(handler)
}

// CaptureStatus reports story capture: off, capturing, or refused with the
// reason, and the stories written.
func (h *HMR) CaptureStatus() CaptureStatus { return h.capture.current() }

// Program returns the current interpreter program, or nil before the
// workspace first compiles.
func (h *HMR) Program() *interp.Program { return h.program.Load() }

// Registry returns the render registry.
func (h *HMR) Registry() *Registry { return h.registry }

type sessionKey struct{}
type recorderKey struct{}

// recorder collects the components a root render reached through the
// delegation hook: Go-built content and cross-package calls.
type recorder struct {
	mu  sync.Mutex
	ids map[string]bool
	// outlets names the component of each instance rendered at a content
	// outlet: a component value built in Go, such as a page passed to a
	// layout, whose Render Identity ends with the outlet's frame.
	outlets map[string]string
}

// addOutlet records the component rendered at an outlet's identity.
func (r *recorder) addOutlet(identity, id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.outlets == nil {
		r.outlets = make(map[string]string)
	}
	if len(r.outlets) < maxOutlets {
		r.outlets[identity] = id
	}
}

// maxOutlets bounds the outlet instances recorded for one root.
const maxOutlets = 1024

// outletFrame matches a Render Identity whose last frame is a content
// outlet, <component>#o<n>. A quoted repetition key ends with a quote, so it
// cannot match.
var outletFrame = regexp.MustCompile(`#o[0-9]+$`)

// outletComponents returns a copy of the recorded outlet components.
func (r *recorder) outletComponents() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.outlets) == 0 {
		return nil
	}
	outlets := make(map[string]string, len(r.outlets))
	for identity, id := range r.outlets {
		outlets[identity] = id
	}
	return outlets
}

func (r *recorder) add(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ids == nil {
		r.ids = make(map[string]bool)
	}
	r.ids[id] = true
}

// Handler serves the control channel and gives every other request a page
// session: the X-KTML-Session it names, or a fresh request token returned in
// X-KTML-Request.
func (h *HMR) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, Prefix) {
			h.serveControl(w, r)
			return
		}
		session := r.Header.Get(SessionHeader)
		if validSession(session) {
			h.registry.Join(session)
		} else {
			session = newToken()
			w.Header().Set(RequestHeader, session)
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, session)))
	})
}

// RenderComponent implements dev.Renderer. A component rendered with no
// enclosing KTML instance in a page session is a root: it is rendered as
// instance @<id> and tracked with its props. After the first swap every
// generated component of the workspace renders through the current program.
func (h *HMR) RenderComponent(ctx context.Context, w io.Writer, component ktml.Component, generated func(context.Context, io.Writer) error) error {
	if dev.Scope(ctx) != "" {
		if rec, ok := ctx.Value(recorderKey{}).(*recorder); ok {
			if id := componentID(component); id != "" {
				rec.add(id)
				if scope := dev.Scope(ctx); outletFrame.MatchString(scope) {
					rec.addOutlet(scope, id)
				}
			}
		}
		return h.render(ctx, w, component, generated)
	}
	session, _ := ctx.Value(sessionKey{}).(string)
	if session == "" {
		return h.render(ctx, w, component, generated)
	}
	id := h.registry.NewInstanceID()
	rec := &recorder{}
	rootCtx := context.WithValue(dev.WithScope(ctx, "@"+id), recorderKey{}, rec)
	if err := h.render(rootCtx, w, component, generated); err != nil {
		return err
	}
	h.track(session, id, component, rec)
	return nil
}

func (h *HMR) render(ctx context.Context, w io.Writer, component ktml.Component, generated func(context.Context, io.Writer) error) error {
	if _, story := component.(interp.StoryProps); story {
		// A story has no generated code: it always renders through the
		// current program.
		program := h.program.Load()
		if program == nil {
			return errors.New("hmr: the interpreter is not ready")
		}
		return program.Render(ctx, w, component)
	}
	if h.delegating.Load() {
		if program := h.program.Load(); program != nil {
			if _, ok := program.ComponentID(component); ok {
				return program.Render(ctx, w, component)
			}
		}
	}
	return generated(ctx, w)
}

func (h *HMR) track(session, id string, component ktml.Component, rec *recorder) {
	h.capture.add(h.program.Load(), component)
	componentID := componentID(component)
	h.registry.Track(RenderInstance{
		SessionID:   session,
		InstanceID:  id,
		ComponentID: componentID,
		Props:       component,
		Generation:  h.generation.Load(),
		Depends:     h.dependencies(componentID, rec),
		Outlets:     rec.outletComponents(),
		Size:        estimateSize(component),
	})
}

// dependencies is the root's transitive component closure in the current
// program, plus that of every component reached through the hook.
func (h *HMR) dependencies(root string, rec *recorder) []string {
	program := h.program.Load()
	seen := map[string]bool{root: true}
	add := func(id string) {
		if program == nil {
			seen[id] = true
			return
		}
		for _, dependency := range program.Dependencies(id) {
			seen[dependency] = true
		}
	}
	add(root)
	rec.mu.Lock()
	for id := range rec.ids {
		add(id)
	}
	rec.mu.Unlock()
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// SwapFile is one changed KTML source, with the bytes `ktml dev` validated.
type SwapFile struct {
	Path   string `json:"path"`
	Source string `json:"source"`
}

// SwapRequest is the body of POST /__ktml/dev/swap.
type SwapRequest struct {
	Generation uint64     `json:"generation"`
	Files      []SwapFile `json:"files"`
}

// Patch is the rerendered HTML of one live root, for its page session.
type Patch struct {
	Session  string `json:"session"`
	Identity string `json:"identity"`
	HTML     string `json:"html"`
	// Document marks a root that renders the whole document (it starts with
	// a doctype); the client morphs document.documentElement.
	Document bool `json:"document,omitempty"`
}

// SwapResponse is the result of a swap. Sessions in Reload had a root that
// failed to rerender; the swap is still applied.
type SwapResponse struct {
	Generation uint64   `json:"generation"`
	Patches    []Patch  `json:"patches"`
	Reload     []string `json:"reload"`
	Diagnostic string   `json:"diagnostic,omitempty"`
}

// SessionRequest is the body of POST /__ktml/dev/session: an SSE stream
// connected or disconnected for a page session.
type SessionRequest struct {
	Session   string `json:"session"`
	Connected bool   `json:"connected"`
}

// ComponentsRequest is the body of POST /__ktml/dev/components: root
// instance IDs, the N of Render Identities "@N", and Render Identities of
// instances at content outlets, that the component picker found on a page.
type ComponentsRequest struct {
	Instances  []string `json:"instances"`
	Identities []string `json:"identities,omitempty"`
}

// ComponentsResponse names the component of each live root instance, and of
// each instance at an outlet, asked for. It never carries props.
type ComponentsResponse struct {
	Components map[string]string `json:"components"`
	Outlets    map[string]string `json:"outlets,omitempty"`
}

// Components names the components of live root instances, and of the
// component values that those roots rendered at content outlets.
func (h *HMR) Components(instances, identities []string) ComponentsResponse {
	response := ComponentsResponse{Components: make(map[string]string)}
	for _, id := range instances {
		if instance, ok := h.registry.Instance(id); ok && instance.ComponentID != "" {
			response.Components[id] = instance.ComponentID
		}
	}
	for _, identity := range identities {
		root, _, _ := strings.Cut(strings.TrimPrefix(identity, "@"), " ")
		instance, ok := h.registry.Instance(root)
		if !ok {
			continue
		}
		if id := instance.Outlets[identity]; id != "" {
			if response.Outlets == nil {
				response.Outlets = make(map[string]string)
			}
			response.Outlets[identity] = id
		}
	}
	return response
}

func (h *HMR) serveControl(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get(TokenHeader)), []byte(h.options.Token)) != 1 {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	switch strings.TrimPrefix(r.URL.Path, Prefix) {
	case "swap":
		var request SwapRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<20)).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		response, err := h.Swap(r.Context(), request)
		status := http.StatusOK
		if err != nil {
			status = http.StatusUnprocessableEntity
			response = SwapResponse{Generation: request.Generation, Diagnostic: err.Error()}
		}
		writeJSON(w, status, response)
	case "capture":
		writeJSON(w, http.StatusOK, h.CaptureStatus())
	case "components":
		var request ComponentsRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&request); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, h.Components(request.Instances, request.Identities))
	case "session":
		var request SessionRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&request); err != nil || !validSession(request.Session) {
			http.Error(w, "invalid session", http.StatusBadRequest)
			return
		}
		if request.Connected {
			h.registry.Connect(request.Session)
		} else {
			h.registry.Disconnect(request.Session)
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// Swap applies changed sources to the workspace, swaps the interpreter
// program, and rerenders every live root that renders a changed component. A
// diagnostic leaves the last valid program active and returns an error.
func (h *HMR) Swap(ctx context.Context, request SwapRequest) (SwapResponse, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(request.Files) == 0 {
		return SwapResponse{}, errors.New("hmr: swap has no files")
	}
	var snapshot *compiler.Snapshot
	paths := make([]string, 0, len(request.Files))
	for _, file := range request.Files {
		path := file.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(h.options.Root, path)
		}
		h.version++
		var err error
		if snapshot, err = h.workspace.Update(path, h.version, []byte(file.Source)); err != nil {
			return SwapResponse{}, err
		}
		paths = append(paths, path)
	}
	if !snapshot.Valid() {
		return SwapResponse{}, snapshot.Err()
	}
	previous := h.program.Load()
	var program *interp.Program
	var err error
	if previous != nil {
		program, err = previous.Swap(snapshot, paths)
	} else {
		program, err = interp.New(snapshot, h.options.Interp)
	}
	if err != nil {
		return SwapResponse{}, err
	}
	changed := make(map[string]bool)
	for _, path := range paths {
		for _, id := range program.FileComponents(path) {
			changed[id] = true
		}
		if previous != nil {
			for _, id := range previous.FileComponents(path) {
				changed[id] = true
			}
		}
	}
	h.program.Store(program)
	h.delegating.Store(true)
	if request.Generation > h.generation.Load() {
		h.generation.Store(request.Generation)
	}
	response := SwapResponse{Generation: h.generation.Load(), Patches: []Patch{}, Reload: []string{}}
	reload := make(map[string]bool)
	for _, instance := range h.registry.Affected(changed) {
		html, depends, err := h.rerender(ctx, program, instance)
		if err != nil {
			h.options.Logf("ktml hmr: rerendering %s (%s) failed; its page reloads: %v", instance.Identity(), instance.ComponentID, err)
			h.registry.Drop(instance.InstanceID)
			reload[instance.SessionID] = true
			continue
		}
		instance.Depends, instance.Generation, instance.RenderedAt = depends, response.Generation, time.Time{}
		h.registry.Track(instance)
		response.Patches = append(response.Patches, Patch{
			Session:  instance.SessionID,
			Identity: instance.Identity(),
			HTML:     html,
			Document: isDocument(html),
		})
	}
	for session := range reload {
		response.Reload = append(response.Reload, session)
	}
	sort.Strings(response.Reload)
	// A page with a failed root reloads whole, so its other patches are moot.
	kept := response.Patches[:0]
	for _, patch := range response.Patches {
		if !reload[patch.Session] {
			kept = append(kept, patch)
		}
	}
	response.Patches = kept
	return response, nil
}

func (h *HMR) rerender(ctx context.Context, program *interp.Program, instance RenderInstance) (string, []string, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), h.options.RenderTimeout)
	defer cancel()
	rec := &recorder{}
	ctx = context.WithValue(dev.WithScope(ctx, instance.Identity()), recorderKey{}, rec)
	var output bytes.Buffer
	if err := program.Render(ctx, &output, instance.Props); err != nil {
		return "", nil, err
	}
	return output.String(), h.dependencies(instance.ComponentID, rec), nil
}

func isDocument(html string) bool {
	trimmed := strings.TrimLeft(html, " \t\r\n")
	return len(trimmed) >= len("<!doctype") && strings.EqualFold(trimmed[:len("<!doctype")], "<!doctype")
}

// componentID is the canonical ID of a generated props value, or the key a
// story props value reports.
func componentID(component ktml.Component) string {
	if keyed, ok := component.(interface{ KTMLComponentID() string }); ok {
		return keyed.KTMLComponentID()
	}
	value := reflect.ValueOf(component)
	for value.IsValid() && value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return ""
		}
		value = value.Elem()
	}
	if !value.IsValid() || value.Type().Name() == "" {
		return ""
	}
	return value.Type().PkgPath() + "." + value.Type().Name()
}

func newToken() string {
	var raw [18]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:])
}

// validSession accepts the opaque tokens this package issues.
func validSession(session string) bool {
	if session == "" || len(session) > 64 {
		return false
	}
	for _, r := range session {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
