package compiler_test

import (
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
)

func TestWorkspace_GIVENValidDoctypeCommentsAndFrameworkNames_WHENLoaded_THENHasNoDiagnostics(t *testing.T) {
	snapshot, path := gapWorkspace(t, `Layout [title: string, children: content] {
	<!-- the shell -->
	doctype html
	html lang="en" {
		<!-- {not} -- interpolated -->
		body x-data @keydown.slash.window="focus()" hx-ext:inherited="sse" {
			children
		}
	}
}
`)
	if diagnostics := snapshot.Diagnostics(path); len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", codes(diagnostics))
	}
}

func TestWorkspace_GIVENInvalidDoctypesAndComments_WHENLoaded_THENReportsCodesAtRanges(t *testing.T) {
	tests := []struct {
		name, body, code, span string
	}{
		{name: "non-html doctype", body: "\tdoctype xml\n", code: "invalid-doctype", span: "xml"},
		{name: "doctype after an element", body: "\tdiv\n\tdoctype html\n", code: "misplaced-doctype", span: "doctype html"},
		{name: "second doctype", body: "\tdoctype html\n\tdoctype html\n", code: "misplaced-doctype", span: "doctype html"},
		{name: "doctype inside an element", body: "\thtml {\n\t\tdoctype html\n\t}\n", code: "misplaced-doctype", span: "doctype html"},
		{name: "doctype inside a condition", body: "\tif on {\n\t\tdoctype html\n\t}\n", code: "misplaced-doctype", span: "doctype html"},
		{name: "comment starting with >", body: "\t<!-->x-->\n", code: "invalid-html-comment", span: "<!-->x-->"},
		{name: "comment starting with ->", body: "\t<!---> x -->\n", code: "invalid-html-comment", span: "<!---> x -->"},
		{name: "nested comment opener", body: "\t<!-- a <!-- b -->\n", code: "invalid-html-comment", span: "<!-- a <!-- b -->"},
		{name: "bang close inside", body: "\t<!-- a --!> b -->\n", code: "invalid-html-comment", span: "<!-- a --!> b -->"},
		{name: "ending with <!-", body: "\t<!-- a <!--->\n", code: "invalid-html-comment", span: "<!-- a <!--->"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			component := "Page [on: boolean] {\n" + test.body + "}\n"
			snapshot, path := gapWorkspace(t, component)
			diagnostics := snapshot.Diagnostics(path)
			if len(diagnostics) != 1 || diagnostics[0].Code != test.code {
				t.Fatalf("diagnostics = %v, want one %s", codes(diagnostics), test.code)
			}
			source := "import { Payment, Money } from \"./payment.ktmlschema\"\n" + component
			if got := source[diagnostics[0].Start:diagnostics[0].End]; got != test.span {
				t.Fatalf("range = %q, want %q", got, test.span)
			}
		})
	}
}

func TestWorkspace_GIVENFrameworkNameOnAComponentCall_WHENLoaded_THENItIsAnUnknownInput(t *testing.T) {
	snapshot, path := gapWorkspace(t, `Fee [label: string] { span "{label}" }
Panel [] {
	Fee label="a" @click="x"
}
`)
	got := codes(snapshot.Diagnostics(path))
	if len(got) != 1 || !strings.HasSuffix(got[0], "unknown-input") {
		t.Fatalf("diagnostics = %v, want one unknown-input", got)
	}
}

func TestSemanticTokens_GIVENDoctypeAndFrameworkNames_THENClassifiesTheDoctypeKeyword(t *testing.T) {
	source := "Page [title: string] {\n\tdoctype html\n\tdiv @click=\"x\" data-title=\"{title}\"\n}\n"
	want := "parameter*:title,type:string,keyword:doctype,parameter:title"
	var got []string
	for _, current := range compiler.SemanticTokens([]byte(source)) {
		kind := string(current.Type)
		if current.Declaration {
			kind += "*"
		}
		got = append(got, kind+":"+source[current.Start:current.End])
	}
	if joined := strings.Join(got, ","); joined != want {
		t.Errorf("tokens = %s\nwant     %s", joined, want)
	}
}
