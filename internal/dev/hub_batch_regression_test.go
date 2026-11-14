package dev

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/dev/livereload"
)

type batchHubFixture struct {
	hub     *Hub
	batches []Batch
	events  <-chan string
	fail    error
}

func newBatchHubFixture(t *testing.T, sources map[string]string) *batchHubFixture {
	t.Helper()
	t.Chdir(t.TempDir())
	for path, source := range sources {
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(target.Close)
	liveReload := livereload.NewServer(target.URL)
	events, closeSSE := openHubSSE(t, liveReload)
	t.Cleanup(closeSSE)
	fixture := &batchHubFixture{events: events}
	fixture.hub = NewHub(liveReload, nil, 0, func(batch Batch) error {
		fixture.batches = append(fixture.batches, batch)
		return fixture.fail
	})
	for _, path := range slices.Sorted(maps.Keys(sources)) {
		if err := fixture.hub.TrackFile(path); err != nil {
			t.Fatalf("TrackFile(%s) error = %v", path, err)
		}
	}
	return fixture
}

func (f *batchHubFixture) expectReload(t *testing.T) {
	t.Helper()
	select {
	case event := <-f.events:
		if event != "reload" {
			t.Fatalf("SSE event = %q, want reload", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("full reload was not published")
	}
}

func (f *batchHubFixture) expectNoEvent(t *testing.T) {
	t.Helper()
	select {
	case event := <-f.events:
		t.Fatalf("published SSE event %q for an unpublished batch", event)
	case <-time.After(50 * time.Millisecond):
	}
}

func writeHubSource(t *testing.T, path, source string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestHub_GIVENSeveralChangedFiles_WHENReloaded_THENGeneratesThemAsOneBatch(t *testing.T) {
	fixture := newBatchHubFixture(t, map[string]string{
		"first.ktml":  `First { div { "old" } }`,
		"second.ktml": `Second { div { "old" } }`,
	})
	writeHubSource(t, "first.ktml", `First { div { "new" } } Extra { div }`)
	writeHubSource(t, "second.ktml", `Second { div { "new" } } Other { div }`)

	fixture.hub.reload(context.Background(), []string{"first.ktml", "second.ktml"})

	fixture.expectReload(t)
	if len(fixture.batches) != 1 {
		t.Fatalf("generation calls = %d, want one batch", len(fixture.batches))
	}
	if got, want := fixture.batches[0].Generate, []string{"first.ktml", "second.ktml"}; !slices.Equal(got, want) {
		t.Errorf("batch generates %v, want %v", got, want)
	}
}

func TestHub_GIVENChangedImport_WHENReloaded_THENBatchRegeneratesTransitiveDependents(t *testing.T) {
	fixture := newBatchHubFixture(t, map[string]string{
		"leaf.ktml":   `Leaf [label: string] { span }`,
		"middle.ktml": "import { Leaf } from \"leaf\"\nMiddle { Leaf label=\"ok\" }",
		"page.ktml":   "import { Middle } from \"middle\"\nPage { Middle }",
		"other.ktml":  `Other { div }`,
	})
	writeHubSource(t, "leaf.ktml", `Leaf [label: string] { span } Extra { div }`)

	fixture.hub.reload(context.Background(), []string{"leaf.ktml"})

	fixture.expectReload(t)
	if len(fixture.batches) != 1 {
		t.Fatalf("generation calls = %d, want one batch", len(fixture.batches))
	}
	if got, want := fixture.batches[0].Generate, []string{"leaf.ktml", "middle.ktml", "page.ktml"}; !slices.Equal(got, want) {
		t.Errorf("batch generates %v, want changed source and transitive dependents %v", got, want)
	}
}

func TestHub_GIVENDeletedImport_WHENDependentStillImportsIt_THENPublishesNothingUntilDependentIsFixed(t *testing.T) {
	fixture := newBatchHubFixture(t, map[string]string{
		"leaf.ktml": `Leaf { span }`,
		"page.ktml": "import { Leaf } from \"leaf\"\nPage { Leaf }",
	})
	leafBaseline := fixture.hub.asts["leaf.ktml"]
	if err := os.Remove("leaf.ktml"); err != nil {
		t.Fatalf("remove leaf: %v", err)
	}

	fixture.hub.reload(context.Background(), []string{"leaf.ktml"})

	fixture.expectNoEvent(t)
	if len(fixture.batches) != 0 {
		t.Fatalf("generated %+v although the dependent no longer resolves its import", fixture.batches)
	}
	if fixture.hub.asts["leaf.ktml"] != leafBaseline {
		t.Fatal("deleted source baseline was dropped before publication")
	}

	writeHubSource(t, "page.ktml", `Page { div }`)
	fixture.hub.reload(context.Background(), []string{"page.ktml"})

	fixture.expectReload(t)
	if len(fixture.batches) != 1 {
		t.Fatalf("generation calls = %d, want one batch", len(fixture.batches))
	}
	batch := fixture.batches[0]
	if !slices.Equal(batch.Generate, []string{"page.ktml"}) || !slices.Equal(batch.Remove, []string{"leaf.ktml"}) {
		t.Errorf("batch = %+v, want page generated with the retained leaf removal", batch)
	}
	if _, tracked := fixture.hub.asts["leaf.ktml"]; tracked {
		t.Error("published removal kept the deleted source baseline")
	}
	if len(fixture.hub.pending) != 0 {
		t.Errorf("pending after publication = %v, want none", fixture.hub.pending)
	}
}

func TestHub_GIVENDeletedSourceAndItsDependent_WHENReloadedTogether_THENBatchRemovesBoth(t *testing.T) {
	fixture := newBatchHubFixture(t, map[string]string{
		"leaf.ktml":  `Leaf { span }`,
		"page.ktml":  "import { Leaf } from \"leaf\"\nPage { Leaf }",
		"other.ktml": `Other { div }`,
	})
	for _, path := range []string{"leaf.ktml", "page.ktml"} {
		if err := os.Remove(path); err != nil {
			t.Fatalf("remove %s: %v", path, err)
		}
	}

	fixture.hub.reload(context.Background(), []string{"leaf.ktml", "page.ktml"})

	fixture.expectReload(t)
	if len(fixture.batches) != 1 {
		t.Fatalf("generation calls = %d, want one batch", len(fixture.batches))
	}
	batch := fixture.batches[0]
	if len(batch.Generate) != 0 || !slices.Equal(batch.Remove, []string{"leaf.ktml", "page.ktml"}) {
		t.Errorf("batch = %+v, want both sources removed and nothing generated", batch)
	}
}

func TestHub_GIVENFailedBatch_WHENNextChangeArrives_THENRetriesUnpublishedFilesInSameBatch(t *testing.T) {
	fixture := newBatchHubFixture(t, map[string]string{
		"first.ktml":  `First { div { "old" } }`,
		"second.ktml": `Second { div { "old" } }`,
	})
	firstBaseline := fixture.hub.asts["first.ktml"]
	fixture.fail = errors.New("generation failed")
	writeHubSource(t, "first.ktml", `First { div { "new" } } Extra { div }`)

	fixture.hub.reload(context.Background(), []string{"first.ktml"})

	fixture.expectNoEvent(t)
	if fixture.hub.asts["first.ktml"] != firstBaseline {
		t.Fatal("failed batch replaced the published baseline")
	}
	fixture.fail = nil
	writeHubSource(t, "second.ktml", `Second { div { "new" } } Other { div }`)
	fixture.hub.reload(context.Background(), []string{"second.ktml"})

	fixture.expectReload(t)
	if got, want := fixture.batches[len(fixture.batches)-1].Generate, []string{"first.ktml", "second.ktml"}; !slices.Equal(got, want) {
		t.Errorf("retry batch generates %v, want %v", got, want)
	}
	if fixture.hub.asts["first.ktml"] == firstBaseline {
		t.Error("retried file baseline was not committed")
	}
}

func TestHub_GIVENEditorTempCopy_WHENWrittenAndRemoved_THENGeneratesNothing(t *testing.T) {
	fixture := newBatchHubFixture(t, map[string]string{"page.ktml": `Page { div }`})
	temp := ".conform.5858165.page.ktml"
	writeHubSource(t, temp, `Page { div }`)

	fixture.hub.reload(context.Background(), []string{temp})
	if err := os.Remove(temp); err != nil {
		t.Fatalf("remove %s: %v", temp, err)
	}
	fixture.hub.reload(context.Background(), []string{temp})

	fixture.expectNoEvent(t)
	if len(fixture.batches) != 0 {
		t.Errorf("generated %+v for an editor's hidden temp copy", fixture.batches)
	}
}

func TestHub_GIVENNonKTMLChange_WHENReloaded_THENGeneratesNothing(t *testing.T) {
	fixture := newBatchHubFixture(t, map[string]string{"page.ktml": `Page { div }`})

	fixture.hub.reload(context.Background(), []string{filepath.Join("assets", "site.css")})

	fixture.expectNoEvent(t)
	if len(fixture.batches) != 0 {
		t.Errorf("generated %+v for a non-KTML change", fixture.batches)
	}
}
