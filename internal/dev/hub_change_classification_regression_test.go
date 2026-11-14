package dev

import "testing"

func TestGetChangedComponent_GIVENRemovalAndCompatibleEdit_WHENClassified_THENForcesFullReload(t *testing.T) {
	oldDocument := compileHMRDocument(t, `Removed { div } Card { div { "old" } }`)
	newDocument := compileHMRDocument(t, `Card { div { "new" } }`)

	if got := getChangedComponent(oldDocument, newDocument); got != "" {
		t.Errorf("getChangedComponent() = %q, want full reload after component removal", got)
	}
}

func TestGetChangedComponent_GIVENImportAndCompatibleEdit_WHENClassified_THENForcesFullReload(t *testing.T) {
	oldDocument := compileHMRDocument(t, `import { Shared } from "old.ktml" Card { div { "old" } }`)
	newDocument := compileHMRDocument(t, `import { Shared } from "new.ktml" Card { div { "new" } }`)

	if got := getChangedComponent(oldDocument, newDocument); got != "" {
		t.Errorf("getChangedComponent() = %q, want full reload after import change", got)
	}
}
