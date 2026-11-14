package storygen

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/formatter"
	"github.com/karl-tye/ktml/internal/parser"
)

// ValueText spells a typed story value in story syntax.
func ValueText(value compiler.Value) string {
	var b strings.Builder
	writeValue(&b, value)
	return b.String()
}

func writeValue(b *strings.Builder, value compiler.Value) {
	switch value.Kind {
	case compiler.NoneValueKind:
		b.WriteString("None")
	case compiler.FixtureValueKind:
		b.WriteString(value.Fixture)
	case compiler.AttributesValueKind:
		b.WriteString("[]")
	case compiler.ScalarValueKind:
		b.WriteString(scalarText(value))
	case compiler.RecordValueKind:
		writeFields(b, value.Fields)
	case compiler.CollectionValueKind:
		b.WriteByte('[')
		for index, item := range value.Items {
			if index > 0 {
				b.WriteString(", ")
			}
			writeValue(b, item)
		}
		b.WriteByte(']')
	case compiler.VariantValueKind:
		b.WriteString(value.Variant)
		if value.Fields != nil {
			b.WriteByte(' ')
			writeFields(b, value.Fields)
		}
	}
}

func writeFields(b *strings.Builder, fields []compiler.FieldValue) {
	b.WriteByte('[')
	for index, field := range fields {
		if index > 0 {
			b.WriteString(", ")
		}
		b.WriteString(field.Name)
		b.WriteString(" = ")
		writeValue(b, field.Value)
	}
	b.WriteByte(']')
}

func scalarText(value compiler.Value) string {
	if value.Type.Kind == compiler.ContextualTypeKind {
		return quote(value.String)
	}
	switch value.Type.Scalar {
	case compiler.BooleanType:
		return strconv.FormatBool(value.Boolean)
	case compiler.IntegerType:
		// KTML has no negative literals; a story shows the magnitude.
		if value.Integer < 0 {
			if value.Integer == math.MinInt64 {
				return strconv.FormatInt(math.MaxInt64, 10)
			}
			return strconv.FormatInt(-value.Integer, 10)
		}
		return strconv.FormatInt(value.Integer, 10)
	case compiler.NumberType:
		number := math.Abs(value.Number)
		if math.IsNaN(number) || math.IsInf(number, 0) {
			number = 0
		}
		text := strconv.FormatFloat(number, 'f', -1, 64)
		if !strings.Contains(text, ".") {
			text += ".0"
		}
		return text
	default:
		return quote(value.String)
	}
}

// quote writes a string literal. A raw backtick literal is used when it is
// exact and the text has quotes or braces; otherwise a double-quoted
// literal escapes what KTML strings cannot hold, braces included, which a
// double-quoted literal would read as interpolation.
func quote(text string) string {
	raw := strings.ContainsAny(text, "\"{}") && !strings.Contains(text, "`")
	for _, r := range text {
		if r < 0x20 || r == 0x7f || r == utf8.RuneError {
			raw = false
		}
	}
	if raw {
		return "`" + text + "`"
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range text {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '{', '}':
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04x`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// StoryText is a story declaration in the formatter's layout, without a
// trailing newline.
func StoryText(story Story) (string, error) {
	var b strings.Builder
	b.WriteString("story ")
	b.WriteString(story.Component.Name)
	b.WriteByte('.')
	b.WriteString(story.Name)
	if len(story.Inputs) > 0 {
		b.WriteString(" [")
		for index, input := range story.Inputs {
			if index > 0 {
				b.WriteString(", ")
			}
			b.WriteString(input.Name)
			b.WriteString(" = ")
			writeValue(&b, input.Value)
		}
		b.WriteByte(']')
	}
	if len(story.Fills) > 0 {
		b.WriteString(" {\n")
		for _, fill := range story.Fills {
			if fill.Slot == "" {
				b.WriteString(quote(fill.Text))
				b.WriteByte('\n')
			}
		}
		for _, fill := range story.Fills {
			if fill.Slot != "" {
				fmt.Fprintf(&b, "slot %s {\n%s\n}\n", fill.Slot, quote(fill.Text))
			}
		}
		b.WriteString("}")
	}
	return formatDeclaration(b.String())
}

// FixtureText is a fixture declaration.
func FixtureText(fixture FixtureDecl) (string, error) {
	return formatDeclaration(fmt.Sprintf("fixture %s: %s = %s", fixture.Name, fixture.Type, quote(fixture.Value)))
}

// formatDeclaration parses one declaration and prints it as ktml fmt does.
func formatDeclaration(source string) (string, error) {
	p := parser.New([]byte(source))
	document := p.ParseDocument()
	if errs := p.Errors(); len(errs) > 0 {
		return "", fmt.Errorf("storygen: generated declaration does not parse: %s\n%s", errs[0].Error(), source)
	}
	if len(document.Nodes) != 1 {
		return "", fmt.Errorf("storygen: generated text is not one declaration:\n%s", source)
	}
	return strings.TrimRight(formatter.Format(&ast.Document{Nodes: document.Nodes}), "\n"), nil
}
