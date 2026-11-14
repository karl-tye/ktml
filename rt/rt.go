// Package rt holds the write and escaping primitives used by generated KTML
// renderers. It is for generated code only; its API is tied to the pinned
// generator version rather than to semver for handwritten callers.
//
// Every primitive writes runs of its input directly to the Writer, with no
// intermediate string, and returns the writer's error unwrapped. Generated
// code wraps a failure with WriteError. The generator escapes static markup
// at generation time with the same rules (see EscapeString).
package rt

import (
	"math"
	"strconv"
	"strings"

	"github.com/karl-tye/ktml"
)

// Raw writes trusted static markup.
func Raw(w *Writer, value string) error {
	return raw(w, value)
}

// Text writes an HTML-escaped text value.
func Text(w *Writer, value string) error {
	return escape(w, value)
}

// TextInt writes an integer in canonical base-10 form as HTML text.
func TextInt(w *Writer, value int64) error {
	return writeBytes(w, strconv.AppendInt(w.Scratch(), value, 10))
}

// TextNumber writes a number in canonical form as HTML text (see
// AppendNumber).
func TextNumber(w *Writer, value float64) error {
	return writeBytes(w, AppendNumber(w.Scratch(), value))
}

// TrustedHTML writes trusted markup verbatim.
func TrustedHTML(w *Writer, value ktml.TrustedHTML) error {
	return raw(w, value.String())
}

// Attr writes an escaped double-quoted attribute value.
func Attr(w *Writer, value string) error {
	return escape(w, value)
}

// AttrInt writes an integer in canonical base-10 form as an attribute value.
func AttrInt(w *Writer, value int64) error {
	return TextInt(w, value)
}

// AttrNumber writes a number in canonical form as an attribute value.
func AttrNumber(w *Writer, value float64) error {
	return TextNumber(w, value)
}

// AppendNumber appends the canonical form of a number: the shortest decimal
// that round-trips, without an exponent for magnitudes in [1e-6, 1e21), and
// in 'e' form otherwise. NaN and infinities render as NaN, Infinity, and
// -Infinity.
func AppendNumber(b []byte, value float64) []byte {
	switch {
	case math.IsNaN(value):
		return append(b, "NaN"...)
	case math.IsInf(value, 1):
		return append(b, "Infinity"...)
	case math.IsInf(value, -1):
		return append(b, "-Infinity"...)
	}
	if magnitude := math.Abs(value); magnitude == 0 || (magnitude >= 1e-6 && magnitude < 1e21) {
		return strconv.AppendFloat(b, value, 'f', -1, 64)
	}
	return strconv.AppendFloat(b, value, 'e', -1, 64)
}

// UnsafeURL replaces a URL whose scheme the URL policy rejects.
const UnsafeURL = "about:invalid#ktml-unsafe-url"

// URL writes an escaped attribute value for a policy-checked URL. Relative URLs
// and the http, https, mailto, and tel schemes pass; anything else renders
// UnsafeURL.
func URL(w *Writer, value ktml.URL) error {
	raw := value.String()
	if !allowedURL(raw) {
		raw = UnsafeURL
	}
	return escape(w, raw)
}

// TrustedURL writes an escaped attribute value for a URL that bypasses the URL
// policy.
func TrustedURL(w *Writer, value ktml.TrustedURL) error {
	return escape(w, value.String())
}

func allowedURL(raw string) bool {
	for index := 0; index < len(raw); index++ {
		switch raw[index] {
		case '/', '?', '#':
			return true
		case ':':
			switch strings.ToLower(raw[:index]) {
			case "http", "https", "mailto", "tel":
				return true
			default:
				return false
			}
		}
	}
	return true
}

// CSS writes a serialized CSS value as an escaped attribute value.
func CSS(w *Writer, value ktml.CSSValue) error {
	return escape(w, value.String())
}

// JSONScript writes JSON inside a script element. <, >, &, U+2028, and U+2029
// are written as \u escapes, so the value cannot close the element or start a
// comment, and stays valid JavaScript.
func JSONScript(w *Writer, value ktml.JSONValue) error {
	encoded := value.String()
	last := 0
	for index := 0; index < len(encoded); index++ {
		var replacement string
		switch encoded[index] {
		case '<':
			replacement = `\u003c`
		case '>':
			replacement = `\u003e`
		case '&':
			replacement = `\u0026`
		case 0xE2:
			if index+2 < len(encoded) && encoded[index+1] == 0x80 && (encoded[index+2] == 0xA8 || encoded[index+2] == 0xA9) {
				replacement = `\u2028`
				if encoded[index+2] == 0xA9 {
					replacement = `\u2029`
				}
				if err := raw(w, encoded[last:index]); err != nil {
					return err
				}
				if err := raw(w, replacement); err != nil {
					return err
				}
				index += 2
				last = index + 1
			}
			continue
		default:
			continue
		}
		if last < index {
			if err := raw(w, encoded[last:index]); err != nil {
				return err
			}
		}
		if err := raw(w, replacement); err != nil {
			return err
		}
		last = index + 1
	}
	if last < len(encoded) {
		return raw(w, encoded[last:])
	}
	return nil
}

// JSONAttr writes JSON as an escaped double-quoted attribute value.
func JSONAttr(w *Writer, value ktml.JSONValue) error {
	return escape(w, value.String())
}

// ScriptAttr writes trusted script as an escaped double-quoted attribute
// value, for script attributes such as onclick, @click, and x-on:click. The
// browser decodes the escapes before it evaluates the script.
func ScriptAttr(w *Writer, value ktml.TrustedScript) error {
	return escape(w, value.String())
}

// ScriptBool writes a boolean as the JavaScript literal true or false, for
// script attributes such as :disabled.
func ScriptBool(w *Writer, value bool) error {
	if value {
		return raw(w, "true")
	}
	return raw(w, "false")
}

// BoolText is the text true or false, the value of an aria-* attribute
// whose boolean path renders as text (ADR-0001).
func BoolText(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

// Script writes a trusted script body. It is verbatim except where the
// text could end the script element or start an escaped state in which
// its end tag would not end it: </script is written as <\/script and <!--
// as <\!--, matched case-insensitively. Both rewrites mean the same in
// JavaScript strings, regular expressions, and comments, the only places
// valid JavaScript contains them.
func Script(w *Writer, value ktml.TrustedScript) error {
	text := value.String()
	last := 0
	for index := 0; index+1 < len(text); index++ {
		if text[index] != '<' {
			continue
		}
		var insert string
		switch {
		case text[index+1] == '/' && index+8 <= len(text) && strings.EqualFold(text[index+2:index+8], "script"):
			insert = `\`
		case text[index+1] == '!' && strings.HasPrefix(text[index+2:], "--"):
			insert = `\`
		default:
			continue
		}
		if err := raw(w, text[last:index+1]); err != nil {
			return err
		}
		if err := raw(w, insert); err != nil {
			return err
		}
		last = index + 1
	}
	return raw(w, text[last:])
}

// escapes maps each byte HTML escaping replaces to its entity. The set and
// spellings match html.EscapeString.
var escapes = [256]string{
	'&':  "&amp;",
	'\'': "&#39;",
	'<':  "&lt;",
	'>':  "&gt;",
	'"':  "&#34;",
}

// escape writes value with &, ', <, >, and " replaced, writing the unescaped
// runs between them directly.
func escape(w *Writer, value string) error {
	last := 0
	for index := 0; index < len(value); index++ {
		replacement := escapes[value[index]]
		if replacement == "" {
			continue
		}
		if last < index {
			if err := raw(w, value[last:index]); err != nil {
				return err
			}
		}
		if err := raw(w, replacement); err != nil {
			return err
		}
		last = index + 1
	}
	if last == 0 {
		return raw(w, value)
	}
	if last < len(value) {
		return raw(w, value[last:])
	}
	return nil
}

// EscapeString returns value HTML-escaped by the rules every escaping
// primitive applies. The generator uses it to escape static markup.
func EscapeString(value string) string {
	var builder strings.Builder
	last := 0
	for index := 0; index < len(value); index++ {
		if replacement := escapes[value[index]]; replacement != "" {
			builder.WriteString(value[last:index])
			builder.WriteString(replacement)
			last = index + 1
		}
	}
	if last == 0 {
		return value
	}
	builder.WriteString(value[last:])
	return builder.String()
}

// Canceled returns the typed error for a context canceled before a component
// or repetition item renders. source is the repetition source, or "" on
// component entry. It unwraps to the context error.
func Canceled(component, source string, err error) error {
	return &ktml.RenderError{Kind: ktml.ErrCanceled, Component: component, Input: source, Err: err}
}

// InvalidState returns the typed error for a nil or unknown Presentation State
// selector, raised before the component writes.
func InvalidState(component, input string) error {
	return &ktml.RenderError{Kind: ktml.ErrInvalidState, Component: component, Input: input}
}

// MissingContent returns the typed error for a required content input that is
// nil, raised before the component writes.
func MissingContent(component, input string) error {
	return &ktml.RenderError{Kind: ktml.ErrMissingContent, Component: component, Input: input}
}
