package dev

import (
	"context"
	"errors"
	"os"
	"testing"
)

// staleGenerationFixture generates like `ktml generate`: it reads each
// source from disk when the batch runs and records the content its output
// was generated from. during runs inside a generation, after the reads and
// before the outputs are published, as a slow generation leaves time for a
// git stash and restore.
type staleGenerationFixture struct {
	*batchHubFixture
	outputs map[string]string
	during  func(round int)
	rounds  int
}

func newStaleGenerationFixture(t *testing.T, sources map[string]string) *staleGenerationFixture {
	t.Helper()
	fixture := &staleGenerationFixture{batchHubFixture: newBatchHubFixture(t, sources), outputs: make(map[string]string)}
	fixture.hub.generateCallback = func(batch Batch) error {
		fixture.batches = append(fixture.batches, batch)
		generated := make(map[string]string, len(batch.Generate))
		for _, file := range batch.Generate {
			content, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			generated[file] = string(content)
		}
		fixture.rounds++
		if fixture.during != nil {
			fixture.during(fixture.rounds)
		}
		for file, content := range generated {
			fixture.outputs[file] = content
		}
		return nil
	}
	return fixture
}

// expectOutputsMatchDisk checks that, once the hub is idle, every source's
// generated output was generated from the source now on disk.
func (f *staleGenerationFixture) expectOutputsMatchDisk(t *testing.T, files ...string) {
	t.Helper()
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if got := f.outputs[file]; got != string(content) {
			t.Errorf("%s output was generated from %q, but the source on disk is %q", file, got, content)
		}
	}
}

// TestHub_GIVENSourceRestoredDuringGeneration_WHENNoLaterEventArrives_THENRegeneratesFromTheSourceOnDisk
// reproduces a git stash during a commit's pre-commit hooks: the batch reads
// the stashed, older source; the restore lands while generation runs; and
// its events are lost or folded into the batch already running, so no later
// batch names the file. The hub must still leave output generated from the
// restored source.
func TestHub_GIVENSourceRestoredDuringGeneration_WHENNoLaterEventArrives_THENRegeneratesFromTheSourceOnDisk(t *testing.T) {
	fixture := newStaleGenerationFixture(t, map[string]string{
		"card.ktml": `Card { div { "committed" } }`,
	})
	// The stash puts the older source back, and an event starts a batch.
	writeHubSource(t, "card.ktml", `Card { div { "older" } } Extra { div }`)
	fixture.during = func(round int) {
		if round == 1 {
			writeHubSource(t, "card.ktml", `Card { div { "committed" } }`)
		}
	}

	fixture.hub.reload(context.Background(), []string{"card.ktml"})

	fixture.expectOutputsMatchDisk(t, "card.ktml")
}

// TestHub_GIVENRapidWritesDuringGeneration_WHENTheyEnd_THENTheLastSourceIsGenerated
// writes A, B, then A' while a generation runs, as editors and formatters
// do, and checks the hub ends on A'.
func TestHub_GIVENRapidWritesDuringGeneration_WHENTheyEnd_THENTheLastSourceIsGenerated(t *testing.T) {
	fixture := newStaleGenerationFixture(t, map[string]string{
		"list.ktml": `List { ul }`,
	})
	writeHubSource(t, "list.ktml", `List { ul { li "a" } } A { div }`)
	fixture.during = func(round int) {
		switch round {
		case 1:
			writeHubSource(t, "list.ktml", `List { ul { li "b" } } B { div }`)
		case 2:
			writeHubSource(t, "list.ktml", `List { ul { li "a2" } } A { div }`)
		}
	}

	fixture.hub.reload(context.Background(), []string{"list.ktml"})

	fixture.expectOutputsMatchDisk(t, "list.ktml")
}

// TestHub_GIVENStashRemovesASourceDuringGeneration_WHENRestored_THENItIsGenerated
// covers a source the stash removes: the batch sees it removed, the restore
// brings it back while the batch runs, and the hub must generate it.
func TestHub_GIVENStashRemovesASourceDuringGeneration_WHENRestored_THENItIsGenerated(t *testing.T) {
	fixture := newStaleGenerationFixture(t, map[string]string{
		"page.ktml":  `Page { div }`,
		"badge.ktml": `Badge { span }`,
	})
	if err := os.Remove("badge.ktml"); err != nil {
		t.Fatal(err)
	}
	fixture.during = func(round int) {
		if round == 1 {
			writeHubSource(t, "badge.ktml", `Badge { span { "restored" } }`)
		}
	}

	fixture.hub.reload(context.Background(), []string{"badge.ktml"})

	fixture.expectOutputsMatchDisk(t, "badge.ktml")
}
