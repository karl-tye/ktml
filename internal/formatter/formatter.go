package formatter

import (
	"strings"

	"github.com/karl-tye/ktml/internal/ast"
)

type Formatter struct {
	b           strings.Builder
	indentLevel int
}

func Format(doc *ast.Document) string {
	f := &Formatter{}
	f.visitDocument(doc)
	return f.b.String()
}

func (f *Formatter) writeIndent() {
	for i := 0; i < f.indentLevel; i++ {
		f.b.WriteByte('\t')
	}
}

func (f *Formatter) visitDocument(doc *ast.Document) {
	if len(doc.Nodes) == 0 {

		return
	}
	for i, node := range doc.Nodes {
		f.visitNode(node)
		if i < len(doc.Nodes)-1 {
			f.b.WriteString("\n\n")
		}
	}
	f.b.WriteString("\n")
}

func (f *Formatter) visitNode(node ast.Node) {
	switch n := node.(type) {
	case *ast.ElementDecl:
		f.writeIndent()
		f.b.WriteString(n.Name)
		if len(n.Attributes) > 0 {
			var longestNameLen int
			var totalLen int
			for _, attr := range n.Attributes {
				nameLen := len(attr.Name)
				if attr.Mode == ast.OptionalInput {
					nameLen++
				}
				if nameLen > longestNameLen {
					longestNameLen = nameLen
				}
				defaultValue, hasDefault := declarationDefault(attr)
				totalLen += len(attr.Name) + len(attr.TypeDecl) + len(defaultValue) + 2
				if attr.Mode == ast.OptionalInput {
					totalLen++
				}
				if hasDefault {
					totalLen += 3
				}
			}
			totalLen += (len(n.Attributes) - 1) * 2

			isMultiLine := len(n.Attributes) > 2 || totalLen > 60

			if !isMultiLine {
				f.b.WriteString(" [")
				for i, attr := range n.Attributes {
					f.b.WriteString(attr.Name)
					if attr.Mode == ast.OptionalInput {
						f.b.WriteByte('?')
					}
					f.b.WriteString(": ")
					f.b.WriteString(attr.TypeDecl)
					if defaultValue, ok := declarationDefault(attr); ok {
						f.b.WriteString(" = ")
						f.b.WriteString(defaultValue)
					}
					if i < len(n.Attributes)-1 {
						f.b.WriteString(", ")
					}
				}
				f.b.WriteString("]")
			} else {
				f.b.WriteString(" [\n")
				f.indentLevel++
				for _, attr := range n.Attributes {
					f.writeIndent()
					f.b.WriteString(attr.Name)
					if attr.Mode == ast.OptionalInput {
						f.b.WriteByte('?')
					}
					f.b.WriteString(":")
					nameLen := len(attr.Name)
					if attr.Mode == ast.OptionalInput {
						nameLen++
					}
					pad := longestNameLen - nameLen + 1
					for range pad {
						f.b.WriteByte(' ')
					}
					f.b.WriteString(attr.TypeDecl)
					if defaultValue, ok := declarationDefault(attr); ok {
						f.b.WriteString(" = ")
						f.b.WriteString(defaultValue)
					}
					f.b.WriteString(",\n")
				}
				f.indentLevel--
				f.writeIndent()
				f.b.WriteString("]")
			}
		} else if n.IsSignature() {
			f.b.WriteString(" []")
		}
		if n.Bodyless {
			return
		}
		if n.DefaultRange != nil {
			f.b.WriteString(" default")
		}
		f.writeBody(n.Body)
	case *ast.RenderClause:
		f.writeIndent()
		f.b.WriteString(n.Name)
		f.b.WriteString(" [")
		f.b.WriteString(n.Selector)
		f.b.WriteString(" = ")
		f.b.WriteString(n.Pattern.String())
		for _, binding := range n.Bindings {
			f.b.WriteString(", ")
			f.b.WriteString(binding.Field)
			if binding.Alias != "" {
				f.b.WriteString(" as ")
				f.b.WriteString(binding.Alias)
			}
		}
		f.b.WriteString("]")
		f.writeBody(n.Body)
	case *ast.StateDecl:
		f.writeIndent()
		f.b.WriteString("state ")
		f.b.WriteString(n.Name)
		if len(n.Variants) == 0 {
			f.b.WriteString(" {}")
			return
		}
		f.b.WriteString(" {\n")
		f.indentLevel++
		for _, variant := range n.Variants {
			f.writeIndent()
			f.b.WriteString(variant.Name)
			if variant.Payload {
				f.writeFields(variant.Fields)
			}
			f.b.WriteString(",\n")
		}
		f.indentLevel--
		f.writeIndent()
		f.b.WriteString("}")
	case *ast.ElementNode:
		f.writeIndent()
		f.b.WriteString(n.Name)
		inline := inlineAttributes(n.Attributes)
		width := f.indentLevel*indentWidth + len(n.Name) + len(inline)
		if needsGroup(n.Attributes) {
			width += len("()")
		}
		if len(n.Children) > 0 {
			width += len(" {")
		}
		if width > maxHeaderWidth && len(n.Attributes) > 0 {
			f.b.WriteString(" (\n")
			f.indentLevel++
			for _, attr := range n.Attributes {
				f.writeIndent()
				f.b.WriteString(attributeSpelling(attr))
				f.b.WriteString("\n")
			}
			f.indentLevel--
			f.writeIndent()
			f.b.WriteString(")")
		} else if needsGroup(n.Attributes) {
			f.b.WriteString(" (" + strings.TrimPrefix(inline, " ") + ")")
		} else {
			f.b.WriteString(inline)
		}
		if len(n.Children) > 0 {
			f.writeBody(n.Children)
		}
	case *ast.SlotNode:
		f.writeIndent()
		f.b.WriteString("slot ")
		f.b.WriteString(n.Name)
		f.writeBlock(n.Body)
	case *ast.ConditionNode:
		f.writeIndent()
		f.writeCondition(n)
	case *ast.RepetitionNode:
		f.writeIndent()
		f.b.WriteString("for ")
		f.b.WriteString(n.Item)
		f.b.WriteString(" in ")
		f.b.WriteString(n.Collection.String())
		f.b.WriteString(" key ")
		f.b.WriteString(n.Key.String())
		f.writeBlock(n.Body)
	case *ast.TextNode:
		f.writeIndent()
		f.b.WriteString(n.Value)
	case *ast.DoctypeNode:
		f.writeIndent()
		f.b.WriteString("doctype ")
		f.b.WriteString(n.Name)
	case *ast.RawTextNode:
		// The text is output, so it is preserved byte for byte.
		f.writeIndent()
		f.b.WriteString("```")
		f.b.WriteString(n.Text)
		f.b.WriteString("```")
	case *ast.HTMLCommentNode:
		// The text is output, so it is preserved byte for byte.
		f.writeIndent()
		f.b.WriteString("<!--")
		f.b.WriteString(n.Text)
		f.b.WriteString("-->")
	case *ast.ImportStmt:
		f.writeIndent()
		f.b.WriteString("import { ")
		names := n.Items
		if len(n.Specifiers) == len(n.Items) && len(n.Specifiers) > 0 {
			names = make([]string, len(n.Specifiers))
			for i, specifier := range n.Specifiers {
				names[i] = specifier.Name
				if specifier.Alias != "" {
					names[i] += " as " + specifier.Alias
				}
			}
		}
		f.b.WriteString(strings.Join(names, ", "))
		f.b.WriteString(" } from ")
		f.b.WriteString(n.Path)
	case *ast.RecordDecl:
		f.writeIndent()
		f.b.WriteString("record ")
		f.b.WriteString(n.Name)
		f.writeFields(n.Fields)
	case *ast.FixtureDecl:
		f.writeIndent()
		f.b.WriteString("fixture " + n.Name + ": " + n.TypeDecl + " = ")
		if n.Value != nil {
			f.b.WriteString(n.Value.Raw)
		}
	case *ast.StoryDecl:
		f.writeStory(n)
	}
}

// Element headers wider than maxHeaderWidth columns, counting each indentation
// level as indentWidth columns, use the parenthesized continuation form.
const (
	maxHeaderWidth = 100
	indentWidth    = 4
)

// inlineAttributes returns the same-line spelling of an attribute list,
// including its leading space.
func inlineAttributes(attributes []*ast.AttributeNode) string {
	var b strings.Builder
	for _, attr := range attributes {
		if !strings.HasPrefix(attr.Name, "#") && !strings.HasPrefix(attr.Name, ".") {
			b.WriteString(" ")
		}
		b.WriteString(attributeSpelling(attr))
	}
	return b.String()
}

func attributeSpelling(attr *ast.AttributeNode) string {
	if attr.Condition != nil {
		return conditionSpelling(attr.Condition)
	}
	if attr.Spread != nil {
		return "..." + attr.Spread.Path.String()
	}
	if attr.BagRange != nil {
		parts := make([]string, len(attr.Bag))
		for i, entry := range attr.Bag {
			parts[i] = attributeSpelling(entry)
		}
		return attr.Name + "=(" + strings.Join(parts, " ") + ")"
	}
	if attr.Value == "" || strings.HasPrefix(attr.Name, "#") || strings.HasPrefix(attr.Name, ".") {
		return attr.Name
	}
	return attr.Name + "=" + attr.Value
}

// conditionSpelling writes a conditional attribute group on one line.
func conditionSpelling(group *ast.AttributeCondition) string {
	var b strings.Builder
	b.WriteString("if ")
	b.WriteString(group.Condition.String())
	b.WriteString(attributeBlockSpelling(group.Then))
	if group.ElseKeywordRange == nil && len(group.Else) == 0 {
		return b.String()
	}
	b.WriteString(" else ")
	if group.ElseIf && len(group.Else) == 1 && group.Else[0].Condition != nil {
		b.WriteString(conditionSpelling(group.Else[0].Condition))
		return b.String()
	}
	b.WriteString(strings.TrimPrefix(attributeBlockSpelling(group.Else), " "))
	return b.String()
}

func attributeBlockSpelling(entries []*ast.AttributeNode) string {
	if len(entries) == 0 {
		return " {}"
	}
	parts := make([]string, len(entries))
	for i, entry := range entries {
		parts[i] = attributeSpelling(entry)
	}
	return " { " + strings.Join(parts, " ") + " }"
}

// needsGroup reports whether an attribute list can only be written as a
// parenthesized group: conditional attributes are not header-line syntax.
func needsGroup(attributes []*ast.AttributeNode) bool {
	for _, attr := range attributes {
		if attr.Condition != nil {
			return true
		}
	}
	return false
}

// writeCondition writes if condition { … } with its else or else if chain.
func (f *Formatter) writeCondition(n *ast.ConditionNode) {
	f.b.WriteString("if ")
	f.b.WriteString(n.Condition.String())
	f.writeBlock(n.Then)
	if n.ElseKeywordRange == nil && len(n.Else) == 0 {
		return
	}
	f.b.WriteString(" else")
	if nested, ok := elseIf(n); ok {
		f.b.WriteString(" ")
		f.writeCondition(nested)
		return
	}
	f.writeBlock(n.Else)
}

func elseIf(n *ast.ConditionNode) (*ast.ConditionNode, bool) {
	if !n.ElseIf || len(n.Else) != 1 {
		return nil, false
	}
	nested, ok := n.Else[0].(*ast.ConditionNode)
	return nested, ok
}

// writeBlock writes a braced body that is {} when empty.
func (f *Formatter) writeBlock(body []ast.Node) {
	if len(body) == 0 {
		f.b.WriteString(" {}")
		return
	}
	f.writeBody(body)
}

// writeBody writes a braced declaration body.
func (f *Formatter) writeBody(body []ast.Node) {
	f.b.WriteString(" {\n")
	f.indentLevel++
	for i, child := range body {
		f.visitNode(child)
		if i < len(body)-1 {
			f.b.WriteString("\n")
		}
	}
	f.indentLevel--
	f.b.WriteString("\n")
	f.writeIndent()
	f.b.WriteString("}")
}

// writeFields writes a record or payload field list, one field per line.
func (f *Formatter) writeFields(fields []*ast.FieldDecl) {
	if len(fields) == 0 {
		f.b.WriteString(" []")
		return
	}
	f.b.WriteString(" [\n")
	f.indentLevel++
	for _, field := range fields {
		f.writeIndent()
		f.b.WriteString(field.Name)
		if field.Optional {
			f.b.WriteByte('?')
		}
		f.b.WriteString(": ")
		f.b.WriteString(field.TypeDecl)
		f.b.WriteString(",\n")
	}
	f.indentLevel--
	f.writeIndent()
	f.b.WriteString("]")
}

func declarationDefault(attribute *ast.AttributeDecl) (string, bool) {
	if attribute.Default != nil {
		return attribute.Default.Raw, true
	}
	// Hand-built prototype ASTs predate explicit declaration modes.
	return attribute.DefaultValue, attribute.DefaultValue != ""
}

// writeStory writes a story with one input per line; values are written
// inline.
func (f *Formatter) writeStory(n *ast.StoryDecl) {
	f.writeIndent()
	f.b.WriteString("story " + n.Component + "." + n.Name)
	if n.ArgumentsRange != nil {
		if len(n.Arguments) == 0 {
			f.b.WriteString(" []")
		} else {
			f.b.WriteString(" [\n")
			f.indentLevel++
			for _, argument := range n.Arguments {
				f.writeIndent()
				f.b.WriteString(argument.Name + " = " + valueSpelling(argument.Value) + ",\n")
			}
			f.indentLevel--
			f.writeIndent()
			f.b.WriteString("]")
		}
	}
	if n.BodyRange != nil {
		f.writeBlock(n.Body)
	}
}

// valueSpelling is the canonical inline spelling of a story value.
func valueSpelling(value *ast.ValueExpr) string {
	if value == nil {
		return ""
	}
	switch value.Kind {
	case ast.ScalarValue:
		return value.Scalar.Raw
	case ast.NameValue:
		if value.PayloadRange == nil {
			return value.Name
		}
		return value.Name + " " + argumentsSpelling(value.Fields)
	case ast.FieldsValue:
		return argumentsSpelling(value.Fields)
	case ast.ListValue:
		items := make([]string, len(value.Items))
		for i, item := range value.Items {
			items[i] = valueSpelling(item)
		}
		return "[" + strings.Join(items, ", ") + "]"
	}
	return "[]"
}

func argumentsSpelling(arguments []*ast.ValueArgument) string {
	entries := make([]string, len(arguments))
	for i, argument := range arguments {
		entries[i] = argument.Name + " = " + valueSpelling(argument.Value)
	}
	return "[" + strings.Join(entries, ", ") + "]"
}
