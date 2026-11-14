package compiler

import (
	"fmt"
	"strings"

	"github.com/karl-tye/ktml/internal/ast"
)

// rawTextElements are the HTML elements whose content the browser parses as
// raw text, up to the matching end tag: character references are not
// decoded and markup is not parsed, so HTML escaping changes the content
// instead of protecting it.
var rawTextElements = map[string]bool{
	"script": true, "style": true,
	"xmp": true, "iframe": true, "noembed": true, "noframes": true, "plaintext": true,
}

// RawTextElement reports whether an HTML element name is a raw-text element.
// Names match case-insensitively, as in HTML.
func RawTextElement(name string) bool {
	return rawTextElements[strings.ToLower(name)]
}

// RawTextBodyElement reports whether an element accepts a raw-text body:
// script and style. The other raw-text elements accept no content.
func RawTextBodyElement(name string) bool {
	name = strings.ToLower(name)
	return name == "script" || name == "style"
}

// RawTextEndTag returns the first sequence in static text that would end or
// corrupt the raw-text content of a script or style element, or "". HTML ends
// both at their end tag, matched case-insensitively; inside a script, <!--
// can start an escaped state in which the end tag no longer ends it.
func RawTextEndTag(element, text string) string {
	lower := strings.ToLower(text)
	element = strings.ToLower(element)
	if index := strings.Index(lower, "</"+element); index >= 0 {
		return text[index : index+2+len(element)]
	}
	if element == "script" && strings.Contains(lower, "<!--") {
		return "<!--"
	}
	return ""
}

// validateRawTextBody checks the children of a raw-text element. script and
// style accept raw-text literals, written byte for byte. Every other child,
// and every child of the other raw-text elements, is an error: HTML escaping
// would turn it into broken JavaScript or CSS.
func (s *Snapshot) validateRawTextBody(path string, caller pathScope, element *ast.ElementNode) {
	for _, child := range element.Children {
		if text, ok := child.(*ast.TextNode); ok && strings.EqualFold(element.Name, "script") {
			s.resolveScriptBody(path, caller, element.Name, text)
			continue
		}
		if raw, ok := child.(*ast.RawTextNode); ok && RawTextBodyElement(element.Name) {
			if end := RawTextEndTag(element.Name, raw.Text); end != "" {
				s.addDiagnostic(path, "raw-text-end-tag", fmt.Sprintf("%s content cannot contain %s, which would end the element early; move it to a static file or build it in Go", element.Name, end), raw.P, raw.E)
			}
			continue
		}
		s.addDiagnostic(path, "raw-text-body", rawTextBodyMessage(element.Name), child.Pos(), child.End())
	}
}

func rawTextBodyMessage(element string) string {
	if RawTextBodyElement(element) {
		return fmt.Sprintf("%s content is raw text: write it as a ``` raw-text literal ```, or load it from a static file", element)
	}
	return fmt.Sprintf("%s content is raw text, which KTML does not emit", element)
}

// resolveScriptBody checks the quoted text of a script element: static
// JavaScript, written byte for byte, with JSONValue, boolean, and
// TrustedScript paths in expression positions. The static text cannot end
// the element, and a TrustedScript path cannot complete </script or <!--
// with the text around it.
func (s *Snapshot) resolveScriptBody(path string, caller pathScope, element string, text *ast.TextNode) {
	quoted := text.Quoted
	if quoted == nil {
		return
	}
	for _, segment := range quoted.Segments {
		if segment.Kind != ast.LiteralSegment {
			continue
		}
		if end := RawTextEndTag(element, segment.Literal); end != "" {
			s.addDiagnostic(path, "raw-text-end-tag", fmt.Sprintf("%s content cannot contain %s, which would end the element early", element, end), segment.P, segment.E)
		}
	}
	s.scriptPositions = scriptPositions(quoted)
	s.trustedScripts = make(map[*ast.PathExpression]bool)
	s.resolveQuotedValue(path, caller, quoted, scriptBodyContext, element)
	trusted := s.trustedScripts
	s.scriptPositions, s.trustedScripts = nil, nil
	for index, segment := range quoted.Segments {
		if segment.Kind != ast.PathSegment {
			continue
		}
		if !trusted[segment.Path] {
			continue
		}
		before, after := "", ""
		if index > 0 && quoted.Segments[index-1].Kind == ast.LiteralSegment {
			before = quoted.Segments[index-1].Literal
		}
		if index+1 < len(quoted.Segments) && quoted.Segments[index+1].Kind == ast.LiteralSegment {
			after = quoted.Segments[index+1].Literal
		}
		if strings.HasSuffix(before, "<") || strings.HasPrefix(after, "/") || strings.HasPrefix(after, "!") || strings.HasPrefix(after, "-") {
			s.addDiagnostic(path, "script-body-adjacency", fmt.Sprintf("TrustedScript {%s} directly touches <, /, !, or - in the script, which could complete </script or <!--; separate them with a space", segment.Path.String()), segment.P, segment.E)
		}
	}
}
