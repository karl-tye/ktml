package gohtml

import (
	"encoding/json"
	"strings"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/formatter"
	"github.com/karl-tye/ktml/internal/parser"
)

// formatSource formats a document. Formatting prints the syntax tree, so a
// document with a syntax error would lose what the parser skipped: it is
// not formatted, as ktml fmt does not.
func formatSource(content []byte) (formatted string, ok bool) {
	var document *ast.Document
	func() {
		defer func() { recover() }()
		documentParser := parser.New(content)
		document = documentParser.ParseDocument()
		ok = len(documentParser.Errors()) == 0
	}()
	if document == nil || !ok {
		return "", false
	}
	return formatter.Format(document), true
}

// formatPart answers range and on-type formatting with the edits of full
// formatting that touch the requested lines, so formatting part of a
// document never disagrees with formatting all of it. A range formats the
// lines it covers. Typing } formats the block it closes; a newline formats
// the line it ended and never the new line, whose indentation the editor
// just placed for the cursor.
func formatPart(method string, payload []byte) ([]byte, error) {
	type position struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	}
	var request struct {
		ID           json.RawMessage `json:"id"`
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		Range struct {
			Start position `json:"start"`
			End   position `json:"end"`
		} `json:"range"`
		Position position `json:"position"`
		Ch       string   `json:"ch"`
	}
	if err := json.Unmarshal(payload, &request); err != nil {
		return nil, err
	}
	respond := func(edits []map[string]any) ([]byte, error) {
		return json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": edits})
	}
	content, ok := documentSource(request.TextDocument.URI)
	if !ok {
		return respond([]map[string]any{})
	}

	// first and last are the lines to format; never is a line no edit may
	// touch.
	first, last, never := 0, 0, -1
	switch {
	case method == "textDocument/rangeFormatting":
		first, last = request.Range.Start.Line, request.Range.End.Line
		if request.Range.End.Character == 0 && last > first {
			last--
		}
	case request.Ch == "\n":
		first, last, never = request.Position.Line-1, request.Position.Line-1, request.Position.Line
	case request.Ch == "}":
		first, last = closedBlock(content, request.Position.Line, request.Position.Character), request.Position.Line
	default:
		return respond([]map[string]any{})
	}

	lines := strings.Split(string(content), "\n")
	// The line a newline opened is usually blank, and formatting would
	// remove it with the line before: the document is formatted without
	// it, and only edits above it are kept.
	formatted := string(content)
	if never >= 0 && never < len(lines) && strings.TrimSpace(lines[never]) == "" {
		formatted = strings.Join(append(append([]string(nil), lines[:never]...), lines[never+1:]...), "\n")
	}
	output, ok := formatSource([]byte(formatted))
	if !ok {
		return respond([]map[string]any{})
	}
	edits := []map[string]any{}
	for _, edit := range lineEdits(formatted, output) {
		if formatted != string(content) && edit.End > never {
			continue
		}
		touched := edit.Start <= last && max(edit.End-1, edit.Start) >= first
		if !touched || never >= edit.Start && never < max(edit.End, edit.Start+1) {
			continue
		}
		start := map[string]any{"line": edit.Start, "character": 0}
		end := map[string]any{"line": edit.End, "character": 0}
		text := strings.Join(edit.Lines, "\n")
		if edit.End < len(lines) {
			if len(edit.Lines) > 0 {
				text += "\n"
			}
		} else {
			// The edit reaches the text after the last newline: it ends at
			// the end of the document.
			end = rangeAt(content, len(content), len(content))["end"].(map[string]any)
			if edit.Start == len(lines) {
				start = end
			}
		}
		edits = append(edits, map[string]any{"range": map[string]any{"start": start, "end": end}, "newText": text})
	}
	return respond(edits)
}

// lineEdits is formatter.LineEdits with each run that replaces lines one
// for one split into single lines, so a range takes only its own lines.
func lineEdits(source, formatted string) []formatter.LineEdit {
	var edits []formatter.LineEdit
	for _, edit := range formatter.LineEdits(source, formatted) {
		if len(edit.Lines) != edit.End-edit.Start {
			edits = append(edits, edit)
			continue
		}
		for index, line := range edit.Lines {
			edits = append(edits, formatter.LineEdit{Start: edit.Start + index, End: edit.Start + index + 1, Lines: []string{line}})
		}
	}
	return edits
}

// closedBlock returns the line of the { that the } before the cursor
// closes, matching braces outside quoted text and comments; the cursor's
// own line when there is none.
func closedBlock(content []byte, line, character int) int {
	offset := positionToOffset(content, line, character)
	var opened []int
	closed := -1
	for index := 0; index < offset && index < len(content); index++ {
		switch character := content[index]; {
		case character == '"' || character == '`':
			for index++; index < len(content) && content[index] != character; index++ {
				if character == '"' && content[index] == '\\' {
					index++
				}
			}
		case character == '/' && index+1 < len(content) && content[index+1] == '/':
			for index < len(content) && content[index] != '\n' {
				index++
			}
		case character == '{':
			opened = append(opened, index)
		case character == '}' && len(opened) > 0:
			closed = opened[len(opened)-1]
			opened = opened[:len(opened)-1]
		}
	}
	if closed < 0 {
		return line
	}
	openLine, _ := offsetToPosition(content, closed)
	return openLine
}
