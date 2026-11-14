package compiler

import (
	"fmt"
	"strings"

	"github.com/karl-tye/ktml/internal/ast"
)

// validateMarkup checks the doctypes and HTML comments of one component,
// default, or Render Clause body (attribute-names-doctype-comments-v1). A
// doctype is valid only directly in that body, before every node other than
// an HTML comment.
func (s *Snapshot) validateMarkup(path string, body []ast.Node) {
	output := false
	for _, node := range body {
		switch value := node.(type) {
		case *ast.HTMLCommentNode:
			s.validateHTMLComment(path, value)
			continue
		case *ast.DoctypeNode:
			if output {
				s.misplacedDoctype(path, value)
			} else if value.Name != "html" {
				s.addDiagnostic(path, "invalid-doctype", fmt.Sprintf("doctype %s is not supported; HTML output uses doctype html", value.Name), value.NameRange.Start, value.NameRange.End)
			}
		default:
			s.validateElement(path, node)
			s.validateNestedMarkup(path, nestedBodies(node)...)
		}
		output = true
	}
}

// validateNestedMarkup checks bodies below the top level, where every
// doctype is misplaced.
func (s *Snapshot) validateNestedMarkup(path string, bodies ...[]ast.Node) {
	for _, body := range bodies {
		for _, node := range body {
			switch value := node.(type) {
			case *ast.HTMLCommentNode:
				s.validateHTMLComment(path, value)
			case *ast.DoctypeNode:
				s.misplacedDoctype(path, value)
			default:
				s.validateElement(path, node)
				s.validateNestedMarkup(path, nestedBodies(node)...)
			}
		}
	}
}

// validateElement reports content in a void element, which HTML cannot
// represent: the browser ends the element at its start tag.
func (s *Snapshot) validateElement(path string, node ast.Node) {
	element, ok := node.(*ast.ElementNode)
	if !ok || !VoidElement(element.Name) || len(element.Children) == 0 {
		return
	}
	s.addDiagnostic(path, "void-content", fmt.Sprintf("%s is a void element and cannot have content; write the content after it", element.Name), element.P, element.P+len(element.Name))
}

// VoidElement reports whether an HTML element name is a void element, which
// has a start tag only.
func VoidElement(name string) bool {
	switch name {
	case "area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr":
		return true
	default:
		return false
	}
}

func (s *Snapshot) misplacedDoctype(path string, doctype *ast.DoctypeNode) {
	s.addDiagnostic(path, "misplaced-doctype", "doctype must be the first node of a component, default, or Render Clause body; only HTML comments may precede it", doctype.P, doctype.E)
}

// validateHTMLComment applies the HTML comment text rules, so the comment
// renders as one well-formed comment.
func (s *Snapshot) validateHTMLComment(path string, comment *ast.HTMLCommentNode) {
	var problem string
	switch text := comment.Text; {
	case strings.HasPrefix(text, ">"), strings.HasPrefix(text, "->"):
		problem = "must not start with > or ->"
	case strings.Contains(text, "<!--"):
		problem = "must not contain <!--"
	case strings.Contains(text, "--!>"):
		problem = "must not contain --!>"
	case strings.HasSuffix(text, "<!-"):
		problem = "must not end with <!-"
	default:
		return
	}
	s.addDiagnostic(path, "invalid-html-comment", "HTML comment text "+problem, comment.P, comment.E)
}

// nestedBodies returns the bodies directly inside a body node.
func nestedBodies(node ast.Node) [][]ast.Node {
	switch value := node.(type) {
	case *ast.ElementNode:
		return [][]ast.Node{value.Children}
	case *ast.RepetitionNode:
		return [][]ast.Node{value.Body}
	case *ast.SlotNode:
		return [][]ast.Node{value.Body}
	case *ast.ConditionNode:
		return [][]ast.Node{value.Then, value.Else}
	}
	return nil
}
