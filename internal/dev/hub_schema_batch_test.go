package dev

import (
	"context"
	"slices"
	"testing"
)

func TestHub_GIVENHostSchemaChange_WHENReloaded_THENRegeneratesSchemaAndTransitiveDependentsWithFullReload(t *testing.T) {
	fixture := newBatchHubFixture(t, map[string]string{
		"user.ktmlschema": "record User [name: string]",
		"card.ktml":       "import { User } from \"./user.ktmlschema\"\nCard [user: User] { span \"{user.name}\" }",
		"page.ktml":       "import { Card } from \"./card.ktml\"\nPage { div }",
		"other.ktml":      `Other { div }`,
	})
	writeHubSource(t, "user.ktmlschema", "record User [name: string, id: string]")

	fixture.hub.reload(context.Background(), []string{"user.ktmlschema"})

	fixture.expectReload(t)
	if len(fixture.batches) != 1 {
		t.Fatalf("generation calls = %d, want one batch", len(fixture.batches))
	}
	if got, want := fixture.batches[0].Generate, []string{"card.ktml", "page.ktml", "user.ktmlschema"}; !slices.Equal(got, want) {
		t.Errorf("batch generates %v, want %v", got, want)
	}
}

func TestHub_GIVENHostSchemaChangeBreakingDependent_WHENReloaded_THENKeepsLastWorkingOutput(t *testing.T) {
	fixture := newBatchHubFixture(t, map[string]string{
		"user.ktmlschema": "record User [name: string]",
		"card.ktml":       "import { User } from \"./user.ktmlschema\"\nCard [user: User] { span \"{user.name}\" }",
	})
	writeHubSource(t, "user.ktmlschema", "record User [title: string]")

	fixture.hub.reload(context.Background(), []string{"user.ktmlschema"})

	fixture.expectNoEvent(t)
	if len(fixture.batches) != 0 {
		t.Fatalf("generation calls = %d, want none for an invalid dependent", len(fixture.batches))
	}
	if !fixture.hub.pending["user.ktmlschema"] {
		t.Errorf("schema change was not kept pending for the next batch")
	}
}

func TestClassifySchemaChange_GIVENAnySchemaEdit_WHENClassified_THENRequiresRebuild(t *testing.T) {
	if !requiresRebuild("app/user.ktmlschema") || requiresRebuild("app/card.ktml") {
		t.Fatalf("requiresRebuild classification is wrong")
	}
}
