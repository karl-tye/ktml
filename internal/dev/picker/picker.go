// Package picker serves the component picker of pages that `ktml dev`
// proxies: a hotkey in the injected client selects a component instance on
// the page and opens its story in the Component Explorer. It resolves the
// Render Identities of the page's instances (ADR-0004) to components, and
// asks the Explorer for a story, which the Explorer writes from the
// component's input types when there is none. Props never leave the
// application: the picker sends identities and component names only.
//
// Only the ktml command links this package; application binaries never do.
package picker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/config"
	"github.com/karl-tye/ktml/internal/explorer"
)

// Prefix is the path prefix of the picker's routes on the dev proxy.
const Prefix = "/__ktml/picker/"

// Explorer is the Component Explorer that ktml dev serves.
type Explorer interface {
	Describe(componentID string) (explorer.ComponentInfo, bool)
	CallTarget(callerID, site string) (string, bool)
	EnsureStory(ctx context.Context, componentID string) (key string, created bool, err error)
	Locate(componentID string, links []explorer.Link, path []compiler.ElementStep) []explorer.Location
	Declaration(componentID string) (explorer.Location, bool)
}

// Editor opens a located source in the developer's editor.
type Editor func(location explorer.Location) error

// Roots names the components of the application's root instances, by the
// N of "@N", and of the component values built in Go that they rendered at
// content outlets, by Render Identity (control.Client.Components).
type Roots func(ctx context.Context, instances, outlets []string) (roots, atOutlets map[string]string, err error)

// Handler serves the picker's routes.
type Handler struct {
	explorer    Explorer
	roots       Roots
	explorerURL string
	editor      Editor
}

// New returns the picker's handler. explorerURL is the Explorer's base URL,
// such as http://127.0.0.1:7332.
func New(explorer Explorer, roots Roots, explorerURL string) *Handler {
	return &Handler{explorer: explorer, roots: roots, explorerURL: strings.TrimSuffix(explorerURL, "/")}
}

// SetEditor lets POST /__ktml/picker/edit open sources in editor. Call it
// before the handler serves.
func (h *Handler) SetEditor(editor Editor) {
	h.editor = editor
}

// resolveRequest is the body of POST /__ktml/picker/resolve: the Render
// Identities of the instances on a page.
type resolveRequest struct {
	Identities []string `json:"identities"`
}

// resolveResponse gives each resolved identity its chain of component
// instances, outermost first: the root, then every instance a call frame
// renders, ending with the identity's own instance.
type resolveResponse struct {
	Chains     map[string][]chainEntry  `json:"chains"`
	Components map[string]componentInfo `json:"components"`
}

type chainEntry struct {
	Identity  string `json:"identity"`
	Component string `json:"component"`
}

type componentInfo struct {
	Name     string `json:"name"`
	Document bool   `json:"document,omitempty"`
	Story    bool   `json:"story,omitempty"`
	// Source is where the component is declared.
	Source *explorer.Location `json:"source,omitempty"`
}

// locateRequest is the body of POST /__ktml/picker/locate: an instance's
// Render Identity and the Element Path from the instance's top element down
// to a picked element.
type locateRequest struct {
	Identity string                 `json:"identity"`
	Path     []compiler.ElementStep `json:"path"`
}

// locateResponse is the source of every element of the path, or null.
type locateResponse struct {
	Locations []*explorer.Location `json:"locations"`
}

// editRequest is the body of POST /__ktml/picker/edit: the picked element,
// as a locate request, or a component. It never names a file: ktml dev
// locates the source itself.
type editRequest struct {
	locateRequest
	Component string `json:"component,omitempty"`
}

// editResponse is the source the editor opened.
type editResponse struct {
	Location explorer.Location `json:"location"`
}

// openRequest is the body of POST /__ktml/picker/open.
type openRequest struct {
	Component string `json:"component"`
}

// openResponse is the Explorer URL of the component's story.
type openResponse struct {
	URL     string `json:"url"`
	Created bool   `json:"created,omitempty"`
}

// maxIdentities bounds one resolve request, and maxPath an Element Path.
const (
	maxIdentities = 20000
	maxPath       = 256
)

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route := strings.TrimPrefix(r.URL.Path, Prefix)
	if route != "resolve" && route != "open" && route != "locate" && route != "edit" {
		http.NotFound(w, r)
		return
	}
	if status, message := checkRequest(r); status != 0 {
		http.Error(w, message, status)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		http.Error(w, "unreadable request", http.StatusBadRequest)
		return
	}
	switch route {
	case "resolve":
		var request resolveRequest
		if err := json.Unmarshal(body, &request); err != nil || len(request.Identities) > maxIdentities {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		writeJSON(w, h.resolve(r.Context(), request.Identities))
	case "open":
		var request openRequest
		if err := json.Unmarshal(body, &request); err != nil || request.Component == "" {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		_, created, err := h.explorer.EnsureStory(ctx, request.Component)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		writeJSON(w, openResponse{URL: h.explorerURL + explorer.Prefix + "open?" + url.Values{"component": {request.Component}}.Encode(), Created: created})
	case "locate":
		var request locateRequest
		if err := json.Unmarshal(body, &request); err != nil || len(request.Path) == 0 || len(request.Path) > maxPath {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		response := locateResponse{Locations: make([]*explorer.Location, len(request.Path))}
		for index, location := range h.locate(r.Context(), request) {
			if location.File != "" {
				response.Locations[index] = &location
			}
		}
		writeJSON(w, response)
	case "edit":
		var request editRequest
		if err := json.Unmarshal(body, &request); err != nil || len(request.Path) > maxPath {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if h.editor == nil {
			http.Error(w, "no editor is configured: set dev.picker.editor", http.StatusNotImplemented)
			return
		}
		var location explorer.Location
		switch {
		case request.Component != "":
			location, _ = h.explorer.Declaration(request.Component)
		case request.Identity != "" && len(request.Path) > 0:
			located := h.locate(r.Context(), request.locateRequest)
			location = located[len(located)-1]
		}
		if location.File == "" {
			http.Error(w, "the selection has no known KTML source", http.StatusUnprocessableEntity)
			return
		}
		if err := h.editor(location); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, editResponse{Location: location})
	}
}

// locate finds the source of every element of a path inside the instance
// the identity names. The calls that supplied the instance's content, and
// outward each caller's, come from the identity's frames.
func (h *Handler) locate(ctx context.Context, request locateRequest) []explorer.Location {
	resolved := h.resolveAll(ctx, []string{request.Identity})
	chain := resolved.response.Chains[request.Identity]
	_, frames, err := parseIdentity(request.Identity)
	if len(chain) == 0 || err != nil {
		return make([]explorer.Location, len(request.Path))
	}
	return h.explorer.Locate(chain[len(chain)-1].Component, contentLinks(frames, resolved.outletAt(request.Identity, frames)), request.Path)
}

// outletInstance reports whether the first n frames of an identity end at
// an instance rendered at a content outlet: a component value built in Go.
type outletInstance func(n int) bool

// contentLinks returns the call that rendered the instance with these
// Render Identity frames, then the call that rendered that caller's
// instance, and so on outward, while each is a KTML call.
func contentLinks(frames []frame, outlet outletInstance) []explorer.Link {
	var links []explorer.Link
	for len(frames) > 0 {
		last := frames[len(frames)-1]
		if !strings.HasPrefix(last.source, "c") {
			break
		}
		links = append(links, explorer.Link{Caller: last.component, Site: last.source})
		owner, ok := ownerFrames(frames[:len(frames)-1], outlet)
		if !ok {
			break
		}
		frames = owner
	}
	return links
}

// ownerFrames trims identity frames to those of the instance whose source
// renders after them: a call frame starts the called instance, and so does
// an outlet frame where a component value built in Go renders; a repetition
// item stays in its component's source; other content at an outlet is the
// source of the call that rendered the outlet's instance. It reports false
// when that is content built in Go.
func ownerFrames(frames []frame, outlet outletInstance) ([]frame, bool) {
	for len(frames) > 0 {
		if outlet != nil && outlet(len(frames)) {
			return frames, true
		}
		last := frames[len(frames)-1]
		switch last.source[0] {
		case 'c':
			return frames, true
		case 'r':
			frames = frames[:len(frames)-1]
		case 'o':
			instance, ok := ownerFrames(frames[:len(frames)-1], outlet)
			if !ok || len(instance) == 0 || !strings.HasPrefix(instance[len(instance)-1].source, "c") {
				return nil, false
			}
			frames = instance[:len(instance)-1]
		default:
			return nil, false
		}
	}
	return frames, true
}

// checkRequest admits only a same-origin JSON POST to a loopback Host. A
// page on another site can send a simple POST, but not with this Origin or
// Content-Type without a CORS preflight, which the picker never grants; a
// DNS rebinding page names a foreign Host. It returns 0 when the request is
// admitted.
func checkRequest(r *http.Request) (int, string) {
	if r.Method != http.MethodPost {
		return http.StatusMethodNotAllowed, "the picker only takes POST"
	}
	host := r.Host
	if split, _, err := net.SplitHostPort(r.Host); err == nil {
		host = split
	}
	if !config.LoopbackHost(host) {
		return http.StatusForbidden, "the picker only answers loopback hosts"
	}
	if origin := r.Header.Get("Origin"); origin != "http://"+r.Host {
		return http.StatusForbidden, "the picker only answers its own pages"
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return http.StatusForbidden, "the picker only answers its own pages"
	}
	if mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mediaType != "application/json" {
		return http.StatusUnsupportedMediaType, "the picker takes application/json"
	}
	return 0, ""
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(value)
}

// frame is one frame of a Render Identity: the component rendering, and its
// call (c<n>), outlet (o<n>), or repetition (r<n>=key) source. raw is the
// frame's text.
type frame struct {
	component, source, raw string
}

// parseIdentity splits a Render Identity "@N frame frame ..." into its root
// instance N and frames. A frame is <component>#<source>, with =<key> for a
// repetition item, where a string key is Go-quoted and may hold spaces.
func parseIdentity(identity string) (string, []frame, error) {
	invalid := errors.New("invalid Render Identity")
	root, rest, _ := strings.Cut(identity, " ")
	number := strings.TrimPrefix(root, "@")
	if number == root || number == "" || strings.Trim(number, "0123456789") != "" {
		return "", nil, invalid
	}
	var frames []frame
	for rest != "" {
		hash := strings.IndexByte(rest, '#')
		if hash <= 0 || strings.ContainsAny(rest[:hash], " \"") {
			return "", nil, invalid
		}
		end := hash + 1
		for end < len(rest) && rest[end] != ' ' && rest[end] != '=' {
			end++
		}
		source := rest[hash+1 : end]
		if source == "" {
			return "", nil, invalid
		}
		if end < len(rest) && rest[end] == '=' {
			end++
			if end < len(rest) && rest[end] == '"' {
				quoted, err := strconv.QuotedPrefix(rest[end:])
				if err != nil {
					return "", nil, invalid
				}
				end += len(quoted)
			} else {
				for end < len(rest) && rest[end] != ' ' {
					end++
				}
			}
		}
		frames = append(frames, frame{component: rest[:hash], source: source, raw: rest[:end]})
		rest = rest[end:]
		if rest != "" {
			if rest[0] != ' ' || len(rest) == 1 {
				return "", nil, invalid
			}
			rest = rest[1:]
		}
	}
	return number, frames, nil
}

// resolved is a resolve response, with the components of the instances at
// outlets that it found, by Render Identity.
type resolved struct {
	response resolveResponse
	outlets  map[string]string
}

// outletAt reports which prefixes of an identity's frames end at an
// instance at an outlet.
func (r resolved) outletAt(identity string, frames []frame) outletInstance {
	root, _, _ := strings.Cut(identity, " ")
	return func(n int) bool {
		_, ok := r.outlets[prefixIdentity(root, frames[:n])]
		return ok
	}
}

// prefixIdentity is the Render Identity of root followed by frames.
func prefixIdentity(root string, frames []frame) string {
	identity := root
	for _, current := range frames {
		identity += " " + current.raw
	}
	return identity
}

// resolve builds the component chain of every identity.
func (h *Handler) resolve(ctx context.Context, identities []string) resolveResponse {
	return h.resolveAll(ctx, identities).response
}

// resolveAll builds the component chain of every identity. A frame's
// component is the one rendering, so a root's component is that of its first
// frame; the application names a root that has none. A call frame's instance
// is the component the call renders. An outlet frame is an instance when the
// content there is a component value built in Go, such as a page passed to a
// layout: the application names it, or else the next frame does, which names
// the component whose source renders there when the outlet's own instance
// was not rendered by a KTML call. Repetition frames are not instances.
func (h *Handler) resolveAll(ctx context.Context, identities []string) resolved {
	response := resolveResponse{Chains: make(map[string][]chainEntry), Components: make(map[string]componentInfo)}
	type parsed struct {
		root   string
		frames []frame
	}
	byIdentity := make(map[string]parsed)
	rootComponent := make(map[string]string)
	var unnamed, outletPrefixes []string
	for _, identity := range identities {
		root, frames, err := parseIdentity(identity)
		if err != nil {
			continue
		}
		byIdentity[identity] = parsed{root, frames}
		if len(frames) > 0 {
			rootComponent[root] = frames[0].component
		}
		for index, current := range frames {
			if strings.HasPrefix(current.source, "o") {
				prefix := prefixIdentity("@"+root, frames[:index+1])
				if !slices.Contains(outletPrefixes, prefix) && len(outletPrefixes) < maxIdentities {
					outletPrefixes = append(outletPrefixes, prefix)
				}
			}
		}
	}
	for _, identity := range identities {
		if entry, ok := byIdentity[identity]; ok && len(entry.frames) == 0 && rootComponent[entry.root] == "" && !slices.Contains(unnamed, entry.root) {
			unnamed = append(unnamed, entry.root)
		}
	}
	outlets := make(map[string]string)
	if (len(unnamed) > 0 || len(outletPrefixes) > 0) && h.roots != nil {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		named, atOutlets, err := h.roots(ctx, unnamed, outletPrefixes)
		cancel()
		if err == nil {
			for root, component := range named {
				if slices.Contains(unnamed, root) {
					rootComponent[root] = component
				}
			}
			for prefix, component := range atOutlets {
				if slices.Contains(outletPrefixes, prefix) {
					outlets[prefix] = component
				}
			}
		}
	}
	// Infer the rest, shorter prefixes first, since an outlet instance's
	// own outlets hold Go content too.
	for _, entry := range byIdentity {
		outlet := func(n int) bool {
			_, ok := outlets[prefixIdentity("@"+entry.root, entry.frames[:n])]
			return ok
		}
		for index, current := range entry.frames {
			if !strings.HasPrefix(current.source, "o") || index+1 >= len(entry.frames) {
				continue
			}
			prefix := prefixIdentity("@"+entry.root, entry.frames[:index+1])
			if _, known := outlets[prefix]; known {
				continue
			}
			owner, ok := ownerFrames(entry.frames[:index], outlet)
			if !ok || (len(owner) > 0 && !outlet(len(owner))) {
				// KTML content of a call, or content of unknown origin.
				continue
			}
			outlets[prefix] = entry.frames[index+1].component
		}
	}
	describe := func(component string) {
		if _, seen := response.Components[component]; seen {
			return
		}
		info, ok := h.explorer.Describe(component)
		if !ok {
			info.Name = component[strings.LastIndexByte(component, '.')+1:]
		}
		entry := componentInfo{Name: info.Name, Document: info.Document, Story: info.Story}
		if source, ok := h.explorer.Declaration(component); ok {
			entry.Source = &source
		}
		response.Components[component] = entry
	}
	for identity, entry := range byIdentity {
		root := rootComponent[entry.root]
		if root == "" {
			continue
		}
		prefix := "@" + entry.root
		chain := []chainEntry{{Identity: prefix, Component: root}}
		for _, current := range entry.frames {
			prefix += " " + current.raw
			switch {
			case strings.HasPrefix(current.source, "c"):
				if target, ok := h.explorer.CallTarget(current.component, current.source); ok {
					chain = append(chain, chainEntry{Identity: prefix, Component: target})
				}
			case strings.HasPrefix(current.source, "o"):
				if component, ok := outlets[prefix]; ok {
					chain = append(chain, chainEntry{Identity: prefix, Component: component})
				}
			}
		}
		if chain[len(chain)-1].Identity != identity {
			// The instance itself did not resolve, such as a closure
			// rendered at an outlet.
			continue
		}
		for _, link := range chain {
			describe(link.Component)
		}
		response.Chains[identity] = chain
	}
	return resolved{response: response, outlets: outlets}
}
