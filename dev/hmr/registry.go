package hmr

import (
	"container/list"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/karl-tye/ktml"
)

// RenderInstance is one tracked root render (ADR-0004 decision 5): a
// generated component rendered by application code with no enclosing KTML
// instance. Nested instances are recomputed by rerendering their root, so
// their props are never stored.
type RenderInstance struct {
	SessionID   string
	InstanceID  string
	ComponentID string         // canonical "<import path>.<Name>"
	Props       ktml.Component // the generated value itself
	Generation  uint64         // interpreter program generation at render
	Depends     []string       // transitive component IDs, sorted
	RenderedAt  time.Time
	Size        int // estimated retained bytes
}

// Identity is the Render Identity of the root instance in the page.
func (instance RenderInstance) Identity() string { return "@" + instance.InstanceID }

// Limits bound the registry. Roots are evicted least recently rendered first.
type Limits struct {
	// SessionRoots bounds the roots of one browser session.
	SessionRoots int
	// Roots and Bytes bound the whole registry.
	Roots int
	Bytes int
	// SessionIdle expires a session with no render, patch, or connection.
	SessionIdle time.Duration
	// Unadopted expires a request token no SSE stream adopted.
	Unadopted time.Duration
	// Disconnected expires a session after its SSE stream disconnects, long
	// enough to survive a page reload.
	Disconnected time.Duration
}

// DefaultLimits are ADR-0004's defaults.
func DefaultLimits() Limits {
	return Limits{
		SessionRoots: 256,
		Roots:        4096,
		Bytes:        64 << 20,
		SessionIdle:  30 * time.Minute,
		Unadopted:    30 * time.Second,
		Disconnected: 10 * time.Second,
	}
}

// session is one browser page. It starts as the unadopted request token of a
// page render; an SSE stream (Connect) or a request naming it (Join) adopts
// it.
type session struct {
	id           string
	adopted      bool
	created      time.Time
	active       time.Time
	disconnected time.Time
	roots        map[string]*list.Element
}

// expires is when the session ends unless it is used again.
func (owner *session) expires(limits Limits) time.Time {
	switch {
	case !owner.adopted:
		return owner.created.Add(limits.Unadopted)
	case !owner.disconnected.IsZero():
		return owner.disconnected.Add(limits.Disconnected)
	default:
		return owner.active.Add(limits.SessionIdle)
	}
}

// Registry is the development-only, server-side render registry. It keeps
// props in the app process and never serializes them.
type Registry struct {
	mu       sync.Mutex
	limits   Limits
	now      func() time.Time
	next     uint64
	sessions map[string]*session
	// order holds every root, least recently rendered first.
	order *list.List
	roots map[string]*list.Element
	bytes int
}

// NewRegistry returns an empty registry. now is the clock; nil means
// time.Now.
func NewRegistry(limits Limits, now func() time.Time) *Registry {
	if now == nil {
		now = time.Now
	}
	return &Registry{limits: limits, now: now, sessions: make(map[string]*session), order: list.New(), roots: make(map[string]*list.Element)}
}

// NewInstanceID returns a fresh root instance ID.
func (r *Registry) NewInstanceID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.next++
	return strconv.FormatUint(r.next, 10)
}

// Track records or replaces a root. A session seen for the first time is a
// request token, which expires unless an SSE stream adopts it.
func (r *Registry) Track(instance RenderInstance) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.sweep(now)
	owner := r.session(instance.SessionID, false, now)
	if element, ok := r.roots[instance.InstanceID]; ok {
		r.remove(element)
	}
	if instance.RenderedAt.IsZero() {
		instance.RenderedAt = now
	}
	stored := instance
	element := r.order.PushBack(&stored)
	r.roots[instance.InstanceID] = element
	owner.roots[instance.InstanceID] = element
	r.bytes += instance.Size
	owner.active = now
	for len(owner.roots) > r.limits.SessionRoots {
		r.remove(r.oldest(owner))
	}
	for r.order.Len() > r.limits.Roots || (r.bytes > r.limits.Bytes && r.order.Len() > 0) {
		r.remove(r.order.Front())
	}
}

// Join marks a session that a live client names on its requests: it is
// adopted, and expires only when idle.
func (r *Registry) Join(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.sweep(now)
	owner := r.session(id, true, now)
	owner.active, owner.disconnected = now, time.Time{}
}

// Connect adopts a session into an SSE stream. Its request token stops
// expiring until the stream disconnects or the session idles.
func (r *Registry) Connect(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.sweep(now)
	owner := r.session(id, true, now)
	owner.active, owner.disconnected = now, time.Time{}
}

// Disconnect starts the grace period after an SSE stream ends.
func (r *Registry) Disconnect(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.sweep(now)
	owner, ok := r.sessions[id]
	if !ok {
		return
	}
	owner.disconnected = now
}

// Affected returns copies of the live roots whose dependencies include any
// changed component, ordered by session and instance.
func (r *Registry) Affected(changed map[string]bool) []RenderInstance {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sweep(r.now())
	var affected []RenderInstance
	for element := r.order.Front(); element != nil; element = element.Next() {
		instance := element.Value.(*RenderInstance)
		for _, dependency := range instance.Depends {
			if changed[dependency] {
				affected = append(affected, *instance)
				break
			}
		}
	}
	sort.Slice(affected, func(i, j int) bool {
		if affected[i].SessionID != affected[j].SessionID {
			return affected[i].SessionID < affected[j].SessionID
		}
		return len(affected[i].InstanceID) < len(affected[j].InstanceID) || len(affected[i].InstanceID) == len(affected[j].InstanceID) && affected[i].InstanceID < affected[j].InstanceID
	})
	return affected
}

// Drop removes a root, for example after its rerender failed.
func (r *Registry) Drop(instanceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if element, ok := r.roots[instanceID]; ok {
		r.remove(element)
	}
}

// Stats reports the live sessions, roots, and estimated retained bytes.
func (r *Registry) Stats() (sessions, roots, bytes int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sweep(r.now())
	return len(r.sessions), r.order.Len(), r.bytes
}

// Instance returns a copy of a live root.
func (r *Registry) Instance(instanceID string) (RenderInstance, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sweep(r.now())
	element, ok := r.roots[instanceID]
	if !ok {
		return RenderInstance{}, false
	}
	return *element.Value.(*RenderInstance), true
}

// session returns a session, creating it when it is new. A session created by
// a render is an unadopted request token.
func (r *Registry) session(id string, adopted bool, now time.Time) *session {
	owner, ok := r.sessions[id]
	if !ok {
		owner = &session{id: id, created: now, active: now, roots: make(map[string]*list.Element)}
		r.sessions[id] = owner
	}
	if adopted {
		owner.adopted = true
	}
	return owner
}

// oldest returns the session's least recently rendered root: roots are kept
// in render order.
func (r *Registry) oldest(owner *session) *list.Element {
	for element := r.order.Front(); element != nil; element = element.Next() {
		if element.Value.(*RenderInstance).SessionID == owner.id {
			return element
		}
	}
	return nil
}

func (r *Registry) remove(element *list.Element) {
	instance := element.Value.(*RenderInstance)
	r.order.Remove(element)
	delete(r.roots, instance.InstanceID)
	r.bytes -= instance.Size
	if owner, ok := r.sessions[instance.SessionID]; ok {
		delete(owner.roots, instance.InstanceID)
	}
}

// sweep removes expired sessions and their roots.
func (r *Registry) sweep(now time.Time) {
	for id, owner := range r.sessions {
		if now.Before(owner.expires(r.limits)) {
			continue
		}
		for _, element := range owner.roots {
			r.remove(element)
		}
		delete(r.sessions, id)
	}
}
