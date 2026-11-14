package compiler

import (
	"strings"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/attrname"
)

// ScriptAttribute reports whether an element attribute is a script
// attribute: on* event handlers, Alpine.js @ and : shorthands, the Alpine.js
// expression directives, and the HTMX attributes that run script
// (attribute-names-doctype-comments-v1, ADR-0001). The list is core and
// lexical: no plugin is consulted, and plugins cannot relax it. Names match
// case-insensitively; HTMX names may carry a data- prefix.
func ScriptAttribute(name string) bool {
	return attrname.Script(name)
}

// ARIAAttribute reports whether an element attribute is an aria-*
// attribute, where a boolean path renders as the text true or false
// instead of controlling presence (ADR-0001).
func ARIAAttribute(name string) bool {
	return attrname.ARIA(name)
}

// StyleAttribute reports whether an element attribute is the style
// attribute, whose value is CSS.
func StyleAttribute(name string) bool {
	return attrname.Style(name)
}

// URLAttribute reports whether an element attribute is a URL attribute,
// such as href, src, action, or hx-get, whose interpolated value must be a
// URL or TrustedURL (ADR-0001).
func URLAttribute(name string) bool {
	return attrname.URL(name)
}

// HTMLAttribute reports whether an element attribute is iframe's srcdoc,
// whose value is an HTML document: paths cannot reach it (ADR-0001).
func HTMLAttribute(name string) bool {
	return attrname.HTML(name)
}

// SafeURLPrefix reports whether the literal text of a URL attribute value
// before its first path fixes the URL's scheme and host, so interpolated
// text can change only the path, query, or fragment (ADR-0001 amendment of
// 2026-10-13). The text must start a relative path ("/" followed by a
// character other than "/" or "\"), a query ("?"), or a fragment ("#"), or
// be an absolute http, https, mailto, or tel prefix that already reaches
// the path. Tab and newline characters, which URL parsers remove, are
// ignored. A value that starts with a path has no safe prefix.
func SafeURLPrefix(quoted *ast.QuotedValue) bool {
	if quoted == nil {
		return false
	}
	var prefix strings.Builder
	for _, segment := range quoted.Segments {
		if segment.Kind == ast.PathSegment {
			break
		}
		prefix.WriteString(segment.Literal)
	}
	return SafeURLText(prefix.String())
}

// SafeURLText reports whether literal text that starts a URL attribute value
// fixes the URL's scheme and host, as SafeURLPrefix describes.
func SafeURLText(prefix string) bool {
	prefix = strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, prefix)
	if prefix == "" {
		return false
	}
	switch prefix[0] {
	case '?', '#':
		return true
	case '/':
		return len(prefix) > 1 && prefix[1] != '/' && prefix[1] != '\\'
	}
	lower := strings.ToLower(prefix)
	for _, scheme := range []string{"mailto:", "tel:"} {
		if strings.HasPrefix(lower, scheme) {
			return true
		}
	}
	for _, scheme := range []string{"http://", "https://"} {
		if rest, ok := strings.CutPrefix(lower, scheme); ok {
			end := strings.IndexAny(rest, "/?#\\")
			return end > 0
		}
	}
	return false
}
