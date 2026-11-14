package explorer

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/dev/hmr"
)

// A preview without control changes must not pin the story's values: a
// story edit has to reach it through the swap.
func TestSwap_GIVENAPreviewWithoutControlChanges_WHENItsStoryChanges_THENThePatchHasTheNewStoryValue(t *testing.T) {
	s, backend, root := newTestServer(t, fixtureFiles, nil)
	request, _ := http.NewRequest(http.MethodGet, backend.URL+Prefix+"preview?story="+url.QueryEscape(runningKey), nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	session := response.Header.Get(hmr.RequestHeader)
	s.registry().Registry().Connect(session)
	path := filepath.Join(root, "ui", "card.story.ktml")
	fixed := strings.Replace(fixtureFiles["ui/card.story.ktml"], `callId = "c-1"`, `callId = "c-2"`, 1)
	if err := os.WriteFile(path, []byte(fixed), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := s.registry().Swap(context.Background(), hmr.SwapRequest{Generation: 1, Files: []hmr.SwapFile{{Path: path, Source: fixed}}})
	if err != nil || len(result.Patches) != 1 || !strings.Contains(result.Patches[0].HTML, "running c-2") {
		t.Fatalf("swap = %+v, %v", result, err)
	}
}
