package ktml

import (
	"fmt"
	"strings"

	"github.com/karl-tye/ktml/internal/attrname"
)

// Attributes is an ordered bag of HTML attributes that a component forwards
// to one of its elements with ...name. Each entry carries the output context
// its value was checked for when it was built, so forwarding cannot bypass
// the script-attribute or style contexts: the constructors accept a name
// only with a value of the context that name requires. The zero value is
// empty.
type Attributes struct {
	entries []AttributeEntry
}

// AttributeKind is the value context of one forwarded attribute.
type AttributeKind uint8

const (
	// AttributeText is a string value, attribute-escaped.
	AttributeText AttributeKind = iota + 1
	// AttributePresence is a valueless boolean attribute, written only when
	// present.
	AttributePresence
	// AttributeURL is a URL value, written through the URL policy.
	AttributeURL
	// AttributeTrustedURL is a TrustedURL value.
	AttributeTrustedURL
	// AttributeJSON is a JSONValue.
	AttributeJSON
	// AttributeScript is a TrustedScript value of a script attribute.
	AttributeScript
	// AttributeScriptBool is a boolean of a script attribute, written as the
	// JavaScript literal true or false.
	AttributeScriptBool
	// AttributeCSS is a CSSValue of the style attribute.
	AttributeCSS
)

// AttributeEntry is one attribute of an Attributes bag.
type AttributeEntry struct {
	name    string
	kind    AttributeKind
	value   string
	present bool
}

// Name returns the attribute name.
func (e AttributeEntry) Name() string { return e.name }

// Kind returns the value context.
func (e AttributeEntry) Kind() AttributeKind { return e.kind }

// Value returns the raw value: the string, URL, JSON, script, or CSS text,
// or "true"/"false" for AttributeScriptBool. It is "" for AttributePresence.
func (e AttributeEntry) Value() string { return e.value }

// Present reports whether an AttributePresence entry is written.
func (e AttributeEntry) Present() bool { return e.present }

// Len returns the number of entries.
func (a Attributes) Len() int { return len(a.entries) }

// At returns entry i, in insertion order.
func (a Attributes) At(i int) AttributeEntry { return a.entries[i] }

// JoinAttributes combines bags in order. When names repeat
// (case-insensitively), the first entry wins and later ones are dropped, as
// an HTML parser keeps the first of duplicate attributes.
func JoinAttributes(bags ...Attributes) Attributes {
	total := 0
	for _, bag := range bags {
		total += len(bag.entries)
	}
	if total == 0 {
		return Attributes{}
	}
	entries := make([]AttributeEntry, 0, total)
	for _, bag := range bags {
	next:
		for _, entry := range bag.entries {
			for _, existing := range entries {
				if strings.EqualFold(existing.name, entry.name) {
					continue next
				}
			}
			entries = append(entries, entry)
		}
	}
	return Attributes{entries: entries}
}

// AttributeError reports an attribute name that is invalid, or a value whose
// context the name does not accept.
type AttributeError struct {
	Name   string
	Reason string
}

func (e *AttributeError) Error() string {
	return fmt.Sprintf("ktml: attribute %q: %s", e.Name, e.Reason)
}

type attributeContext uint8

const (
	plainContext attributeContext = iota
	scriptContext
	styleContext
	urlContext
	htmlContext
)

func contextOf(name string) attributeContext {
	switch {
	case attrname.Script(name):
		return scriptContext
	case attrname.Style(name):
		return styleContext
	case attrname.URL(name):
		return urlContext
	case attrname.HTML(name):
		return htmlContext
	}
	return plainContext
}

func single(name string, kind AttributeKind, value string, present bool, allowed ...attributeContext) (Attributes, error) {
	if !attrname.Valid(name) {
		return Attributes{}, &AttributeError{Name: name, Reason: "not a valid attribute name"}
	}
	context := contextOf(name)
	for _, candidate := range allowed {
		if candidate == context {
			return Attributes{entries: []AttributeEntry{{name: name, kind: kind, value: value, present: present}}}, nil
		}
	}
	switch context {
	case scriptContext:
		return Attributes{}, &AttributeError{Name: name, Reason: "is evaluated as JavaScript; use ScriptAttr, ScriptBoolAttr, or JSONAttr"}
	case styleContext:
		return Attributes{}, &AttributeError{Name: name, Reason: "is CSS; use CSSAttr"}
	case urlContext:
		return Attributes{}, &AttributeError{Name: name, Reason: "is a URL; use URLAttr or TrustedURLAttr"}
	case htmlContext:
		return Attributes{}, &AttributeError{Name: name, Reason: "is an HTML document the browser runs; write it as literal template text"}
	}
	return Attributes{}, &AttributeError{Name: name, Reason: "is not a script or style attribute; use Attr, URLAttr, or JSONAttr"}
}

// Attr is a plain attribute with a string value. Script, style, and URL
// attributes are rejected.
func Attr(name, value string) (Attributes, error) {
	return single(name, AttributeText, value, false, plainContext)
}

// BoolAttr is a valueless attribute written only when present, such as
// required or disabled. It is valid for every name, since it has no value.
func BoolAttr(name string, present bool) (Attributes, error) {
	return single(name, AttributePresence, "", present, plainContext, scriptContext, styleContext, urlContext, htmlContext)
}

// URLAttr is an attribute whose URL is written through the URL policy.
// Script and style attributes are rejected.
func URLAttr(name string, value URL) (Attributes, error) {
	return single(name, AttributeURL, value.String(), false, plainContext, urlContext)
}

// TrustedURLAttr is an attribute with a TrustedURL, which bypasses the URL
// policy. Script and style attributes are rejected.
func TrustedURLAttr(name string, value TrustedURL) (Attributes, error) {
	return single(name, AttributeTrustedURL, value.String(), false, plainContext, urlContext)
}

// JSONAttr is an attribute with a JSON value. It is valid for plain and
// script attributes, as in templates.
func JSONAttr(name string, value JSONValue) (Attributes, error) {
	return single(name, AttributeJSON, value.String(), false, plainContext, scriptContext)
}

// ScriptAttr is a script attribute, such as @click or hx-on::after-request,
// with a TrustedScript value. Other names are rejected.
func ScriptAttr(name string, value TrustedScript) (Attributes, error) {
	return single(name, AttributeScript, value.String(), false, scriptContext)
}

// ScriptBoolAttr is a script attribute, such as :disabled, with a boolean
// written as the JavaScript literal true or false. Other names are rejected.
func ScriptBoolAttr(name string, value bool) (Attributes, error) {
	literal := "false"
	if value {
		literal = "true"
	}
	return single(name, AttributeScriptBool, literal, false, scriptContext)
}

// CSSAttr is the style attribute with a CSSValue. Other names are rejected.
func CSSAttr(name string, value CSSValue) (Attributes, error) {
	return single(name, AttributeCSS, value.String(), false, styleContext)
}

func must(attributes Attributes, err error) Attributes {
	if err != nil {
		panic(err)
	}
	return attributes
}

// MustAttr is Attr for a constant name; it panics on an error.
func MustAttr(name, value string) Attributes { return must(Attr(name, value)) }

// MustBoolAttr is BoolAttr for a constant name; it panics on an error.
func MustBoolAttr(name string, present bool) Attributes { return must(BoolAttr(name, present)) }

// MustURLAttr is URLAttr for a constant name; it panics on an error.
func MustURLAttr(name string, value URL) Attributes { return must(URLAttr(name, value)) }

// MustTrustedURLAttr is TrustedURLAttr for a constant name; it panics on an
// error.
func MustTrustedURLAttr(name string, value TrustedURL) Attributes {
	return must(TrustedURLAttr(name, value))
}

// MustJSONAttr is JSONAttr for a constant name; it panics on an error.
func MustJSONAttr(name string, value JSONValue) Attributes { return must(JSONAttr(name, value)) }

// MustScriptAttr is ScriptAttr for a constant name; it panics on an error.
func MustScriptAttr(name string, value TrustedScript) Attributes {
	return must(ScriptAttr(name, value))
}

// MustScriptBoolAttr is ScriptBoolAttr for a constant name; it panics on an
// error.
func MustScriptBoolAttr(name string, value bool) Attributes {
	return must(ScriptBoolAttr(name, value))
}

// MustCSSAttr is CSSAttr for a constant name; it panics on an error.
func MustCSSAttr(name string, value CSSValue) Attributes { return must(CSSAttr(name, value)) }
