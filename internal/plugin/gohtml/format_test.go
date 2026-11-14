package gohtml

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/textposition"
)

type textEdit struct {
	Range struct {
		Start, End struct{ Line, Character int }
	} `json:"range"`
	NewText string `json:"newText"`
}

// applyEdits applies non-overlapping LSP edits to source.
func applyEdits(t *testing.T, source string, raw []byte) string {
	t.Helper()
	var response struct {
		Result []textEdit `json:"result"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	edits := response.Result
	sort.Slice(edits, func(i, j int) bool { return edits[i].Range.Start.Line > edits[j].Range.Start.Line })
	for _, edit := range edits {
		start := textposition.Offset([]byte(source), edit.Range.Start.Line, edit.Range.Start.Character)
		end := textposition.Offset([]byte(source), edit.Range.End.Line, edit.Range.End.Character)
		if edit.Range.End.Line >= strings.Count(source, "\n")+1 {
			end = len(source)
		}
		source = source[:start] + edit.NewText + source[end:]
	}
	return source
}

// unformatted is canonical but for the indentation of lines 1 to 6.
const unformatted = "Page {\n  div {\n      p {\n  \"a\"\n      }\n  }\n    span {\n\t\t\"b\"\n\t}\n}\n"

func formatRequest(t *testing.T, method, source string, params map[string]any) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "page.ktml")
	openDocument(t, path, source, 1)
	params["id"] = 1
	params["textDocument"] = map[string]any{"uri": "file://" + path}
	return callLSP(t, method, params)
}

func lineRange(first, last int) map[string]any {
	return map[string]any{"start": map[string]any{"line": first, "character": 0}, "end": map[string]any{"line": last, "character": 0}}
}

func TestLSP_RangeFormatting_GIVENARange_WHENFormatted_THENOnlyItsLinesChangeAsFullFormattingWould(t *testing.T) {
	full, ok := formatSource([]byte(unformatted))
	if !ok {
		t.Fatal("fixture does not parse")
	}
	partial := applyEdits(t, unformatted, formatRequest(t, "textDocument/rangeFormatting", unformatted, map[string]any{"range": lineRange(1, 4)}))
	lines, fullLines := strings.Split(partial, "\n"), strings.Split(full, "\n")
	if lines[1] != fullLines[1] || lines[2] != fullLines[2] || lines[3] != fullLines[3] {
		t.Errorf("the range is not formatted: %q", partial)
	}
	if lines[4] != "      }" || lines[6] != "    span {" {
		t.Errorf("a line after the range changed: %q", partial)
	}
	everything := applyEdits(t, unformatted, formatRequest(t, "textDocument/rangeFormatting", unformatted, map[string]any{"range": lineRange(0, 11)}))
	if everything != full {
		t.Errorf("formatting every line = %q, want full formatting %q", everything, full)
	}
	viaFull := applyEdits(t, unformatted, formatRequest(t, "textDocument/formatting", unformatted, map[string]any{}))
	if viaFull != full {
		t.Errorf("full formatting = %q, want %q", viaFull, full)
	}
}

func TestLSP_OnTypeFormatting_GIVENAClosedBlockOrANewLine_WHENTyped_THENFormatsTheBlockOrTheEndedLine(t *testing.T) {
	full, _ := formatSource([]byte(unformatted))
	fullLines := strings.Split(full, "\n")

	// } typed at the end of line 5 closes the div opened on line 1.
	closed := strings.Split(applyEdits(t, unformatted, formatRequest(t, "textDocument/onTypeFormatting", unformatted, map[string]any{"position": map[string]any{"line": 5, "character": 3}, "ch": "}"})), "\n")
	for line := 1; line <= 5; line++ {
		if closed[line] != fullLines[line] {
			t.Errorf("after }, line %d = %q, want %q", line, closed[line], fullLines[line])
		}
	}
	if closed[6] != "    span {" {
		t.Errorf("after }, a line after the block changed: %q", closed[6])
	}

	// A newline typed after line 3 formats line 3 and leaves the new,
	// indented line 4 for the cursor.
	typed := "Page {\n\tdiv {\n\t\tp {\n  \"a\"\n\t\t\t\n\t\t}\n\t}\n}\n"
	ended := strings.Split(applyEdits(t, typed, formatRequest(t, "textDocument/onTypeFormatting", typed, map[string]any{"position": map[string]any{"line": 4, "character": 3}, "ch": "\n"})), "\n")
	if ended[3] != "\t\t\t\"a\"" || ended[4] != "\t\t\t" {
		t.Errorf("after a newline = %q", ended)
	}
}

func TestLSP_PartialFormatting_GIVENASyntaxError_WHENFormatted_THENTheDocumentIsUnchanged(t *testing.T) {
	broken := "Page [] {\n  div {\n      p \"a\n}\n"
	for _, test := range []struct {
		method string
		params map[string]any
	}{
		{"textDocument/rangeFormatting", map[string]any{"range": lineRange(0, 4)}},
		{"textDocument/onTypeFormatting", map[string]any{"position": map[string]any{"line": 3, "character": 1}, "ch": "}"}},
		{"textDocument/formatting", map[string]any{}},
	} {
		if got := applyEdits(t, broken, formatRequest(t, test.method, broken, test.params)); got != broken {
			t.Errorf("%s changed a document with a syntax error: %q", test.method, got)
		}
	}
}
