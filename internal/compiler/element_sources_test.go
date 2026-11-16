package compiler_test

import (
	"path/filepath"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
)

const timelineSource = `Card [title: string, children: content, foot?: content] {
	article class="card" {
		h3 class="card-title" "{title}"
		div class="card-body" {
			children
		}
		if foot? {
			footer {
				foot
			}
		}
	}
}

Badge [label: string] {
	span class="badge" "{label}"
}

Timeline [rows: [string], open: boolean] {
	section id="timeline" class="timeline" {
		h2 "Timeline"
		ul {
			for row in rows key row {
				li class="timeline-row row-{row}" {
					span class="when" "{row}"
					if open {
						span class="badge open" "open"
					} else {
						span class="badge" "closed"
					}
					Badge label="{row}"
				}
			}
		}
	}
}

Page [rows: [string]] {
	Card title="Jobs" {
		p class="intro" "Recent jobs"
		Timeline rows="{rows}" open=true
		slot foot {
			a href="/more" "More"
		}
	}
}

Shell [children: content] {
	Card title="Shell" {
		children
	}
}

Home [] {
	Shell {
		em class="hello" "Hello"
	}
}
`

func TestLocateElement_GIVENAnElementPath_WHENLocated_THENFindsEachElementsDeclaration(t *testing.T) {
	dir := writeWorkspace(t, map[string]string{"ui.ktml": timelineSource})
	snapshot := loadEntry(t, dir, "ui.ktml")
	if err := snapshot.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}
	file := filepath.Join(dir, "ui.ktml")
	id := func(name string) compiler.SymbolID {
		t.Helper()
		component, ok := snapshot.Resolve(file, name)
		if !ok {
			t.Fatalf("no component %s", name)
		}
		return component.ID
	}
	step := func(tag, elementID string, classes ...string) compiler.ElementStep {
		return compiler.ElementStep{Tag: tag, ID: elementID, Classes: classes}
	}
	type want struct {
		line, column int
		component    string
	}
	check := func(t *testing.T, got []compiler.ElementSource, wants []want) {
		t.Helper()
		if len(got) != len(wants) {
			t.Fatalf("got %d sources, want %d: %+v", len(got), len(wants), got)
		}
		for index, expected := range wants {
			source := got[index]
			if expected.line == 0 {
				if source.Found {
					t.Errorf("step %d: found %+v, want none", index, source)
				}
				continue
			}
			if !source.Found || source.Line != expected.line || source.Column != expected.column || source.Component != id(expected.component) {
				t.Errorf("step %d = %+v, want %s line %d column %d", index, source, expected.component, expected.line, expected.column)
			}
			if source.Found && filepath.Base(source.Range.File) != "ui.ktml" {
				t.Errorf("step %d file = %q", index, source.Range.File)
			}
		}
	}

	t.Run("nested rows, a repetition, and both condition branches", func(t *testing.T) {
		got := snapshot.LocateElement(id("Timeline"), nil, []compiler.ElementStep{
			step("section", "timeline", "timeline"),
			step("ul", ""),
			step("li", "", "timeline-row", "row-a"),
			step("span", "", "badge"),
		})
		// The else branch's span.badge: the then branch requires "open".
		check(t, got, []want{{20, 2, "Timeline"}, {22, 3, "Timeline"}, {24, 5, "Timeline"}, {29, 7, "Timeline"}})
		got = snapshot.LocateElement(id("Timeline"), nil, []compiler.ElementStep{
			step("section", "timeline", "timeline"), step("ul", ""), step("li", "", "timeline-row"), step("span", "", "badge", "open"),
		})
		if len(got) != 4 || got[3].Line != 27 {
			t.Errorf("open badge = %+v, want line 27", got)
		}
	})

	t.Run("a mismatched static id or class finds nothing below it", func(t *testing.T) {
		got := snapshot.LocateElement(id("Timeline"), nil, []compiler.ElementStep{
			step("section", "other"), step("ul", ""),
		})
		check(t, got, []want{{}, {}})
		got = snapshot.LocateElement(id("Timeline"), nil, []compiler.ElementStep{
			step("section", "timeline", "timeline"), step("ul", ""), step("li", "", "row-a"),
		})
		check(t, got, []want{{20, 2, "Timeline"}, {22, 3, "Timeline"}, {}})
	})

	t.Run("an outlet continues in the content its caller supplied", func(t *testing.T) {
		// Card's instance was rendered by Page's first call (c0).
		links := []compiler.ContentLink{{Caller: id("Page"), Site: "c0"}}
		got := snapshot.LocateElement(id("Card"), links, []compiler.ElementStep{
			step("article", "", "card"), step("div", "", "card-body"), step("p", "", "intro"),
		})
		check(t, got, []want{{2, 2, "Card"}, {4, 3, "Card"}, {40, 3, "Page"}})
		got = snapshot.LocateElement(id("Card"), links, []compiler.ElementStep{
			step("article", "", "card"), step("footer", ""), step("a", ""),
		})
		check(t, got, []want{{2, 2, "Card"}, {8, 4, "Card"}, {43, 4, "Page"}})
		// Without the link, the outlet's content is unknown.
		got = snapshot.LocateElement(id("Card"), nil, []compiler.ElementStep{
			step("article", "", "card"), step("div", "", "card-body"), step("p", "", "intro"),
		})
		check(t, got, []want{{2, 2, "Card"}, {4, 3, "Card"}, {}})
	})

	t.Run("content passed through two outlets", func(t *testing.T) {
		links := []compiler.ContentLink{{Caller: id("Shell"), Site: "c0"}, {Caller: id("Home"), Site: "c0"}}
		got := snapshot.LocateElement(id("Card"), links, []compiler.ElementStep{
			step("article", "", "card"), step("div", "", "card-body"), step("em", "", "hello"),
		})
		check(t, got, []want{{2, 2, "Card"}, {4, 3, "Card"}, {56, 3, "Home"}})
	})

	t.Run("an implicit tbody is skipped", func(t *testing.T) {
		dir := writeWorkspace(t, map[string]string{"t.ktml": "Grid [] {\n\ttable {\n\t\ttr {\n\t\t\ttd \"x\"\n\t\t}\n\t}\n}\n"})
		snapshot := loadEntry(t, dir, "t.ktml")
		grid, _ := snapshot.Resolve(filepath.Join(dir, "t.ktml"), "Grid")
		got := snapshot.LocateElement(grid.ID, nil, []compiler.ElementStep{step("table", ""), step("tbody", ""), step("tr", ""), step("td", "")})
		if len(got) != 4 || !got[0].Found || got[1].Found || got[2].Line != 3 || got[3].Line != 4 {
			t.Errorf("table = %+v", got)
		}
	})

	t.Run("an unknown component finds nothing", func(t *testing.T) {
		got := snapshot.LocateElement("nope", nil, []compiler.ElementStep{step("div", "")})
		if len(got) != 1 || got[0].Found {
			t.Errorf("unknown = %+v", got)
		}
	})
}
