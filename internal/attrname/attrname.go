// Package attrname holds the attribute-name rules shared by the compiler and
// the runtime: the lexical rule for names, and the core classification of
// script and style attributes. Plugins cannot relax either.
package attrname

import "strings"

// Valid reports whether name follows the attribute-name lexical rule
// (attribute-names-doctype-comments-v1, widened by
// migration-language-v1): an optional @ or :, a letter or _, then letters,
// digits, _, -, :, ., and *.
func Valid(name string) bool {
	index := 0
	if index < len(name) && (name[index] == '@' || name[index] == ':') {
		index++
	}
	if index >= len(name) || !isLetter(name[index]) {
		return false
	}
	for index++; index < len(name); index++ {
		if !NameChar(name[index]) {
			return false
		}
	}
	return true
}

// NameChar reports whether b may continue an attribute name.
func NameChar(b byte) bool {
	return isLetter(b) || b >= '0' && b <= '9' || b == '-' || b == ':' || b == '.' || b == '*'
}

func isLetter(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b == '_'
}

// scriptBases are the attribute names, before any :argument or .modifier,
// whose values the browser or a core-supported library evaluates as
// JavaScript (ADR-0001).
var scriptBases = map[string]bool{
	// Alpine.js directives whose values are expressions.
	"x-data": true, "x-init": true, "x-show": true, "x-text": true, "x-html": true,
	"x-model": true, "x-modelable": true, "x-for": true, "x-effect": true, "x-if": true,
	"x-id": true, "x-on": true, "x-bind": true, "x-trap": true, "x-intersect": true,
	"x-anchor": true, "x-sort": true, "x-mask": true,
	// HTMX attributes that run or may run script: inline handlers, js:
	// values, and trigger filters.
	"hx-on": true, "hx-vals": true, "hx-vars": true, "hx-headers": true, "hx-trigger": true,
}

// Script reports whether an element attribute is a script attribute: on*
// event handlers, Alpine.js @ and : shorthands, the Alpine.js expression
// directives, and the HTMX attributes that run script, including the hx-on
// handlers in their colon and dash forms. Names match case-insensitively; HTMX names
// may carry a data- prefix.
func Script(name string) bool {
	name = strings.ToLower(name)
	if strings.HasPrefix(name, "@") || strings.HasPrefix(name, ":") || strings.HasPrefix(name, "on") {
		return true
	}
	base := strings.TrimPrefix(name, "data-")
	if !strings.HasPrefix(base, "hx-") {
		base = name
	}
	// Every hx-on handler runs script: hx-on:click, hx-on::after-request,
	// and the htmx 2 dash forms hx-on-click and hx-on--after-request alike.
	if base == "hx-on" || strings.HasPrefix(base, "hx-on:") || strings.HasPrefix(base, "hx-on-") {
		return true
	}
	if index := strings.IndexAny(base, ":.*"); index >= 0 {
		base = base[:index]
	}
	return scriptBases[base]
}

// ARIA reports whether an element attribute is an aria-* attribute, whose
// boolean path renders as the text true or false rather than as presence
// (ADR-0001, aria-* boolean amendment of 2026-10-13). It matches
// case-insensitively.
func ARIA(name string) bool {
	return len(name) > len("aria-") && strings.EqualFold(name[:len("aria-")], "aria-")
}

// Style reports whether an element attribute is the style attribute, whose
// value is CSS. It matches case-insensitively.
func Style(name string) bool {
	return strings.EqualFold(name, "style")
}

// HTML reports whether an element attribute is iframe's srcdoc, whose value
// the browser parses as a whole HTML document in the page's origin. KTML has
// no trusted HTML value, so data never reaches it: only literal text. It
// matches case-insensitively.
func HTML(name string) bool {
	return strings.EqualFold(name, "srcdoc")
}

// urlNames are the attribute names whose values are URLs that the browser
// loads, navigates to, or submits to, and the HTMX request attributes
// (ADR-0001, URL attribute amendment of 2026-10-13).
var urlNames = map[string]bool{
	"action": true, "background": true, "cite": true, "codebase": true, "data": true,
	"formaction": true, "href": true, "icon": true, "longdesc": true, "manifest": true,
	"ping": true, "poster": true, "src": true, "srcset": true, "usemap": true, "xlink:href": true,
	"hx-get": true, "hx-post": true, "hx-put": true, "hx-patch": true, "hx-delete": true,
}

// URL reports whether an element attribute is a URL attribute, whose
// interpolated value must be a URL or TrustedURL. Names match
// case-insensitively, on every element; HTMX names may carry a data- prefix.
func URL(name string) bool {
	name = strings.ToLower(name)
	if strings.HasPrefix(name, "data-hx-") {
		name = strings.TrimPrefix(name, "data-")
	}
	return urlNames[name]
}
