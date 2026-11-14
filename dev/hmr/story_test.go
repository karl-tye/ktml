package hmr_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/dev/hmr"
)

const badgeStories = "import { Badge } from \"./badge.ktml\"\n\nstory Badge.Plain [label = \"plain\"]\n"

func TestSwap_GIVENAStoryRoot_WHENItsStoryOrComponentChanges_THENTheSameSwapPatchesIt(t *testing.T) {
	var f *fixture
	f = newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		program := f.hmr.Program()
		story := program.Stories()[0]
		_ = f.hmr.RenderComponent(r.Context(), w, program.StoryProps(story.ID, nil), nil)
	})
	if err := os.WriteFile(filepath.Join(f.root, "badge.story.ktml"), []byte(badgeStories), 0o600); err != nil {
		t.Fatal(err)
	}
	// Load the story file through the swap path, as the Explorer does when
	// a story document appears.
	if _, err := f.hmr.Swap(context.Background(), hmr.SwapRequest{Generation: 1, Files: []hmr.SwapFile{{Path: filepath.Join(f.root, "badge.story.ktml"), Source: badgeStories}}}); err != nil {
		t.Fatalf("Swap(story) error = %v", err)
	}
	page := f.get(t, "")
	session := page.Header().Get(hmr.RequestHeader)
	if page.Body.String() != `<b data-ktml-i="@1">plain</b>` {
		t.Fatalf("story page = %s", page.Body.String())
	}
	instance, ok := f.hmr.Registry().Instance("1")
	if !ok || instance.SessionID != session || !strings.HasSuffix(instance.ComponentID, "::story::Badge.Plain") {
		t.Fatalf("Instance(1) = %#v, %v", instance, ok)
	}

	code, response := f.swap(t, token, map[string]string{"badge.story.ktml": strings.Replace(badgeStories, `"plain"`, `"edited"`, 1)})
	if code != http.StatusOK || len(response.Patches) != 1 || response.Patches[0].HTML != `<b data-ktml-i="@1">edited</b>` || response.Patches[0].Session != session {
		t.Fatalf("story edit: status %d, response %#v", code, response)
	}
	code, response = f.swap(t, token, map[string]string{"badge.ktml": strings.Replace(badgeSource, `b "`, `i "`, 1)})
	if code != http.StatusOK || len(response.Patches) != 1 || response.Patches[0].HTML != `<i data-ktml-i="@1">edited</i>` {
		t.Fatalf("component edit: status %d, response %#v", code, response)
	}
}
