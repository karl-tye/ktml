package gogen

import (
	"bytes"
	"fmt"
	"go/format"
	"go/scanner"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/karl-tye/ktml/internal/compiler"
)

// SourceMapping maps one generated Go token back to the KTML source range it
// was generated from.
type SourceMapping struct {
	// Go is the byte offset of the mapped token in the generated file.
	Go int
	// Source is the KTML or Host Schema range: a declaration name, an input,
	// a call, or a path.
	Source compiler.SourceRange
}

// Generated Go carries its source map as standard line directives
// (https://pkg.go.dev/cmd/compile#hdr-Compiler_Directives), so go build,
// go vet, and gopls type errors in generated files name .ktml and
// .ktmlschema positions:
//
//	if err := rt.Text(w, /*line ui.ktml:12:31*/ props.View.Month); err != nil { /*line ui_ktml.go:40:1*/
//
// A mapped token is preceded by /*line source:line:column*/ and a space, so
// the directive names the column before it. Every line that maps a token
// ends with a directive that restores the generated file's own line numbers,
// so the following lines keep their real positions. Directives are inserted
// after gofmt as inline comments, which gofmt leaves in place, and the
// result is formatted again until it is stable.

// markerPrefix opens the placeholder comment the generator writes before a
// mapped token; it becomes a line directive once the file is formatted.
const markerPrefix = "/*ktml:"

// mark returns the placeholder that maps the Go token written after it to
// source. Sources without a file, as in snapshot-free generation, map
// nothing.
func (g *generator) mark(source compiler.SourceRange) string {
	if source.File == "" || g.target == "" {
		return ""
	}
	g.marks = append(g.marks, source)
	return markerPrefix + strconv.Itoa(len(g.marks)-1) + "*/ "
}

// markIn maps a token to a range of the document being generated.
func (g *generator) markIn(start, end int) string {
	return g.mark(compiler.SourceRange{File: g.file, Start: start, End: end})
}

// finish formats generated Go, turns its placeholders into line directives
// relative to the generated file target, and returns the mappings.
func (g *generator) finish(unformatted []byte) ([]byte, []SourceMapping, error) {
	formatted, err := format.Source(unformatted)
	if err != nil {
		return nil, nil, fmt.Errorf("format generated Go: %w\n%s", err, unformatted)
	}
	if g.target == "" || len(g.marks) == 0 {
		return formatted, nil, nil
	}
	// The placeholders in file order; a placeholder that is written twice,
	// such as a key in a duplicate-key error, maps both tokens.
	var sources []compiler.SourceRange
	type placeholder struct{ start, end int }
	var placeholders []placeholder
	scan(formatted, func(offset int, text string) {
		if !strings.HasPrefix(text, markerPrefix) {
			return
		}
		index, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(text, markerPrefix), "*/"))
		if err == nil && index >= 0 && index < len(g.marks) {
			sources = append(sources, g.marks[index])
			placeholders = append(placeholders, placeholder{start: offset, end: offset + len(text)})
		}
	})
	// Replace each placeholder with a directive, numbered by settle, and end
	// each line that holds one with a directive that restores the generated
	// file's line numbers.
	var output bytes.Buffer
	previous := 0
	for position, current := range placeholders {
		output.Write(formatted[previous:current.start])
		output.WriteString(sourcePlaceholder)
		previous = current.end
		lineEnd := bytes.IndexByte(formatted[current.end:], '\n') + current.end
		if position+1 < len(placeholders) && placeholders[position+1].start < lineEnd {
			continue
		}
		output.Write(formatted[previous:lineEnd])
		output.WriteString(" " + restorePlaceholder)
		previous = lineEnd
	}
	output.Write(formatted[previous:])
	result, err := g.settle(output.Bytes(), sources)
	if err != nil {
		return nil, nil, err
	}
	var mappings []SourceMapping
	scan(result, func(offset int, text string) {
		if strings.HasPrefix(text, "/*line ") && !isRestore(text) {
			mappings = append(mappings, SourceMapping{Go: mappedToken(result, offset+len(text)), Source: sources[len(mappings)]})
		}
	})
	return result, mappings, nil
}

// Placeholders for the directives settle numbers.
const (
	sourcePlaceholder  = "/*line ktml-source*/"
	restorePlaceholder = "/*line ktml-restore*/"
)

// settle numbers the directives of content and formats it until it is
// stable. The nth source directive positions the token after it at the
// start of sources[n]; gofmt may leave a space or a comma between the two,
// which the directive's column allows for. A restore directive gives its
// own physical line, so the next line has its real number. Formatting
// changes only spacing, never lines, and renumbering changes only comment
// widths, so this converges at once.
func (g *generator) settle(content []byte, sources []compiler.SourceRange) ([]byte, error) {
	name := filepath.Base(g.target)
	indexes := make(map[string]lineIndex)
	sourceLines := func(file string) lineIndex {
		if index, ok := indexes[file]; ok {
			return index
		}
		var text []byte
		if g.snapshot != nil {
			text = g.snapshot.Source(file)
		}
		index := newLineIndex(text)
		indexes[file] = index
		return index
	}
	for round := range 4 {
		var output bytes.Buffer
		line, previous, position := 1, 0, 0
		scan(content, func(offset int, text string) {
			if !strings.HasPrefix(text, "/*line ") {
				return
			}
			line += bytes.Count(content[previous:offset], []byte("\n"))
			output.Write(content[previous:offset])
			previous = offset + len(text)
			if text == restorePlaceholder || isRestore(text) {
				fmt.Fprintf(&output, "/*line %s:%d:1*/", name, line)
				return
			}
			source := sources[position]
			position++
			sourceLine, column := sourceLines(source.File).lineColumn(source.Start)
			column = max(column-(mappedToken(content, previous)-previous), 1)
			fmt.Fprintf(&output, "/*line %s:%d:%d*/", g.directiveFile(source.File), sourceLine, column)
		})
		output.Write(content[previous:])
		// After the first round content is gofmt output, so unchanged
		// numbering is already stable.
		if round > 0 && bytes.Equal(output.Bytes(), content) {
			return content, nil
		}
		formatted, err := format.Source(output.Bytes())
		if err != nil {
			return nil, fmt.Errorf("format generated Go with line directives: %w", err)
		}
		if bytes.Equal(formatted, content) {
			return formatted, nil
		}
		content = formatted
	}
	return nil, fmt.Errorf("format generated Go with line directives: not stable")
}

// mappedToken returns the offset of the token a directive ending at offset
// maps: gofmt keeps a space after the directive, and moves a directive that
// followed a comma before it.
func mappedToken(content []byte, offset int) int {
	for offset < len(content) && (content[offset] == ' ' || content[offset] == ',') {
		offset++
	}
	return offset
}

// directiveFile is the file name a directive gives for source: relative to
// the generated file's directory, so it does not depend on where the code
// was generated.
func (g *generator) directiveFile(source string) string {
	relative, err := filepath.Rel(filepath.Dir(g.target), source)
	if err != nil {
		return filepath.ToSlash(source)
	}
	return filepath.ToSlash(relative)
}

// isRestore reports whether a directive names a generated Go file.
func isRestore(directive string) bool {
	name, _, _ := strings.Cut(strings.TrimPrefix(directive, "/*line "), ":")
	return strings.HasSuffix(name, ".go")
}

// scan calls visit with every comment of Go source, in order.
func scan(source []byte, visit func(offset int, text string)) {
	files := token.NewFileSet()
	file := files.AddFile("", files.Base(), len(source))
	var s scanner.Scanner
	s.Init(file, source, nil, scanner.ScanComments)
	for {
		position, kind, literal := s.Scan()
		if kind == token.EOF {
			return
		}
		if kind == token.COMMENT {
			visit(file.Offset(position), literal)
		}
	}
}

// StripLineDirectives returns generated Go without its line directives,
// formatted: the code as it reads without the source map, for inspection.
func StripLineDirectives(generated []byte) ([]byte, error) {
	var output bytes.Buffer
	previous := 0
	scan(generated, func(offset int, text string) {
		if !strings.HasPrefix(text, "/*line ") {
			return
		}
		output.Write(generated[previous:offset])
		previous = offset + len(text)
	})
	output.Write(generated[previous:])
	return format.Source(output.Bytes())
}
