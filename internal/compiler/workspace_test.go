package compiler_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
)

func TestWorkspace_GIVENNamedCrossFileImport_WHENLoaded_THENResolvesStableTypedSymbolsAndReferences(t *testing.T) {
	dir := t.TempDir()
	componentsPath := filepath.Join(dir, "components.ktml")
	pagePath := filepath.Join(dir, "page.ktml")
	writeWorkspaceFile(t, componentsPath, `Button [label: string] { button { "{label}" } }`)
	writeWorkspaceFile(t, pagePath, `import { Button } from "components"
Page [title: string] { Button label="{title}" }`)

	workspace := compiler.NewWorkspace(dir)
	snapshot, err := workspace.Load(pagePath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	button, ok := snapshot.Resolve(pagePath, "Button")
	if !ok {
		t.Fatal("Resolve(Button) = false")
	}
	if len(button.Inputs) != 1 || button.Inputs[0].Name != "label" || !button.Inputs[0].Type.IsScalar(compiler.StringType) || button.Inputs[0].Mode != compiler.RequiredInput {
		t.Errorf("Button inputs = %#v", button.Inputs)
	}
	if dependencies := snapshot.Dependencies(pagePath); len(dependencies) != 1 || dependencies[0] != componentsPath {
		t.Errorf("Dependencies(page) = %#v, want components.ktml", dependencies)
	}
	if references := snapshot.References(button.ID); len(references) != 2 {
		t.Errorf("References(Button) = %#v, want import and call", references)
	}

	reloaded, err := workspace.Load(pagePath)
	if err != nil {
		t.Fatalf("second Load() error = %v", err)
	}
	reloadedButton, ok := reloaded.Resolve(pagePath, "Button")
	if !ok || reloadedButton.ID != button.ID {
		t.Errorf("stable symbol ID changed from %q to %q", button.ID, reloadedButton.ID)
	}
}

func TestWorkspace_GIVENInvalidComponentCalls_WHENAnalyzed_THENReportsTypedSemanticDiagnostics(t *testing.T) {
	dir := t.TempDir()
	componentsPath := filepath.Join(dir, "components.ktml")
	pagePath := filepath.Join(dir, "page.ktml")
	writeWorkspaceFile(t, componentsPath, `Button [label: string, disabled: boolean] { button }`)
	writeWorkspaceFile(t, pagePath, `import { Button } from "components"
Page [count: number] {
	Button label="{count}" extra="value" {}
	Missing
}`)

	snapshot, err := compiler.NewWorkspace(dir).Load(pagePath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if snapshot.Valid() {
		t.Fatal("Snapshot.Valid() = true, want semantic errors")
	}
	codes := make(map[string]bool)
	for _, diagnostic := range snapshot.Diagnostics(pagePath) {
		codes[diagnostic.Code] = true
		if diagnostic.Start < 0 || diagnostic.End <= diagnostic.Start {
			t.Errorf("diagnostic has invalid range: %#v", diagnostic)
		}
	}
	for _, code := range []string{"input-type-mismatch", "unknown-input", "missing-input", "unknown-component"} {
		if !codes[code] {
			t.Errorf("diagnostics missing %q: %#v", code, snapshot.Diagnostics(pagePath))
		}
	}
}

func TestSnapshot_GIVENErrorsInMultipleFiles_WHENRequested_THENReturnsFirstRangedDiagnosticInPathOrder(t *testing.T) {
	dir := t.TempDir()
	aPath := filepath.Join(dir, "a.ktml")
	zPath := filepath.Join(dir, "z.ktml")
	writeWorkspaceFile(t, zPath, `Z { MissingZ }`)
	writeWorkspaceFile(t, aPath, `A { MissingA }`)

	snapshot, err := compiler.NewWorkspace(dir).Index()
	if err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	want := fmt.Sprintf("%s:4-12: [unknown-component] component MissingA is not in scope (and 1 more errors)", aPath)
	if err := snapshot.Err(); err == nil || err.Error() != want {
		t.Fatalf("Snapshot.Err() = %v, want %q", err, want)
	}
}

func TestWorkspace_GIVENDependencyEdits_WHENUpdated_THENInvalidatesDependentsAndPreservesLastValidSnapshot(t *testing.T) {
	dir := t.TempDir()
	componentsPath := filepath.Join(dir, "components.ktml")
	pagePath := filepath.Join(dir, "page.ktml")
	writeWorkspaceFile(t, componentsPath, `Button [label: string] { button { "{label}" } }`)
	writeWorkspaceFile(t, pagePath, `import { Button } from "components"
Page [title: string] { Button label="{title}" }`)
	workspace := compiler.NewWorkspace(dir)
	validSnapshot, err := workspace.Load(pagePath)
	if err != nil || !validSnapshot.Valid() {
		t.Fatalf("initial Load() = (%v, %v)", validSnapshot, err)
	}
	pageParseCount := workspace.ParseCount(pagePath)

	invalidSnapshot, err := workspace.Update(componentsPath, 1, []byte(`Button [label: string] { button`))
	if err != nil {
		t.Fatalf("Update(invalid) error = %v", err)
	}
	if invalidSnapshot.Valid() {
		t.Fatal("invalid update produced a valid snapshot")
	}
	invalidCodes := make(map[string]bool)
	for _, diagnostic := range invalidSnapshot.Diagnostics(pagePath) {
		invalidCodes[diagnostic.Code] = true
	}
	if !invalidCodes["invalid-dependency"] {
		t.Errorf("dependent diagnostics = %#v, want invalid-dependency", invalidSnapshot.Diagnostics(pagePath))
	}
	lastValid := workspace.LastValid()
	if lastValid == nil || !lastValid.Valid() {
		t.Fatal("LastValid() did not preserve the valid snapshot")
	}
	if _, ok := lastValid.Resolve(pagePath, "Button"); !ok {
		t.Error("LastValid() lost the imported Button symbol")
	}
	if got := workspace.ParseCount(pagePath); got != pageParseCount {
		t.Errorf("unchanged page parsed %d times, want %d", got, pageParseCount)
	}

	changedSnapshot, err := workspace.Update(componentsPath, 2, []byte(`Button [label: string, kind: string] { button { "{label}" } }`))
	if err != nil {
		t.Fatalf("Update(schema) error = %v", err)
	}
	codes := make(map[string]bool)
	for _, diagnostic := range changedSnapshot.Diagnostics(pagePath) {
		codes[diagnostic.Code] = true
	}
	if !codes["missing-input"] {
		t.Errorf("dependent diagnostics = %#v, want missing-input", changedSnapshot.Diagnostics(pagePath))
	}
	restoredSnapshot, err := workspace.Close(componentsPath)
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !restoredSnapshot.Valid() {
		t.Errorf("Close() did not restore disk-backed validity: %#v", restoredSnapshot.Diagnostics(pagePath))
	}
}

func TestWorkspace_GIVENDefaultedInput_WHENCallerOmitsIt_THENSchemaIsTypedAndCallIsValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "components.ktml")
	writeWorkspaceFile(t, path, `Button [label: string, disabled: boolean = false] { button disabled="{disabled}" { "{label}" } }
Page { Button label="Save" }`)

	snapshot, err := compiler.NewWorkspace(dir).Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !snapshot.Valid() {
		t.Fatalf("snapshot diagnostics = %#v", snapshot.Diagnostics(path))
	}
	button, ok := snapshot.Resolve(path, "Button")
	if !ok || len(button.Inputs) != 2 {
		t.Fatalf("Button schema = %#v", button)
	}
	if got := button.Inputs[1].Mode; got != compiler.DefaultedInput {
		t.Errorf("disabled mode = %q, want %q", got, compiler.DefaultedInput)
	}
}

func TestWorkspace_GIVENIndexedPackage_WHENOneFileChanges_THENOnlyChangedSyntaxIsReparsed(t *testing.T) {
	dir := t.TempDir()
	firstPath := filepath.Join(dir, "first.ktml")
	secondPath := filepath.Join(dir, "second.ktml")
	writeWorkspaceFile(t, firstPath, `First { div }`)
	writeWorkspaceFile(t, secondPath, `Second { span }`)
	workspace := compiler.NewWorkspace(dir)

	snapshot, err := workspace.Index()
	if err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	if components := snapshot.Components(); len(components) != 2 {
		t.Fatalf("Components() = %#v, want both package components", components)
	}
	firstCount := workspace.ParseCount(firstPath)
	secondCount := workspace.ParseCount(secondPath)
	if _, err := workspace.Index(); err != nil {
		t.Fatalf("second Index() error = %v", err)
	}
	if workspace.ParseCount(firstPath) != firstCount || workspace.ParseCount(secondPath) != secondCount {
		t.Fatal("unchanged Index() reparsed package files")
	}
	if _, err := workspace.Update(firstPath, 1, []byte(`First { article }`)); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if workspace.ParseCount(firstPath) != firstCount+1 || workspace.ParseCount(secondPath) != secondCount {
		t.Errorf("parse counts = (%d, %d), want (%d, %d)", workspace.ParseCount(firstPath), workspace.ParseCount(secondPath), firstCount+1, secondCount)
	}
}

func TestWorkspace_GIVENVersionedAndUnversionedOverlays_THENAcceptsUnversionedChangesAndRejectsStaleVersions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "components.ktml")
	writeWorkspaceFile(t, path, `Initial { div }`)
	workspace := compiler.NewWorkspace(dir)
	if _, err := workspace.Load(path); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	first, err := workspace.Update(path, 0, []byte(`First { div }`))
	if err != nil {
		t.Fatalf("first unversioned Update() error = %v", err)
	}
	second, err := workspace.Update(path, 0, []byte(`Second { div }`))
	if err != nil {
		t.Fatalf("second unversioned Update() error = %v", err)
	}
	if _, ok := first.Resolve(path, "First"); !ok {
		t.Error("first unversioned update was not applied")
	}
	if _, ok := second.Resolve(path, "Second"); !ok {
		t.Error("second unversioned update was not applied")
	}

	current, err := workspace.Update(path, 2, []byte(`Current { div }`))
	if err != nil {
		t.Fatalf("versioned Update() error = %v", err)
	}
	stale, err := workspace.Update(path, 1, []byte(`Stale { div }`))
	if err != nil {
		t.Fatalf("stale Update() error = %v", err)
	}
	if stale != current {
		t.Error("stale version replaced the current snapshot")
	}
	if _, ok := stale.Resolve(path, "Current"); !ok {
		t.Error("stale version replaced current symbols")
	}
}

func writeWorkspaceFile(t *testing.T, path, source string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestWorkspace_GIVENANestedGoModule_WHENIndexed_THENItsDocumentsAreLeftOut(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{"internal/ui", "proto"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeWorkspaceFile(t, filepath.Join(dir, "go.mod"), "module example.com/app\n")
	writeWorkspaceFile(t, filepath.Join(dir, "internal", "ui", "ui.ktml"), `Card { div }`)
	writeWorkspaceFile(t, filepath.Join(dir, "proto", "go.mod"), "module example.com/proto\n")
	writeWorkspaceFile(t, filepath.Join(dir, "proto", "broken.ktml"), `Broken { div title="{missing.path}" }`)
	snapshot, err := compiler.NewWorkspace(dir).Index()
	if err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	if components := snapshot.Components(); len(components) != 1 {
		t.Fatalf("Components() = %#v, want only the root module's component", components)
	}
	if !snapshot.Valid() {
		t.Fatal("the nested module's invalid document made the workspace invalid")
	}
}
