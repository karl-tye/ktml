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
}

// Roots names the components of the application's root instances, by the
// N of "@N" (control.Client.Components).
type Roots func(ctx context.Context, instances []string) (map[string]string, error)

// Handler serves the picker's routes.
type Handler struct {
	explorer    Explorer
	roots       Roots
	explorerURL string
}

// New returns the picker's handler. explorerURL is the Explorer's base URL,
// such as http://127.0.0.1:7332.
func New(explorer Explorer, roots Roots, explorerURL string) *Handler {
	return &Handler{explorer: explorer, roots: roots, explorerURL: strings.TrimSuffix(explorerURL, "/")}
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

// maxIdentities bounds one resolve request.
const maxIdentities = 20000

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route := strings.TrimPrefix(r.URL.Path, Prefix)
	if route != "resolve" && route != "open" {
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
	}
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

// resolve builds the component chain of every identity. A frame's component
// is the one rendering, so a root's component is that of its first frame;
// the application names a root that has none. A call frame's instance is the
// component the call renders; outlet and repetition frames are not
// instances.
func (h *Handler) resolve(ctx context.Context, identities []string) resolveResponse {
	response := resolveResponse{Chains: make(map[string][]chainEntry), Components: make(map[string]componentInfo)}
	type parsed struct {
		root   string
		frames []frame
	}
	byIdentity := make(map[string]parsed)
	rootComponent := make(map[string]string)
	var unnamed []string
	for _, identity := range identities {
		root, frames, err := parseIdentity(identity)
		if err != nil {
			continue
		}
		byIdentity[identity] = parsed{root, frames}
		if len(frames) > 0 {
			rootComponent[root] = frames[0].component
		}
	}
	for _, identity := range identities {
		if entry, ok := byIdentity[identity]; ok && len(entry.frames) == 0 && rootComponent[entry.root] == "" && !slices.Contains(unnamed, entry.root) {
			unnamed = append(unnamed, entry.root)
		}
	}
	if len(unnamed) > 0 && h.roots != nil {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		named, err := h.roots(ctx, unnamed)
		cancel()
		if err == nil {
			for root, component := range named {
				if slices.Contains(unnamed, root) {
					rootComponent[root] = component
				}
			}
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
		response.Components[component] = componentInfo{Name: info.Name, Document: info.Document, Story: info.Story}
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
			if !strings.HasPrefix(current.source, "c") {
				continue
			}
			if target, ok := h.explorer.CallTarget(current.component, current.source); ok {
				chain = append(chain, chainEntry{Identity: prefix, Component: target})
			}
		}
		if chain[len(chain)-1].Identity != identity {
			// The instance itself did not resolve, such as a component
			// value rendered at an outlet.
			continue
		}
		for _, link := range chain {
			describe(link.Component)
		}
		response.Chains[identity] = chain
	}
	return response
}
