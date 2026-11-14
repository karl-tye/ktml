package templmigrate

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ktmlLiteral quotes literal text as a KTML string value. Single-line text
// with braces or double quotes uses a backtick string, which KTML takes
// verbatim. Anything else is a double-quoted Go string whose braces are
// Unicode escapes, so no brace starts an interpolation.
func ktmlLiteral(text string) string {
	if text != "" && !strings.ContainsAny(text, "`\n\r") && strings.ContainsAny(text, "{}\"\\") && utf8.ValidString(text) {
		return "`" + text + "`"
	}
	return `"` + quotedLiteral(text) + `"`
}

// quotedLiteral escapes literal text for the inside of a double-quoted KTML
// string.
func quotedLiteral(text string) string {
	quoted := strconv.Quote(text)
	quoted = quoted[1 : len(quoted)-1]
	quoted = strings.ReplaceAll(quoted, "{", "\\u007b")
	return strings.ReplaceAll(quoted, "}", "\\u007d")
}

// segment is one part of a KTML quoted value: literal text or a path.
type segment struct {
	literal string
	path    string
}

// ktmlValue quotes a sequence of literal and path segments.
func ktmlValue(segments []segment) string {
	paths := false
	for _, s := range segments {
		if s.path != "" {
			paths = true
		}
	}
	if !paths {
		var b strings.Builder
		for _, s := range segments {
			b.WriteString(s.literal)
		}
		return ktmlLiteral(b.String())
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, s := range segments {
		if s.path != "" {
			b.WriteString("{" + s.path + "}")
			continue
		}
		b.WriteString(quotedLiteral(s.literal))
	}
	b.WriteByte('"')
	return b.String()
}

// lowerCamel turns words into a KTML field name ([a-z][A-Za-z0-9]*).
func lowerCamel(words []string) string {
	var b strings.Builder
	for _, word := range words {
		for _, part := range splitWord(word) {
			if b.Len() == 0 {
				b.WriteString(strings.ToLower(part[:1]) + part[1:])
				continue
			}
			b.WriteString(strings.ToUpper(part[:1]) + part[1:])
		}
	}
	name := b.String()
	if name == "" || !unicode.IsLetter(rune(name[0])) {
		name = "value" + strings.ToUpper(name[:min(1, len(name))]) + name[min(1, len(name)):]
	}
	return name
}

// splitWord splits on anything that is not an ASCII letter or digit.
func splitWord(word string) []string {
	return strings.FieldsFunc(word, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
	})
}

// upperFirst uppercases the first ASCII letter, as generated Go names do.
func upperFirst(name string) string {
	if name == "" {
		return name
	}
	if name[0] >= 'a' && name[0] <= 'z' {
		return string(name[0]-'a'+'A') + name[1:]
	}
	return name
}

// goExported reports whether a Go name is exported.
func goExported(name string) bool {
	r, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(r)
}

// ktmlKeywords cannot be KTML item or input names.
var ktmlKeywords = map[string]bool{"if": true, "else": true, "for": true, "in": true, "key": true, "slot": true, "import": true, "record": true, "state": true, "default": true, "children": true, "true": true, "false": true}

// uniqueName returns name, or name with a numeric suffix, not yet in used,
// and records it.
func uniqueName(used map[string]bool, name string) string {
	candidate := name
	for i := 2; used[candidate] || ktmlKeywords[candidate]; i++ {
		candidate = name + strconv.Itoa(i)
	}
	used[candidate] = true
	return candidate
}

// htmlElements are the element names a content input must not shadow: KTML
// reads a body line naming a content input as its outlet.
var htmlElements = map[string]bool{}

func init() {
	for _, name := range strings.Fields(`a abbr address area article aside audio b base bdi bdo blockquote body br button canvas caption cite code col colgroup data datalist dd del details dfn dialog div dl dt em embed fieldset figcaption figure footer form h1 h2 h3 h4 h5 h6 head header hgroup hr html i iframe img input ins kbd label legend li link main map mark menu meta meter nav noscript object ol optgroup option output p param picture pre progress q rp rt ruby s samp script search section select slot small source span strong style sub summary sup svg table tbody td template textarea tfoot th thead time title tr track u ul var video wbr`) {
		htmlElements[name] = true
	}
}
