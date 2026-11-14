package ktml

import (
	"encoding/json"
	"fmt"
)

// The contextual value types below are opaque: templates cannot construct or
// convert them, so a template cannot elevate an untrusted string (ADR-0001).
// Their constructors are provisional; the trust slice owns the final
// constructor API.

// TrustedHTML is markup rendered verbatim as HTML text. TrustHTML is an
// explicit audit boundary.
type TrustedHTML struct{ raw string }

// TrustHTML wraps markup whose safety Go has established.
func TrustHTML(raw string) TrustedHTML { return TrustedHTML{raw: raw} }

// String returns the wrapped markup.
func (h TrustedHTML) String() string { return h.raw }

// TrustedScript is JavaScript for a script attribute, where it is
// attribute-escaped, or a script element body, where it is verbatim except
// that </script and <!-- are rewritten so it cannot end the element.
// TrustScript is an explicit audit boundary.
type TrustedScript struct{ raw string }

// TrustScript wraps a script body whose safety Go has established.
func TrustScript(raw string) TrustedScript { return TrustedScript{raw: raw} }

// String returns the wrapped script.
func (s TrustedScript) String() string { return s.raw }

// CSSValue is serialized CSS rendered as an escaped attribute value.
type CSSValue struct{ raw string }

// TrustCSS wraps CSS serialized by Go.
func TrustCSS(raw string) CSSValue { return CSSValue{raw: raw} }

// String returns the serialized CSS.
func (c CSSValue) String() string { return c.raw }

// JSONValue is encoded JSON. Renderers make it safe for its container: a
// script element or a double-quoted attribute.
type JSONValue struct{ raw string }

// TrustJSON wraps JSON encoded by Go, for example by encoding/json.
func TrustJSON(encoded string) JSONValue { return JSONValue{raw: encoded} }

// String returns the encoded JSON.
func (j JSONValue) String() string { return j.raw }

// JSON encodes v with encoding/json. The encoder escapes <, >, &, U+2028, and
// U+2029, so the value cannot end a script element or open a comment, and it
// is a valid JavaScript literal in any expression position of a script
// attribute or script body. An error is encoding/json's, wrapped.
func JSON(v any) (JSONValue, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return JSONValue{}, fmt.Errorf("ktml: encode JSON: %w", err)
	}
	return JSONValue{raw: string(data)}, nil
}

// MustJSON is JSON for values that always encode, such as strings, numbers,
// booleans, and slices or structs of them; it panics on an error.
func MustJSON(v any) JSONValue {
	value, err := JSON(v)
	if err != nil {
		panic(err)
	}
	return value
}
