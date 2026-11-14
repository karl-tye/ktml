package compiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/symbolindex"
)

const indexSchema = `record User [
	name: string,
	tags: [Tag],
]

record Tag [
	id: string,
	label: string,
]

state Payment {
	Unpaid,
	Pending [reference: string],
}
`

const indexModal = `Modal [title: string, children: content, footer?: content] {
	section {
		h2 "{title}"
		children
		if footer? {
			footer
		}
	}
}
`

const indexPage = `import { Modal } from "./modal.ktml"
import { User, Payment as Pay } from "./user.ktmlschema"

Page [user: User, payment: Pay] {
	Modal title="{user.name}" {
		p "body"
		slot footer {
			for tag in user.tags key tag.id {
				span "{tag.label}"
			}
		}
	}
}

Badge [payment: Pay]

Badge [payment = Pending, reference] { "{reference}" }

Badge [payment = Unpaid] { "-" }
`

func indexWorkspace(t *testing.T) (dir string, snapshot *Snapshot) {
	t.Helper()
	dir = t.TempDir()
	for name, source := range map[string]string{"user.ktmlschema": indexSchema, "modal.ktml": indexModal, "page.ktml": indexPage} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := NewWorkspace(dir).Index()
	if err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	for _, document := range []string{"user.ktmlschema", "modal.ktml", "page.ktml"} {
		for _, diagnostic := range snapshot.Diagnostics(filepath.Join(dir, document)) {
			t.Errorf("%s: %v", document, diagnostic)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
	return dir, snapshot
}

// spelled returns the source text of a range and its line, for assertions.
func spelled(source string, r SourceRange) string {
	return source[r.Start:r.End]
}

func TestSymbolIndex_GIVENAWorkspace_WHENIndexed_THENEachDocumentHoldsItsDeclarations(t *testing.T) {
	dir, snapshot := indexWorkspace(t)
	page, modal, schema := filepath.Join(dir, "page.ktml"), filepath.Join(dir, "modal.ktml"), filepath.Join(dir, "user.ktmlschema")
	index := snapshot.SymbolIndex([]string{page, modal, schema})

	declared := func(file string) map[string]bool {
		names := make(map[string]bool)
		for _, symbol := range index[file].Symbols {
			names[string(symbol.Kind)+" "+symbol.Name] = true
		}
		return names
	}
	tests := []struct {
		file string
		want []string
	}{
		{modal, []string{"component Modal", "input title", "slot children", "slot footer"}},
		{page, []string{"component Page", "input user", "input payment", "component Badge", "loop-binding tag", "binding reference", "alias Pay"}},
		{schema, []string{"record User", "field name", "field tags", "record Tag", "state Payment", "variant Pending", "field reference"}},
	}
	for _, test := range tests {
		got := declared(test.file)
		for _, want := range test.want {
			if !got[want] {
				t.Errorf("%s declares %v, want %q", filepath.Base(test.file), got, want)
			}
		}
	}
}

func TestSymbolIndex_GIVENAWorkspace_WHENIndexed_THENReferencesNameTheirSymbolsAndTheirCaller(t *testing.T) {
	dir, snapshot := indexWorkspace(t)
	page, modal := filepath.Join(dir, "page.ktml"), filepath.Join(dir, "modal.ktml")
	index := snapshot.SymbolIndex([]string{page})
	modalID, pageID := componentID(modal, "Modal"), componentID(page, "Page")

	type want struct {
		symbol SymbolID
		kind   symbolindex.ReferenceKind
		text   string
		from   SymbolID
	}
	wants := []want{
		{modalID, symbolindex.ImportReference, "Modal", ""},
		{modalID, symbolindex.CallReference, "Modal", pageID},
		{InputID(modalID, "title"), symbolindex.WriteReference, "title", pageID},
		{InputID(modalID, "footer"), symbolindex.ReadReference, "footer", pageID},
		{InputID(pageID, "user"), symbolindex.ReadReference, "user", pageID},
		{fieldID(recordID(filepath.Join(dir, "user.ktmlschema"), "User"), "name"), symbolindex.ReadReference, "name", pageID},
	}
	var got []string
	for _, reference := range index[page].References {
		got = append(got, string(reference.Symbol)+" "+string(reference.Kind)+" "+spelled(indexPage, reference.Range)+" from "+string(reference.From))
	}
	for _, want := range wants {
		line := string(want.symbol) + " " + string(want.kind) + " " + want.text + " from " + string(want.from)
		found := false
		for _, candidate := range got {
			found = found || candidate == line
		}
		if !found {
			t.Errorf("missing reference %q in:\n%s", line, strings.Join(got, "\n"))
		}
	}

	var item IndexedSymbol
	for _, symbol := range index[page].Symbols {
		if symbol.Kind == symbolindex.LoopBinding {
			item = symbol
		}
	}
	uses := 0
	for _, reference := range index[page].References {
		if reference.Symbol == item.ID {
			uses++
			if spelled(indexPage, reference.Range) != "tag" {
				t.Errorf("item use spells %q", spelled(indexPage, reference.Range))
			}
		}
	}
	if uses != 2 {
		t.Errorf("item tag has %d uses, want its key root and its body root", uses)
	}
}

func TestWorkspaceLoad_GIVENNothingChanged_WHENLoadedAgain_THENReusesTheSnapshotUntilTheDiskChanges(t *testing.T) {
	dir := t.TempDir()
	page := filepath.Join(dir, "page.ktml")
	card := filepath.Join(dir, "card.ktml")
	write := func(path, source string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(page, "import { Card } from \"./card.ktml\"\n\nPage {\n\tCard\n}\n")
	workspace := NewWorkspace(dir)
	first, err := workspace.Load(page)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Diagnostics(page)) == 0 {
		t.Fatal("want an unresolved import before card.ktml exists")
	}
	if again, _ := workspace.Load(page); again != first {
		t.Error("an unchanged Load analyzed again")
	}

	write(card, "Card {\n\tp \"c\"\n}\n")
	created, _ := workspace.Load(page)
	if created == first || len(created.Diagnostics(page)) != 0 {
		t.Fatalf("after card.ktml appeared: diagnostics = %v", created.Diagnostics(page))
	}
	if again, _ := workspace.Load(card); again != created {
		t.Error("loading a document the snapshot holds analyzed again")
	}

	write(card, "Card [title: string] {\n\tp \"{title}\"\n}\n")
	edited, _ := workspace.Load(page)
	if edited == created || len(edited.Diagnostics(page)) == 0 {
		t.Errorf("after card.ktml gained a required input: diagnostics = %v", edited.Diagnostics(page))
	}
	if _, err := workspace.Update(page, 2, []byte("Page {\n\tp \"x\"\n}\n")); err != nil {
		t.Fatal(err)
	}
	if overlaid, _ := workspace.Load(page); overlaid == edited || overlaid.Source(page)[0] != 'P' || len(overlaid.Diagnostics(page)) != 0 {
		t.Errorf("after an overlay: diagnostics = %v", overlaid.Diagnostics(page))
	}
}
