package storygen

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/karl-tye/ktml/dev/interp"
	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/parser"
)

// CapturedHeader comments a story Document that capture creates.
var CapturedHeader = []string{
	"Stories captured by `ktml dev --capture-stories` from pages rendered while browsing.",
	"Values are redacted unless the capture ran with --capture-raw. A story below a",
	"`// ktml:captured <hash>` line is kept as captured; edit it freely.",
}

// Capturer writes a story for each new shape of props a running application
// renders. It is safe for concurrent use.
type Capturer struct {
	root, dir string
	// redactor is nil when raw values are kept.
	redactor *Redactor

	mu sync.Mutex
	// seen holds the component and shape of each story captured, read
	// from a Document the first time a capture touches it.
	seen   map[string]bool
	loaded map[string]bool
}

// NewCapturer captures into dir, which must be inside the workspace root.
// A nil redactor keeps raw values.
func NewCapturer(root, dir string, redactor *Redactor) (*Capturer, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	dir = filepath.Clean(dir)
	relative, err := filepath.Rel(root, dir)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("the capture directory %s is outside the workspace %s", dir, root)
	}
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == ".git" || part == ".ktml" || part == "node_modules" {
			return nil, fmt.Errorf("the capture directory %s is inside %s", dir, part)
		}
	}
	return &Capturer{root: root, dir: dir, redactor: redactor, seen: make(map[string]bool), loaded: make(map[string]bool)}, nil
}

// CaptureResult is what one Add did.
type CaptureResult struct {
	// Path and Story are set when a story was written.
	Path  string
	Story string
	// Skipped explains a capture that wrote nothing new.
	Skipped string
}

var shapeComment = regexp.MustCompile(`\(shape ([0-9a-f]{12})\)`)

// Add writes a story for a captured root unless a story of its shape exists.
func (c *Capturer) Add(captured interp.Captured) (CaptureResult, error) {
	component := captured.Component
	if captured.Unsupported != "" {
		return CaptureResult{Skipped: fmt.Sprintf("%s: %s cannot be given by a story", component.Name, captured.Unsupported)}, nil
	}
	values := captured.Values
	if c.redactor != nil {
		values = c.redactor.Values(values)
	}
	content := make(map[string]bool)
	for _, name := range captured.Content {
		content[name] = true
	}
	var fills []Fill
	for _, input := range component.Inputs {
		if input.IsContent() && (content[input.Name] || input.Mode == compiler.RequiredInput) {
			fill := Fill{Slot: input.Name, Text: "Captured " + strings.ToLower(humanize(input.Name))}
			if input.Name == compiler.ChildrenInput {
				fill = Fill{Text: "Captured content"}
			}
			fills = append(fills, fill)
		}
	}
	shape := shapeOf(values, fills)

	c.mu.Lock()
	defer c.mu.Unlock()
	relative, err := filepath.Rel(c.root, component.File)
	if err != nil || strings.HasPrefix(relative, "..") {
		return CaptureResult{Skipped: component.Name + " is outside the workspace"}, nil
	}
	path := StoryPath(filepath.Join(c.dir, relative))
	before, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return CaptureResult{}, err
	}
	if err != nil {
		before = nil
	}
	existing := scanDocument(before)
	if !c.loaded[path] {
		c.loaded[path] = true
		for _, comment := range existing.comments {
			if match := shapeComment.FindStringSubmatch(comment.text); match != nil {
				c.seen[comment.component+"|"+match[1]] = true
			}
		}
	}
	key := component.Name + "|" + shape
	if c.seen[key] {
		return CaptureResult{Skipped: component.Name + " has a story of this shape"}, nil
	}

	fixtures := &capturedFixtures{existing: existing.fixtures}
	story := Story{Component: component, Fills: fills}
	for _, value := range values {
		story.Inputs = append(story.Inputs, InputValue{Name: value.Input, Value: fixtures.assign(value.Value)})
	}
	names := existing.stories[component.Name]
	if names == nil {
		names = make(map[string]bool)
	}
	story.Name = uniqueName(captureName(values), names)
	text, err := StoryText(story)
	if err != nil {
		return CaptureResult{}, err
	}
	mode := "redacted"
	if c.redactor == nil {
		mode = "raw values"
	}
	importPath, err := filepath.Rel(filepath.Dir(path), component.File)
	if err != nil {
		return CaptureResult{}, err
	}
	importPath = filepath.ToSlash(importPath)
	if !strings.HasPrefix(importPath, "../") {
		importPath = "./" + importPath
	}
	desired := Desired{
		Header:     CapturedHeader,
		Imports:    []Import{{Path: importPath, Names: []string{component.Name}}},
		Fixtures:   fixtures.added,
		Stories:    []Block{{Key: component.Name + "." + story.Name, Comments: []string{fmt.Sprintf("Captured from a render of %s, %s (shape %s).", component.Name, mode, shape)}, Text: text}},
		Accumulate: true,
	}
	source, _, err := Merge(before, desired, CapturedMarker)
	if err != nil {
		return CaptureResult{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := writeAtomic(path, source); err != nil {
		return CaptureResult{}, err
	}
	c.seen[key] = true
	return CaptureResult{Path: path, Story: component.Name + "." + story.Name}, nil
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Chmod(0o644); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}

// shapeOf identifies what a story renders differently: the Variants it
// selects, the optional values it gives, and which collections are empty,
// not the text or numbers in them.
func shapeOf(values []compiler.StoryValue, fills []Fill) string {
	var b strings.Builder
	for _, value := range values {
		b.WriteString(value.Input)
		b.WriteByte('=')
		writeShape(&b, value.Value)
		b.WriteByte(';')
	}
	for _, fill := range fills {
		b.WriteString("fill:" + fill.Slot + ";")
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])[:12]
}

func writeShape(b *strings.Builder, value compiler.Value) {
	switch value.Kind {
	case compiler.RecordValueKind:
		b.WriteByte('{')
		for _, field := range value.Fields {
			b.WriteString(field.Name)
			b.WriteByte(':')
			writeShape(b, field.Value)
			b.WriteByte(',')
		}
		b.WriteByte('}')
	case compiler.VariantValueKind:
		b.WriteString(value.Variant)
		if value.Fields != nil {
			writeShape(b, compiler.Value{Kind: compiler.RecordValueKind, Fields: value.Fields})
		}
	case compiler.CollectionValueKind:
		items := make(map[string]bool)
		for _, item := range value.Items {
			var inner strings.Builder
			writeShape(&inner, item)
			items[inner.String()] = true
		}
		list := make([]string, 0, len(items))
		for item := range items {
			list = append(list, item)
		}
		sort.Strings(list)
		b.WriteString("[" + strings.Join(list, "|") + "]")
	case compiler.NoneValueKind:
		b.WriteByte('-')
	default:
		b.WriteByte('v')
	}
}

// captureName names a captured story by the Variants its inputs select.
func captureName(values []compiler.StoryValue) string {
	var names []string
	var visit func(value compiler.Value)
	visit = func(value compiler.Value) {
		switch value.Kind {
		case compiler.VariantValueKind:
			names = append(names, value.Variant)
			for _, field := range value.Fields {
				visit(field.Value)
			}
		case compiler.RecordValueKind:
			for _, field := range value.Fields {
				visit(field.Value)
			}
		}
	}
	for _, value := range values {
		visit(value.Value)
	}
	if len(names) > 2 {
		names = names[:2]
	}
	return "Captured" + strings.Join(names, "")
}

// capturedFixtures gives each trusted value a fixture: an existing one with
// the same type and value, or a new captured<Type><n>.
type capturedFixtures struct {
	existing []FixtureDecl
	added    []FixtureDecl
}

var fixturePrefixes = map[string]string{
	compiler.TrustedURLType:    "capturedURL",
	compiler.JSONValueType:     "capturedJSON",
	compiler.TrustedScriptType: "capturedScript",
	compiler.CSSValueType:      "capturedCSS",
}

func (f *capturedFixtures) assign(value compiler.Value) compiler.Value {
	switch value.Kind {
	case compiler.FixtureValueKind:
		text := value.String
		if value.Type.Name == compiler.JSONValueType && !json.Valid([]byte(text)) {
			text = "null"
		}
		for _, declared := range append(append([]FixtureDecl(nil), f.existing...), f.added...) {
			if declared.Type == value.Type.Name && declared.Value == text {
				value.Fixture = declared.Name
				return value
			}
		}
		used := make(map[string]bool)
		for _, declared := range append(append([]FixtureDecl(nil), f.existing...), f.added...) {
			used[declared.Name] = true
		}
		name := ""
		for suffix := 1; name == "" || used[name]; suffix++ {
			name = fmt.Sprintf("%s%d", fixturePrefixes[value.Type.Name], suffix)
		}
		f.added = append(f.added, FixtureDecl{Name: name, Type: value.Type.Name, Value: text})
		value.Fixture = name
	case compiler.RecordValueKind, compiler.VariantValueKind:
		if value.Fields != nil {
			fields := make([]compiler.FieldValue, len(value.Fields))
			for index, field := range value.Fields {
				fields[index] = compiler.FieldValue{Name: field.Name, Value: f.assign(field.Value)}
			}
			value.Fields = fields
		}
	case compiler.CollectionValueKind:
		items := make([]compiler.Value, len(value.Items))
		for index, item := range value.Items {
			items[index] = f.assign(item)
		}
		value.Items = items
	}
	return value
}

// document is what capture reads from an existing story Document.
type document struct {
	fixtures []FixtureDecl
	// stories holds the story names of each component.
	stories map[string]map[string]bool
	// comments are the comment lines above stories.
	comments []storyComment
}

type storyComment struct {
	component, text string
}

func scanDocument(source []byte) document {
	result := document{stories: make(map[string]map[string]bool)}
	if source == nil {
		return result
	}
	text := string(source)
	parsed := parser.New(source).ParseDocument()
	for _, node := range parsed.Nodes {
		switch declaration := node.(type) {
		case *ast.FixtureDecl:
			value := ""
			if declaration.Value != nil {
				value = declaration.Value.String
			}
			result.fixtures = append(result.fixtures, FixtureDecl{Name: declaration.Name, Type: declaration.TypeDecl, Value: value})
		case *ast.StoryDecl:
			if result.stories[declaration.Component] == nil {
				result.stories[declaration.Component] = make(map[string]bool)
			}
			result.stories[declaration.Component][declaration.Name] = true
			current := lineStart(text, declaration.Pos())
			for current > 0 {
				previous := lineStart(text, current-1)
				line := strings.TrimSpace(text[previous : current-1])
				if !strings.HasPrefix(line, "//") {
					break
				}
				result.comments = append(result.comments, storyComment{component: declaration.Component, text: line})
				current = previous
			}
		}
	}
	return result
}
