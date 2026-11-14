package explorer

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	cardID   = "ktml.explorer/ui.Card"
	statusID = "ktml.explorer/ui.Status"
)

func TestDescribe_GIVENComponents_WHENLookedUp_THENNamesThemAndTheirStoriesAndCalls(t *testing.T) {
	s, _, _ := newTestServer(t, fixtureFiles, nil)
	card, ok := s.Describe(cardID)
	if !ok || card.Name != "Card" || !card.Story || card.Document {
		t.Errorf("Describe(Card) = %+v, %v", card, ok)
	}
	status, ok := s.Describe(statusID)
	if !ok || status.Name != "Status" || status.Story {
		t.Errorf("Describe(Status) = %+v, %v", status, ok)
	}
	if _, ok := s.Describe("ktml.explorer/ui.Missing"); ok {
		t.Error("Describe() knows a missing component")
	}
	if target, ok := s.CallTarget(cardID, "c0"); !ok || target != statusID {
		t.Errorf("CallTarget(Card, c0) = %q, %v", target, ok)
	}
}

func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// TestOpen_GIVENAComponentWithAStory_WHENOpened_THENRedirectsToItsStory
// opens a component the picker names at its story; a component without one
// is not created by a GET.
func TestOpen_GIVENAComponentWithAStory_WHENOpened_THENRedirectsToItsStory(t *testing.T) {
	_, backend, root := newTestServer(t, fixtureFiles, nil)
	client := &http.Client{CheckRedirect: noRedirect}
	response, err := client.Get(backend.URL + Prefix + "open?component=" + url.QueryEscape(cardID))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	location := response.Header.Get("Location")
	if response.StatusCode != http.StatusSeeOther || !strings.HasPrefix(location, "/?story=") || !strings.Contains(location, "Card.") {
		t.Errorf("open Card = %d %q, want a redirect to a Card story", response.StatusCode, location)
	}
	for _, component := range []string{statusID, "ktml.explorer/ui.Missing", ""} {
		code, body := get(t, backend.URL+Prefix+"open?component="+url.QueryEscape(component))
		if code != http.StatusNotFound {
			t.Errorf("open %q = %d, want 404", component, code)
		}
		if component == statusID && !strings.Contains(body, "no story") {
			t.Errorf("open Status explains nothing: %s", body)
		}
	}
	if source, _ := os.ReadFile(filepath.Join(root, "ui", "card.story.ktml")); strings.Contains(string(source), "Status.") {
		t.Error("a GET wrote a story")
	}
	request, _ := http.NewRequest(http.MethodGet, backend.URL+Prefix+"open?component="+url.QueryEscape(cardID), nil)
	request.Host = "attacker.example"
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Errorf("open with a foreign Host = %d, want 403", response.StatusCode)
	}
}

// TestEnsureStory_GIVENAComponentWithoutAStory_WHENEnsured_THENWritesATypeDerivedStoryAndOpensIt
// creates the story the picker needs from the component's input types only,
// keeps the Document's hand-written stories, and serves it at once.
func TestEnsureStory_GIVENAComponentWithoutAStory_WHENEnsured_THENWritesATypeDerivedStoryAndOpensIt(t *testing.T) {
	s, backend, root := newTestServer(t, fixtureFiles, nil)
	key, created, err := s.EnsureStory(context.Background(), statusID)
	if err != nil || !created || !strings.HasPrefix(key, "ui/card.story.ktml#Status.") {
		t.Fatalf("EnsureStory(Status) = %q, %v, %v", key, created, err)
	}
	source, _ := os.ReadFile(filepath.Join(root, "ui", "card.story.ktml"))
	text := string(source)
	if !strings.Contains(text, "// ktml:generated ") || !strings.Contains(text, "story Status.") || !strings.Contains(text, "story Card.Running [\n\ttitle = \"Board\"") || !strings.Contains(text, "story Card.Idle [") {
		t.Errorf("story Document:\n%s", text)
	}
	if status, ok := s.Describe(statusID); !ok || !status.Story {
		t.Errorf("the new story is not in the program: %+v", status)
	}
	code, body := get(t, backend.URL+Prefix+"preview?story="+url.QueryEscape(key))
	if code != http.StatusOK || !strings.Contains(body, "<em>idle</em>") && !strings.Contains(body, "running") && !strings.Contains(body, "failed") {
		t.Errorf("preview of %s = %d:\n%s", key, code, body)
	}
	again, created, err := s.EnsureStory(context.Background(), statusID)
	if err != nil || created || again == "" {
		t.Errorf("second EnsureStory = %q, %v, %v; want the existing story", again, created, err)
	}
	if after, _ := os.ReadFile(filepath.Join(root, "ui", "card.story.ktml")); string(after) != text {
		t.Error("the second EnsureStory rewrote the Document")
	}
	if _, _, err := s.EnsureStory(context.Background(), "ktml.explorer/ui.Missing"); err == nil {
		t.Error("EnsureStory of a missing component succeeded")
	}
}
