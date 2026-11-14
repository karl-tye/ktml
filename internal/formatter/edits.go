package formatter

import "strings"

// LineEdit replaces the source lines [Start, End) with Lines. Lines are the
// source split at "\n", so the text after the last newline is a line too.
type LineEdit struct {
	Start, End int
	Lines      []string
}

// maxDiffCells bounds the line diff's table; a larger difference becomes
// one edit.
const maxDiffCells = 4 << 20

// LineEdits returns the edits that turn source into formatted, line by line:
// each run of changed lines between unchanged ones is one edit. Range and
// on-type formatting apply the edits a range touches, so they always agree
// with formatting the whole document.
func LineEdits(source, formatted string) []LineEdit {
	before, after := strings.Split(source, "\n"), strings.Split(formatted, "\n")
	prefix := 0
	for prefix < len(before) && prefix < len(after) && before[prefix] == after[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(before)-prefix && suffix < len(after)-prefix && before[len(before)-1-suffix] == after[len(after)-1-suffix] {
		suffix++
	}
	removed, added := before[prefix:len(before)-suffix], after[prefix:len(after)-suffix]
	if len(removed) == 0 && len(added) == 0 {
		return nil
	}
	if (len(removed)+1)*(len(added)+1) > maxDiffCells {
		return []LineEdit{{Start: prefix, End: prefix + len(removed), Lines: added}}
	}

	// common[i][j] is the length of the longest common subsequence of
	// removed[i:] and added[j:].
	width := len(added) + 1
	common := make([]int32, (len(removed)+1)*width)
	for i := len(removed) - 1; i >= 0; i-- {
		for j := len(added) - 1; j >= 0; j-- {
			if removed[i] == added[j] {
				common[i*width+j] = common[(i+1)*width+j+1] + 1
			} else {
				common[i*width+j] = max(common[(i+1)*width+j], common[i*width+j+1])
			}
		}
	}
	var edits []LineEdit
	var current *LineEdit
	flush := func() {
		if current != nil {
			edits = append(edits, *current)
			current = nil
		}
	}
	i, j := 0, 0
	for i < len(removed) || j < len(added) {
		switch {
		case i < len(removed) && j < len(added) && removed[i] == added[j]:
			flush()
			i, j = i+1, j+1
		case j < len(added) && (i == len(removed) || common[i*width+j+1] >= common[(i+1)*width+j]):
			if current == nil {
				current = &LineEdit{Start: prefix + i, End: prefix + i}
			}
			current.Lines = append(current.Lines, added[j])
			j++
		default:
			if current == nil {
				current = &LineEdit{Start: prefix + i, End: prefix + i}
			}
			current.End = prefix + i + 1
			i++
		}
	}
	flush()
	return edits
}

// Apply applies line edits to source, for tests and callers that check
// edits against the formatter.
func Apply(source string, edits []LineEdit) string {
	lines := strings.Split(source, "\n")
	var result []string
	next := 0
	for _, edit := range edits {
		result = append(result, lines[next:edit.Start]...)
		result = append(result, edit.Lines...)
		next = edit.End
	}
	result = append(result, lines[next:]...)
	return strings.Join(result, "\n")
}
