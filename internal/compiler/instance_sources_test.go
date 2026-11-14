package compiler_test

import (
	"testing"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
)

func TestWorkspace_GIVENCallsAndOutlets_WHENLoaded_THENNumbersThemPerComponentInSourcePreOrder(t *testing.T) {
	dir := writeWorkspace(t, map[string]string{"ui.ktml": `Frame [children: content, foot?: content] {
	section {
		children
		if foot? {
			foot
		}
	}
}

Badge [label: string] {
	b "{label}"
}

Panel [labels: [string], open: boolean] default {
	p "closed"
}

Panel [open = true] {
	Frame {
		Badge label="first"
		for label in labels key label {
			Badge label="{label}"
		}
		slot foot {
			Badge label="foot"
		}
	}
	Badge label="last"
}
`})
	snapshot := loadEntry(t, dir, "ui.ktml")
	if err := snapshot.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}
	var calls, outlets []string
	var walk func(nodes []ast.Node)
	walk = func(nodes []ast.Node) {
		for _, node := range nodes {
			switch value := node.(type) {
			case *ast.ElementNode:
				if source, ok := snapshot.CallSource(value); ok {
					calls = append(calls, value.Name+"="+source)
				}
				if source, ok := snapshot.OutletSource(value); ok {
					outlets = append(outlets, value.Name+"="+source)
				}
				walk(value.Children)
			case *ast.SlotNode:
				walk(value.Body)
			case *ast.RepetitionNode:
				walk(value.Body)
			case *ast.ConditionNode:
				walk(value.Then)
				walk(value.Else)
			}
		}
	}
	for _, document := range snapshot.Documents() {
		for _, node := range document.Nodes {
			switch value := node.(type) {
			case *ast.ElementDecl:
				walk(value.Body)
			case *ast.RenderClause:
				walk(value.Body)
			}
		}
	}
	wantCalls := []string{"Frame=c0", "Badge=c1", "Badge=c2", "Badge=c3", "Badge=c4"}
	if !equalStrings(calls, wantCalls) {
		t.Errorf("calls = %v, want %v", calls, wantCalls)
	}
	wantOutlets := []string{"children=o0", "foot=o1"}
	if !equalStrings(outlets, wantOutlets) {
		t.Errorf("outlets = %v, want %v", outlets, wantOutlets)
	}
	var nilSnapshot *compiler.Snapshot
	if _, ok := nilSnapshot.CallSource(&ast.ElementNode{}); ok {
		t.Error("nil snapshot reports a call source")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestInstanceShape_GIVENBodies_WHENClassified_THENDocumentsIncludeWrappersOfDocumentComponents(t *testing.T) {
	dir := writeWorkspace(t, map[string]string{
		"layout.ktml": "Layout [children: content] {\n\tdoctype html\n\thtml {\n\t\tbody {\n\t\t\tchildren\n\t\t}\n\t}\n}\n",
		"page.ktml": `import { Layout } from "./layout.ktml"

Page [] {
	Layout {
		p "body"
	}
}

Shell [] {
	Page
}

Card [] {
	article "one root"
}

Pair [] {
	Card
	Card
}

Single [] {
	Card
}
`,
	})
	snapshot := loadEntry(t, dir, "page.ktml")
	if err := snapshot.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}
	file := dir + "/page.ktml"
	markup := func(element *ast.ElementNode) bool { return element.Name == "article" }
	want := map[string]compiler.InstanceShapeKind{
		"Page":   compiler.DocumentInstance,
		"Shell":  compiler.DocumentInstance,
		"Card":   compiler.SingleRootInstance,
		"Pair":   compiler.MultiRootInstance,
		"Single": compiler.MultiRootInstance,
	}
	for _, node := range snapshot.Document(file).Nodes {
		declaration, ok := node.(*ast.ElementDecl)
		if !ok {
			continue
		}
		if got := snapshot.InstanceShape(file, declaration.Body, markup); got != want[declaration.Name] {
			t.Errorf("%s shape = %d, want %d", declaration.Name, got, want[declaration.Name])
		}
	}
}
