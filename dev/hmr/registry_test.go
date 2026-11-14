package hmr

import (
	"strings"
	"testing"
	"time"
)

type clock struct{ now time.Time }

func (c *clock) Now() time.Time                 { return c.now }
func (c *clock) Advance(duration time.Duration) { c.now = c.now.Add(duration) }

func testRegistry(limits Limits) (*Registry, *clock) {
	c := &clock{now: time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)}
	return NewRegistry(limits, c.Now), c
}

func root(registry *Registry, session string, size int, depends ...string) RenderInstance {
	instance := RenderInstance{SessionID: session, InstanceID: registry.NewInstanceID(), ComponentID: "app.Page", Depends: depends, Size: size}
	registry.Track(instance)
	return instance
}

func live(registry *Registry, instances ...RenderInstance) string {
	var states []string
	for _, instance := range instances {
		_, ok := registry.Instance(instance.InstanceID)
		state := "-"
		if ok {
			state = "+"
		}
		states = append(states, instance.InstanceID+state)
	}
	return strings.Join(states, " ")
}

func TestRegistry_GIVENRequestTokenNeverAdopted_WHENItsTTLPasses_THENItsRootsExpire(t *testing.T) {
	registry, c := testRegistry(DefaultLimits())
	page := root(registry, "token", 10)
	c.Advance(29 * time.Second)
	if got := live(registry, page); got != "1+" {
		t.Fatalf("before the adoption deadline: %s", got)
	}
	c.Advance(time.Second)
	if got := live(registry, page); got != "1-" {
		t.Errorf("after the adoption deadline: %s", got)
	}
	if sessions, roots, bytes := registry.Stats(); sessions != 0 || roots != 0 || bytes != 0 {
		t.Errorf("Stats() = %d sessions, %d roots, %d bytes", sessions, roots, bytes)
	}
}

func TestRegistry_GIVENAdoptedSession_WHENDisconnectedAndIdle_THENExpiresAfterGraceOrIdleTime(t *testing.T) {
	registry, c := testRegistry(DefaultLimits())
	page := root(registry, "token", 10)
	registry.Connect("token")
	c.Advance(29 * time.Minute)
	if got := live(registry, page); got != "1+" {
		t.Fatalf("connected session within idle time: %s", got)
	}
	registry.Disconnect("token")
	c.Advance(9 * time.Second)
	registry.Connect("token") // a reload reconnects within the grace period
	c.Advance(time.Minute)
	if got := live(registry, page); got != "1+" {
		t.Fatalf("reconnected session: %s", got)
	}
	registry.Disconnect("token")
	c.Advance(10 * time.Second)
	if got := live(registry, page); got != "1-" {
		t.Errorf("after the disconnect grace: %s", got)
	}

	idle := root(registry, "idle", 10)
	registry.Join("idle")
	c.Advance(29 * time.Minute)
	fresh := root(registry, "idle", 10) // a render is activity
	c.Advance(29 * time.Minute)
	if got := live(registry, idle, fresh); got != "2+ 3+" {
		t.Fatalf("active session: %s", got)
	}
	c.Advance(time.Minute)
	if got := live(registry, idle, fresh); got != "2- 3-" {
		t.Errorf("idle session: %s", got)
	}
}

func TestRegistry_GIVENBounds_WHENExceeded_THENEvictsLeastRecentlyRenderedRoots(t *testing.T) {
	limits := DefaultLimits()
	limits.SessionRoots, limits.Roots, limits.Bytes = 2, 3, 100
	registry, c := testRegistry(limits)
	a1 := root(registry, "a", 10)
	c.Advance(time.Second)
	a2 := root(registry, "a", 10)
	c.Advance(time.Second)
	registry.Track(RenderInstance{SessionID: "a", InstanceID: a1.InstanceID, Size: 10}) // rerendered: now the newest
	c.Advance(time.Second)
	a3 := root(registry, "a", 10)
	if got := live(registry, a1, a2, a3); got != "1+ 2- 3+" {
		t.Fatalf("session bound: %s", got)
	}
	b1 := root(registry, "b", 10)
	b2 := root(registry, "b", 10)
	if got := live(registry, a1, a3, b1, b2); got != "1- 3+ 4+ 5+" {
		t.Fatalf("root bound: %s", got)
	}
	big := root(registry, "c", 85)
	if got := live(registry, a3, b1, b2, big); got != "3- 4- 5+ 6+" {
		t.Fatalf("byte bound: %s", got)
	}
	if _, roots, bytes := registry.Stats(); roots != 2 || bytes != 95 {
		t.Errorf("Stats() = %d roots, %d bytes", roots, bytes)
	}
	huge := root(registry, "c", 500)
	if got := live(registry, b2, big, huge); got != "5- 6- 7-" {
		t.Errorf("a root over the byte bound alone: %s", got)
	}
}

func TestRegistry_GIVENDependencies_WHENAComponentChanges_THENReturnsEveryRootThatRendersIt(t *testing.T) {
	registry, _ := testRegistry(DefaultLimits())
	page := root(registry, "a", 1, "app.Badge", "app.Page")
	other := root(registry, "b", 1, "app.Badge", "app.List")
	root(registry, "b", 1, "app.List")
	var ids []string
	for _, instance := range registry.Affected(map[string]bool{"app.Badge": true}) {
		ids = append(ids, instance.SessionID+"/"+instance.Identity())
	}
	if got, want := strings.Join(ids, " "), "a/@"+page.InstanceID+" b/@"+other.InstanceID; got != want {
		t.Errorf("Affected() = %s, want %s", got, want)
	}
	registry.Drop(page.InstanceID)
	if got := len(registry.Affected(map[string]bool{"app.Page": true})); got != 0 {
		t.Errorf("dropped root still affected: %d", got)
	}
}
