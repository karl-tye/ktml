package compiler_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
)

const contentToolingSchema = `record Address [
	city: string,
	verified: boolean,
	note?: string,
]

record User [
	name: string,
	admin: boolean,
	address?: Address,
]
`

const contentToolingModal = `Modal [title: string, children: content, footer?: content, actions?: content] {
	section {
		h2 "{title}"
		children
		if footer? {
			footer
		}
		actions
	}
}
`

const contentToolingPage = `import { Modal } from "./modal.ktml"
import { User } from "./user.ktmlschema"

Page [user: User, note?: string, on: boolean] {
	Modal title="Pay" {
		p "body"
		slot footer {
			button "Pay"
		}
	}
	if note? && user.address? {
		p "{note} {user.address.city}"
	}
}
`

func contentToolingWorkspace(t *testing.T) (string, *compiler.Snapshot) {
	t.Helper()
	dir := writeWorkspace(t, map[string]string{"user.ktmlschema": contentToolingSchema, "modal.ktml": contentToolingModal, "page.ktml": contentToolingPage})
	snapshot, err := compiler.NewWorkspace(dir).Index()
	if err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	if !snapshot.Valid() {
		t.Fatalf("workspace is invalid: %v", snapshot.Err())
	}
	return dir, snapshot
}

func TestSchemaCompletions_GIVENSlotFillsAndConditions_WHENCompleting_THENOffersSlotsAndNarrowedPaths(t *testing.T) {
	dir, snapshot := contentToolingWorkspace(t)
	const header = "import { Modal } from \"./modal.ktml\"\nimport { User } from \"./user.ktmlschema\"\n\nPage [user: User, note?: string, on: boolean] {\n"
	tests := []struct {
		name, source string
		context      bool
		want, kinds  string
	}{
		{name: "slot names in a call body", source: header + "\tModal title=\"x\" {\n\t\tslot |", context: true, want: "footer,actions", kinds: "slot"},
		{name: "slot name prefix", source: header + "\tModal title=\"x\" {\n\t\tslot fo|", context: true, want: "footer,actions"},
		{name: "filled slots are excluded", source: header + "\tModal title=\"x\" {\n\t\tslot footer {}\n\t\tslot |", context: true, want: "actions"},
		{name: "slot in a continuation call", source: header + "\tModal (\n\t\ttitle=\"x\"\n\t) {\n\t\tslot |", context: true, want: "footer,actions"},
		{name: "roots after if", source: header + "\tif |", context: true, want: "user,note,on", kinds: "input"},
		{name: "roots after not", source: header + "\tif !|", context: true, want: "user,note,on"},
		{name: "roots after and", source: header + "\tif on && |", context: true, want: "user,note,on"},
		{name: "roots in a group", source: header + "\tif on && (|", context: true, want: "user,note,on"},
		{name: "fields in a condition", source: header + "\tif user.|", context: true, want: "name,admin,address", kinds: "field"},
		{name: "optional record has no fields", source: header + "\tif user.address.|", context: true, want: ""},
		{name: "operand narrowing after and", source: header + "\tif user.address? && user.address.|", context: true, want: "city,verified,note"},
		{name: "branch narrowing in a body path", source: header + "\tif user.address? {\n\t\tp \"{user.address.|", context: true, want: "city,verified,note"},
		{name: "else branch is not narrowed", source: header + "\tif user.address? {} else {\n\t\tp \"{user.address.|", context: true, want: ""},
		{name: "else of a negated presence is narrowed", source: header + "\tif !user.address? {} else {\n\t\tif user.address.|", context: true, want: "city,verified,note"},
		{name: "content type position", source: "Card [actions?: |", context: true, want: "string,boolean,number,integer,content"},
		{name: "slot outside a call body is not a slot context", source: header + "\tslot |", context: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			offset := strings.Index(tt.source, "|")
			source := []byte(strings.Replace(tt.source, "|", "", 1))
			list, ok := compiler.SchemaCompletions(filepath.Join(dir, "page.ktml"), source, offset, snapshot)
			if ok != tt.context {
				t.Fatalf("context = %v, want %v (items %#v)", ok, tt.context, list.Items)
			}
			if !ok {
				return
			}
			var labels, kinds []string
			for _, item := range list.Items {
				labels = append(labels, item.Label)
				kinds = append(kinds, string(item.Kind))
			}
			if got := strings.Join(labels, ","); got != tt.want {
				t.Errorf("labels = %q, want %q", got, tt.want)
			}
			if tt.kinds != "" {
				want := strings.TrimSuffix(strings.Repeat(tt.kinds+",", len(kinds)), ",")
				if got := strings.Join(kinds, ","); got != want {
					t.Errorf("kinds = %q, want %q", got, want)
				}
			}
		})
	}
}

func TestContentOccurrenceAt_GIVENOutletsFillsAndDeclarations_THENResolvesTheContentInput(t *testing.T) {
	dir, snapshot := contentToolingWorkspace(t)
	modal := filepath.Join(dir, "modal.ktml")
	page := filepath.Join(dir, "page.ktml")
	tests := []struct {
		file, source, context, want string
	}{
		{file: modal, source: contentToolingModal, context: "footer?: content", want: "slot footer?: content (Modal)"},
		{file: modal, source: contentToolingModal, context: "\t\t\tfooter\n", want: "slot footer?: content (Modal)"},
		{file: modal, source: contentToolingModal, context: "\t\tchildren\n", want: "children: content (children of Modal)"},
		{file: modal, source: contentToolingModal, context: "footer? {", want: "slot footer?: content (Modal)"},
		{file: page, source: contentToolingPage, context: "footer {", want: "slot footer?: content (Modal)"},
	}
	for _, tt := range tests {
		t.Run(tt.context, func(t *testing.T) {
			offset := strings.Index(tt.source, tt.context) + len(tt.context) - len(strings.TrimLeft(tt.context, "\t"))
			occurrence, ok := snapshot.ContentOccurrenceAt(tt.file, offset)
			if !ok {
				t.Fatalf("no content occurrence at %d", offset)
			}
			if got := compiler.ContentSignature(occurrence.Component, occurrence.Input); got != tt.want {
				t.Errorf("signature = %q, want %q", got, tt.want)
			}
		})
	}
	if _, ok := snapshot.ContentOccurrenceAt(page, strings.Index(contentToolingPage, "button")); ok {
		t.Error("an element resolved as a content occurrence")
	}
}

func TestContentRename_GIVENANamedSlot_WHENRenamed_THENEditsDeclarationOutletsAndFillsAcrossFiles(t *testing.T) {
	dir, snapshot := contentToolingWorkspace(t)
	modal := filepath.Join(dir, "modal.ktml")
	occurrence, ok := snapshot.ContentOccurrenceAt(modal, strings.Index(contentToolingModal, "footer?"))
	if !ok {
		t.Fatal("no footer declaration")
	}
	ranges, err := snapshot.ContentRename(occurrence, "bottom")
	if err != nil {
		t.Fatalf("ContentRename() error = %v", err)
	}
	var spelled []string
	for _, current := range ranges {
		spelled = append(spelled, filepath.Base(current.File)+":"+strings.Repeat("x", current.End-current.Start))
	}
	// declaration, the presence test, the outlet, and the fill.
	if got := strings.Join(spelled, ","); got != "modal.ktml:xxxxxx,modal.ktml:xxxxxx,modal.ktml:xxxxxx,page.ktml:xxxxxx" {
		t.Errorf("ranges = %s (%#v)", got, ranges)
	}
	for _, name := range []string{"children", "title", "actions", "footer", "9x", "for"} {
		if _, err := snapshot.ContentRename(occurrence, name); err == nil {
			t.Errorf("rename to %q was accepted", name)
		}
	}
	children, _ := snapshot.ContentOccurrenceAt(modal, strings.Index(contentToolingModal, "children: content"))
	if _, err := snapshot.ContentRename(children, "body"); err == nil {
		t.Error("children rename was accepted")
	}
}

func TestSemanticTokens_GIVENContentAndConditions_THENClassifiesKeywordsOperatorsAndOutlets(t *testing.T) {
	source := "Modal [footer?: content] {\n\tif !footer? && on || (a) { footer } else { p }\n\tCard { slot actions { span } }\n\tchildren\n}\n"
	want := "parameter*:footer,type:content,keyword:if,operator:!,parameter:footer,operator:?,operator:&&,parameter:on,operator:||,parameter:a,parameter:footer,keyword:else,keyword:slot,parameter:actions,parameter:children"
	var got []string
	for _, current := range compiler.SemanticTokens([]byte(source)) {
		kind := string(current.Type)
		if current.Declaration {
			kind += "*"
		}
		got = append(got, kind+":"+source[current.Start:current.End])
	}
	if joined := strings.Join(got, ","); joined != want {
		t.Errorf("tokens =\n%s\nwant\n%s", joined, want)
	}
}

func TestPartSignature_GIVENNarrowedConditionPaths_THENDescribesTheNarrowedType(t *testing.T) {
	dir, snapshot := contentToolingWorkspace(t)
	page := filepath.Join(dir, "page.ktml")
	tests := []struct {
		context, want string
	}{
		{context: "note? &&", want: "input note?: string"},
		{context: "note} ", want: "input note?: string\nnarrowed to string"},
		{context: "address.city", want: "User.address?: Address\nnarrowed to Address"},
	}
	for _, tt := range tests {
		t.Run(tt.context, func(t *testing.T) {
			part, ok := snapshot.PathPartAt(page, strings.Index(contentToolingPage, tt.context))
			if !ok {
				t.Fatal("no path part")
			}
			if got := snapshot.PartSignature(part); got != tt.want {
				t.Errorf("signature = %q, want %q", got, tt.want)
			}
		})
	}
}
