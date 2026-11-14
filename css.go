package ktml

import (
	"fmt"
	"strings"
)

// CSSDeclaration is one property: value pair of a CSS declaration list.
type CSSDeclaration struct {
	property string
	value    string
}

// CSSDecl pairs a property with its value. CSS validates both.
func CSSDecl(property, value string) CSSDeclaration {
	return CSSDeclaration{property: property, value: value}
}

// cssFunctions are the CSS functions a value may call. Every other function,
// url() and expression() among them, is rejected, so a CSSValue built by CSS
// can never load a resource or run script.
var cssFunctions = map[string]bool{
	"calc": true, "min": true, "max": true, "clamp": true, "var": true,
	"rgb": true, "rgba": true, "hsl": true, "hsla": true,
	"translate": true, "translatex": true, "translatey": true,
	"scale": true, "scalex": true, "scaley": true, "rotate": true,
}

// gridFunctions are the grid track functions a value may call. Their
// arguments have a grammar of their own (gridFunction): numbers, lengths,
// percentages, fr, and track keywords only, never another free-form value.
var gridFunctions = map[string]bool{"repeat": true, "minmax": true, "fit-content": true}

// CSS serializes declarations as "property: value; property: value". A
// property must be a CSS property name or a custom property (--name). A
// value may contain only letters, digits, spaces, and . , % # ( ) + - * / _ !,
// and may call only calc, min, max, clamp, var, rgb, rgba, hsl, hsla,
// translate, translateX, translateY, scale, scaleX, scaleY, and rotate.
// Grid track lists may use repeat(), minmax(), and fit-content(): a repeat
// count is a positive integer, auto-fill, or auto-fit; a track is a length,
// percentage, fr, 0, auto, min-content, max-content, minmax(a, b) of those,
// or fit-content(<length-percentage>); minmax() and fit-content() may also
// stand alone, but nothing nests in them and repeat() does not nest. Any
// other declaration is a *CSSError, so presenter data cannot break out of
// the declaration or reach url(). TrustCSS remains the audit boundary for
// CSS outside this grammar.
func CSS(declarations ...CSSDeclaration) (CSSValue, error) {
	var serialized strings.Builder
	for index, declaration := range declarations {
		if reason := cssProperty(declaration.property); reason != "" {
			return CSSValue{}, &CSSError{Property: declaration.property, Value: declaration.value, Reason: reason}
		}
		if reason := cssValue(declaration.value); reason != "" {
			return CSSValue{}, &CSSError{Property: declaration.property, Value: declaration.value, Reason: reason}
		}
		if index > 0 {
			serialized.WriteString("; ")
		}
		serialized.WriteString(declaration.property)
		serialized.WriteString(": ")
		serialized.WriteString(strings.TrimSpace(declaration.value))
	}
	return CSSValue{raw: serialized.String()}, nil
}

// MustCSS is CSS for constant declarations; it panics on an error.
func MustCSS(declarations ...CSSDeclaration) CSSValue {
	value, err := CSS(declarations...)
	if err != nil {
		panic(err)
	}
	return value
}

// CSSError reports a declaration CSS rejects.
type CSSError struct {
	Property string
	Value    string
	Reason   string
}

func (e *CSSError) Error() string {
	return fmt.Sprintf("ktml: CSS declaration %q: %q: %s", e.Property, e.Value, e.Reason)
}

func cssProperty(property string) string {
	name := property
	custom := strings.HasPrefix(name, "--")
	if custom {
		name = name[2:]
	} else {
		name = strings.TrimPrefix(name, "-")
	}
	if name == "" {
		return "the property is empty"
	}
	for index := 0; index < len(name); index++ {
		c := name[index]
		letter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		switch {
		case letter:
		case c >= '0' && c <= '9' || c == '-':
			if index == 0 && !custom {
				return "a property name starts with a letter"
			}
		case c == '_' && custom:
		default:
			return fmt.Sprintf("a property name cannot contain %q", c)
		}
	}
	return ""
}

func cssValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "the value is empty"
	}
	depth := 0
	for index := 0; index < len(value); index++ {
		c := value[index]
		switch {
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9':
		case index+1 < len(value) && (c == '/' && value[index+1] == '*' || c == '*' && value[index+1] == '/'):
			// A comment would swallow the declarations after this one.
			return "a value cannot open or close a comment"
		case strings.IndexByte(" \t.,%#+-*/_!", c) >= 0:
		case c == '(':
			start := index
			for start > 0 && isCSSIdentifierByte(value[start-1]) {
				start--
			}
			function := strings.ToLower(value[start:index])
			if gridFunctions[function] {
				end := closingParen(value, index)
				if end < 0 {
					return "the value has an unclosed ("
				}
				if reason := gridFunction(function, value[index+1:end]); reason != "" {
					return fmt.Sprintf("%s(): %s", value[start:index], reason)
				}
				index = end
				continue
			}
			if function != "" && !cssFunctions[function] {
				return fmt.Sprintf("the function %s() is not allowed", value[start:index])
			}
			depth++
		case c == ')':
			depth--
			if depth < 0 {
				return "the value has an unmatched )"
			}
		default:
			return fmt.Sprintf("a value cannot contain %q", c)
		}
	}
	if depth != 0 {
		return "the value has an unclosed ("
	}
	return ""
}

func isCSSIdentifierByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_'
}

// closingParen returns the index of the ) that closes the ( at open, or -1.
func closingParen(value string, open int) int {
	depth := 0
	for index := open; index < len(value); index++ {
		switch value[index] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return index
			}
		}
	}
	return -1
}

// gridFunction checks the arguments of repeat(), minmax(), or fit-content().
func gridFunction(function, arguments string) string {
	args := splitTopLevel(arguments, ',')
	switch function {
	case "repeat":
		if len(args) != 2 {
			return "repeat() takes a count and a track list"
		}
		if !repeatCount(args[0]) {
			return fmt.Sprintf("%q is not a repeat count (a positive integer, auto-fill, or auto-fit)", trimBlanks(args[0]))
		}
		tracks := splitTopLevel(args[1], ' ', '\t')
		if len(tracks) == 0 {
			return "repeat() needs at least one track"
		}
		for _, track := range tracks {
			if reason := trackSize(track); reason != "" {
				return reason
			}
		}
		return ""
	case "minmax":
		return minmaxArguments(args)
	default:
		return fitContentArguments(args)
	}
}

// trackSize checks one track of a repeat() track list.
func trackSize(track string) string {
	open := strings.IndexByte(track, '(')
	if open < 0 {
		if !trackBreadth(track) {
			return fmt.Sprintf("%q is not a track size", track)
		}
		return ""
	}
	if closingParen(track, open) != len(track)-1 {
		return fmt.Sprintf("%q is not a track size", track)
	}
	args := splitTopLevel(track[open+1:len(track)-1], ',')
	switch strings.ToLower(track[:open]) {
	case "minmax":
		return minmaxArguments(args)
	case "fit-content":
		return fitContentArguments(args)
	}
	return fmt.Sprintf("the function %s() is not allowed in a track list", track[:open])
}

func minmaxArguments(args []string) string {
	if len(args) != 2 {
		return "minmax() takes two track breadths"
	}
	for _, arg := range args {
		if !trackBreadth(trimBlanks(arg)) {
			return fmt.Sprintf("%q is not a track breadth", trimBlanks(arg))
		}
	}
	return ""
}

func fitContentArguments(args []string) string {
	if len(args) != 1 || !lengthPercentage(trimBlanks(args[0]), false) {
		return "fit-content() takes one length or percentage"
	}
	return ""
}

// splitTopLevel splits s at the separator bytes outside parentheses,
// dropping empty pieces when splitting on whitespace and keeping them
// (so they fail) when splitting on commas.
func splitTopLevel(s string, separators ...byte) []string {
	whitespace := separators[0] == ' '
	var pieces []string
	depth, start := 0, 0
	add := func(piece string) {
		if !whitespace || piece != "" {
			pieces = append(pieces, piece)
		}
	}
	for index := 0; index < len(s); index++ {
		switch c := s[index]; {
		case c == '(':
			depth++
		case c == ')':
			depth--
		case depth == 0 && strings.IndexByte(string(separators), c) >= 0:
			add(s[start:index])
			start = index + 1
		}
	}
	add(s[start:])
	return pieces
}

func repeatCount(count string) bool {
	count = strings.ToLower(trimBlanks(count))
	if count == "auto-fill" || count == "auto-fit" {
		return true
	}
	if count == "" || strings.Trim(count, "0") == "" {
		return false
	}
	for index := 0; index < len(count); index++ {
		if count[index] < '0' || count[index] > '9' {
			return false
		}
	}
	return true
}

// trackBreadth reports whether s is a length, percentage, fr, 0, auto,
// min-content, or max-content.
func trackBreadth(s string) bool {
	switch strings.ToLower(s) {
	case "auto", "min-content", "max-content":
		return true
	}
	return lengthPercentage(s, true)
}

// cssLengthUnits are the units a grid length may carry.
var cssLengthUnits = map[string]bool{
	"px": true, "em": true, "rem": true, "ex": true, "ch": true, "lh": true, "rlh": true,
	"vw": true, "vh": true, "vmin": true, "vmax": true, "svw": true, "svh": true,
	"lvw": true, "lvh": true, "dvw": true, "dvh": true, "cqw": true, "cqh": true,
	"cm": true, "mm": true, "q": true, "in": true, "pt": true, "pc": true,
}

// lengthPercentage reports whether s is an unsigned number with a length
// unit or %, or 0; with fr, an fr flex value too.
func lengthPercentage(s string, fr bool) bool {
	index, digits := 0, 0
	for index < len(s) && s[index] >= '0' && s[index] <= '9' {
		index, digits = index+1, digits+1
	}
	if index < len(s) && s[index] == '.' {
		index++
		for index < len(s) && s[index] >= '0' && s[index] <= '9' {
			index, digits = index+1, digits+1
		}
	}
	if digits == 0 {
		return false
	}
	unit := strings.ToLower(s[index:])
	switch {
	case unit == "":
		return strings.Trim(s, "0.") == ""
	case unit == "%", cssLengthUnits[unit]:
		return true
	case unit == "fr":
		return fr
	}
	return false
}

// trimBlanks trims the spaces and tabs a value may contain; any other
// whitespace stays and fails the grammar.
func trimBlanks(s string) string { return strings.Trim(s, " \t") }
