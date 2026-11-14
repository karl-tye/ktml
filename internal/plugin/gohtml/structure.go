package gohtml

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/parser"
)

// foldingRanges returns a folding range for every construct that spans
// lines: declarations and their input lists, element bodies and attribute
// groups, conditions, repetitions, slot fills, raw text, and comments.
func foldingRanges(source []byte) []map[string]int {
	var document *ast.Document
	func() {
		defer func() { recover() }()
		document = parser.New(source).ParseDocument()
	}()
	ranges := []map[string]int{}
	seen := make(map[[2]int]bool)
	add := func(start, end int) {
		if start < 0 || end > len(source) || start >= end {
			return
		}
		// The last line is the line of the range's last byte.
		first := bytes.Count(source[:start], []byte("\n"))
		last := bytes.Count(source[:end-1], []byte("\n"))
		key := [2]int{first, last}
		if first < last && !seen[key] {
			seen[key] = true
			ranges = append(ranges, map[string]int{"startLine": first, "endLine": last})
		}
	}
	var nodes func([]ast.Node)
	nodes = func(body []ast.Node) {
		for _, node := range body {
			switch value := node.(type) {
			case *ast.ElementNode:
				add(value.P, value.E)
				if value.AttributeGroup != nil {
					add(value.AttributeGroup.Start, value.AttributeGroup.End)
				}
				for _, attribute := range value.Attributes {
					if attribute.BagRange != nil {
						add(attribute.BagRange.Start, attribute.BagRange.End)
					}
				}
				nodes(value.Children)
			case *ast.ConditionNode:
				add(value.P, value.E)
				nodes(value.Then)
				nodes(value.Else)
			case *ast.RepetitionNode:
				add(value.P, value.E)
				nodes(value.Body)
			case *ast.SlotNode:
				add(value.P, value.E)
				nodes(value.Body)
			case *ast.RawTextNode, *ast.HTMLCommentNode:
				add(value.Pos(), value.End())
			}
		}
	}
	if document == nil {
		return ranges
	}
	for _, node := range document.Nodes {
		switch value := node.(type) {
		case *ast.ElementDecl:
			add(value.P, value.E)
			// The input list.
			if bracket := bytes.IndexByte(source[value.P:value.E], '['); bracket >= 0 {
				if closing := bytes.IndexByte(source[value.P+bracket:value.E], ']'); closing >= 0 {
					add(value.P+bracket, value.P+bracket+closing+1)
				}
			}
			nodes(value.Body)
		case *ast.RenderClause:
			add(value.P, value.E)
			nodes(value.Body)
		case *ast.RecordDecl, *ast.StateDecl:
			add(node.Pos(), node.End())
		}
	}
	return ranges
}

// referenceLenses returns a code lens over each component declared in path
// with the number of its references across the project.
func referenceLenses(path string) []map[string]any {
	lenses := []map[string]any{}
	snapshot, err := projectSnapshot(path)
	if err != nil {
		return lenses
	}
	source := snapshot.Source(path)
	if source == nil {
		return lenses
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		canonical = path
	}
	components := snapshot.Components()
	sort.Slice(components, func(i, j int) bool { return components[i].Range.Start < components[j].Range.Start })
	for _, component := range components {
		if component.Range.File != path && component.Range.File != canonical {
			continue
		}
		count := len(snapshot.References(component.ID))
		title := fmt.Sprintf("%d references", count)
		if count == 1 {
			title = "1 reference"
		}
		lenses = append(lenses, map[string]any{
			"range":   rangeAt(source, component.Range.Start, component.Range.End),
			"command": map[string]any{"title": title, "command": ""},
		})
	}
	return lenses
}
