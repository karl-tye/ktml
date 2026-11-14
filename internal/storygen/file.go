package storygen

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/parser"
)

// Markers open the blocks a tool maintains in a story Document. The marker
// line carries a hash of the block below it, up to the end of its
// declaration; a block whose text no longer matches was edited by hand, and
// the tool leaves it alone.
const (
	GeneratedMarker = "ktml:generated"
	CapturedMarker  = "ktml:captured"
)

// Import is an import declaration a story Document needs.
type Import struct {
	Path  string
	Names []string
}

// Block is a story a tool writes: its comment lines and declaration.
type Block struct {
	// Key is Component.Name.
	Key      string
	Comments []string
	Text     string
}

// Desired is what a tool wants a story Document to hold.
type Desired struct {
	// Header comments a new Document.
	Header   []string
	Imports  []Import
	Fixtures []FixtureDecl
	Stories  []Block
	// Accumulate keeps the tool's stories it does not list: captures add
	// stories and never remove them.
	Accumulate bool
}

// FileReport is what a merge did to one Document. Each list holds story
// keys, Component.Name, in Document order.
type FileReport struct {
	Added     []string
	Updated   []string
	Unchanged []string
	// Edited are stories whose marker no longer matches their text: they
	// are kept as they are, and not regenerated.
	Edited []string
	// Own are stories without a marker that a tool would have written
	// under the same name: they are kept, and the tool's story is not
	// written.
	Own []string
	// Removed are intact stories the tool no longer writes.
	Removed []string
}

// Changed reports whether the merge changed the Document.
func (r FileReport) Changed() bool {
	return len(r.Added)+len(r.Updated)+len(r.Removed) > 0
}

// existing is a top-level declaration of a story Document, with the marker
// block it belongs to, if any.
type existing struct {
	kind   string // "import", "fixture", "story"
	key    string // import path, fixture name, or Component.Name
	names  []string
	start  int // block start: the marker line, or the declaration
	end    int // declaration end
	marked bool
	intact bool
}

func blockHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])[:12]
}

// markerLine is the marker of a block with this body.
func markerLine(marker, body string) string {
	return "// " + marker + " " + blockHash(body)
}

// Merge updates a story Document's source with a tool's desired blocks.
// source is nil for a new Document. Blocks under the tool's marker whose
// text is intact are replaced or removed; every other block is kept, and a
// desired story whose name another block already uses is not written. The
// result is deterministic: merging its output again changes nothing.
func Merge(source []byte, desired Desired, marker string) ([]byte, FileReport, error) {
	var report FileReport
	if source == nil {
		source = []byte(newDocument(desired.Header))
	}
	text := string(source)
	p := parser.New(source)
	document := p.ParseDocument()
	if errs := p.Errors(); len(errs) > 0 {
		line, _ := lineColumn(text, errs[0].Pos)
		return nil, report, fmt.Errorf("line %d: %s; fix it first, the story generator does not edit a Document that does not parse", line, errs[0].Msg)
	}
	blocks := scan(text, document, marker)

	type edit struct {
		start, end int
		text       string
	}
	var edits []edit
	replace := func(block existing, body string) {
		edits = append(edits, edit{block.start, block.end, markerLine(marker, body) + "\n" + body})
	}

	// Imports: the tool's own import of a path names everything it needs
	// and everything it named before, so stories written against it keep
	// resolving; names another import of the path already brings in are
	// left out.
	var newImports []string
	for _, wanted := range desired.Imports {
		imported := make(map[string]bool)
		var own *existing
		for index, block := range blocks {
			if block.kind != "import" || block.key != wanted.Path {
				continue
			}
			if block.marked && block.intact && own == nil {
				own = &blocks[index]
				continue
			}
			for _, name := range block.names {
				imported[name] = true
			}
		}
		names := make(map[string]bool)
		for _, name := range wanted.Names {
			if !imported[name] {
				names[name] = true
			}
		}
		if own != nil {
			for _, name := range own.names {
				if !imported[name] {
					names[name] = true
				}
			}
		}
		if len(names) == 0 {
			continue
		}
		body := importText(wanted.Path, names)
		if own != nil {
			if text[own.start:own.end] != markerLine(marker, body)+"\n"+body {
				replace(*own, body)
			}
			continue
		}
		newImports = append(newImports, markerLine(marker, body)+"\n"+body)
	}

	// Fixtures: added once, updated while intact, never removed, since a
	// hand-written story may use one.
	var newFixtures []string
	for _, fixture := range desired.Fixtures {
		body, err := FixtureText(fixture)
		if err != nil {
			return nil, report, err
		}
		found := false
		for _, block := range blocks {
			if block.kind != "fixture" || block.key != fixture.Name {
				continue
			}
			found = true
			if block.marked && block.intact && text[block.start:block.end] != markerLine(marker, body)+"\n"+body {
				replace(block, body)
			}
		}
		if !found {
			newFixtures = append(newFixtures, markerLine(marker, body)+"\n"+body)
		}
	}

	// Stories.
	wanted := make(map[string]bool, len(desired.Stories))
	var newStories []string
	for _, story := range desired.Stories {
		wanted[story.Key] = true
		body := strings.Join(append(commentLines(story.Comments), story.Text), "\n")
		var match *existing
		for index, block := range blocks {
			if block.kind == "story" && block.key == story.Key {
				match = &blocks[index]
				break
			}
		}
		switch {
		case match == nil:
			newStories = append(newStories, markerLine(marker, body)+"\n"+body)
			report.Added = append(report.Added, story.Key)
		case !match.marked:
			report.Own = append(report.Own, story.Key)
		case !match.intact:
			report.Edited = append(report.Edited, story.Key)
		case text[match.start:match.end] == markerLine(marker, body)+"\n"+body:
			report.Unchanged = append(report.Unchanged, story.Key)
		default:
			replace(*match, body)
			report.Updated = append(report.Updated, story.Key)
		}
	}
	for _, block := range blocks {
		if block.kind == "story" && block.marked && !wanted[block.key] && !desired.Accumulate {
			if block.intact {
				// The blank lines left behind are collapsed below.
				edits = append(edits, edit{block.start, block.end, ""})
				report.Removed = append(report.Removed, block.key)
			} else {
				report.Edited = append(report.Edited, block.key)
			}
		}
	}

	// New imports go after the last import, new fixtures after the last
	// import or fixture, and new stories at the end.
	importAt, fixtureAt := headerEnd(text), -1
	for _, block := range blocks {
		switch block.kind {
		case "import":
			importAt = max(importAt, block.end)
			fixtureAt = max(fixtureAt, block.end)
		case "fixture":
			fixtureAt = max(fixtureAt, block.end)
		}
	}
	if fixtureAt < 0 {
		fixtureAt = importAt
	}
	if len(newImports) > 0 {
		edits = append(edits, edit{importAt, importAt, separated(importAt, strings.Join(newImports, "\n\n"))})
	}
	if len(newFixtures) > 0 {
		edits = append(edits, edit{fixtureAt, fixtureAt, separated(fixtureAt, strings.Join(newFixtures, "\n\n"))})
	}
	if len(newStories) > 0 {
		end := len(strings.TrimRight(text, "\n"))
		edits = append(edits, edit{end, end, separated(end, strings.Join(newStories, "\n\n"))})
	}

	sort.SliceStable(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
	var out strings.Builder
	at := 0
	for _, e := range edits {
		out.WriteString(text[at:e.start])
		out.WriteString(e.text)
		at = max(at, e.end)
	}
	out.WriteString(text[at:])
	result := strings.TrimRight(out.String(), "\n") + "\n"
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}
	return []byte(result), report, nil
}

// separated is an insertion at offset, with a blank line between it and
// the text before it.
func separated(offset int, insertion string) string {
	if offset == 0 {
		return insertion + "\n\n"
	}
	return "\n\n" + insertion
}

func newDocument(header []string) string {
	if len(header) == 0 {
		return ""
	}
	return strings.Join(commentLines(header), "\n") + "\n"
}

func commentLines(lines []string) []string {
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			result = append(result, "//")
			continue
		}
		result = append(result, "// "+line)
	}
	return result
}

func importText(path string, names map[string]bool) string {
	list := make([]string, 0, len(names))
	for name := range names {
		list = append(list, name)
	}
	sort.Strings(list)
	return fmt.Sprintf("import { %s } from %q", strings.Join(list, ", "), path)
}

// headerEnd is where new imports go in a Document without any: after its
// header comment, the leading comment lines followed by a blank line, or at
// its start.
func headerEnd(text string) int {
	at := 0
	for at < len(text) {
		next := strings.IndexByte(text[at:], '\n')
		if next < 0 {
			next = len(text) - at
		}
		line := strings.TrimSpace(text[at : at+next])
		if !strings.HasPrefix(line, "//") {
			break
		}
		at += next + 1
	}
	if at == 0 {
		return 0
	}
	if at >= len(text) || text[at] == '\n' {
		return at - 1
	}
	return 0
}

func lineStart(text string, offset int) int {
	if offset > len(text) {
		offset = len(text)
	}
	return strings.LastIndexByte(text[:offset], '\n') + 1
}

func lineColumn(text string, offset int) (int, int) {
	offset = min(max(offset, 0), len(text))
	line := strings.Count(text[:offset], "\n") + 1
	return line, offset - lineStart(text, offset) + 1
}

// declarationEnd is where a declaration's own text ends: the parser's end
// may include the blank and comment lines after it.
func declarationEnd(text string, start, end int) int {
	for {
		trimmed := strings.TrimRight(text[start:end], " \t\r\n")
		end = start + len(trimmed)
		lastLine := strings.LastIndexByte(trimmed, '\n')
		if lastLine < 0 || !strings.HasPrefix(strings.TrimSpace(trimmed[lastLine+1:]), "//") {
			return end
		}
		end = start + lastLine
	}
}

// scan finds a Document's top-level declarations and their marker blocks.
func scan(text string, document *ast.Document, marker string) []existing {
	var blocks []existing
	prefix := "// " + marker + " "
	for _, node := range document.Nodes {
		end := declarationEnd(text, node.Pos(), node.End())
		block := existing{start: node.Pos(), end: end}
		switch declaration := node.(type) {
		case *ast.ImportStmt:
			block.kind, block.key = "import", declaration.Path
			if unquoted, err := strconv.Unquote(declaration.Path); err == nil {
				block.key = unquoted
			}
			for _, specifier := range declaration.Specifiers {
				block.names = append(block.names, specifier.LocalName())
			}
			if len(declaration.Specifiers) == 0 {
				block.names = append(block.names, declaration.Items...)
			}
		case *ast.FixtureDecl:
			block.kind, block.key = "fixture", declaration.Name
		case *ast.StoryDecl:
			block.kind, block.key = "story", declaration.Component+"."+declaration.Name
		default:
			continue
		}
		// The comment lines directly above a declaration; the marker opens
		// the block when it is one of them, and the block's hash covers
		// everything after the marker line.
		current := lineStart(text, node.Pos())
		for current > 0 {
			previous := lineStart(text, current-1)
			line := strings.TrimSpace(text[previous : current-1])
			if !strings.HasPrefix(line, "//") {
				break
			}
			if strings.HasPrefix(line, prefix) {
				block.marked = true
				block.start = previous
				block.intact = strings.TrimPrefix(line, prefix) == blockHash(text[current:end])
				break
			}
			current = previous
		}
		blocks = append(blocks, block)
	}
	return blocks
}
