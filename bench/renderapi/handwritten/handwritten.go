// Package handwritten is the lower bound of the ADR-0003 benchmarks: plain Go
// that writes each page directly to the io.Writer, with no framework, no
// writer wrapper, no context checks, no key preflight, and no typed errors.
// Escaping writes the unescaped runs directly, as rt does.
package handwritten

import (
	"io"
	"strconv"

	"github.com/karl-tye/ktml/bench/renderapi/model"
)

// writer is the io.StringWriter fast path every target in the benchmarks
// has; other targets are adapted.
type writer interface {
	io.Writer
	io.StringWriter
}

type stringWriter struct{ io.Writer }

func (w stringWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }

// out is the per-page writer: the target plus number-formatting scratch. It
// is the one allocation a handwritten page makes.
type out struct {
	writer
	scratch [32]byte
}

func wrap(w io.Writer) *out {
	if sw, ok := w.(writer); ok {
		return &out{writer: sw}
	}
	return &out{writer: stringWriter{w}}
}

var escapes = [256]string{
	'&':  "&amp;",
	'\'': "&#39;",
	'<':  "&lt;",
	'>':  "&gt;",
	'"':  "&#34;",
}

// escape writes s HTML-escaped (the html.EscapeString set) for text and
// double-quoted attribute values.
func escape(w *out, s string) error {
	last := 0
	for i := 0; i < len(s); i++ {
		replacement := escapes[s[i]]
		if replacement == "" {
			continue
		}
		if _, err := w.WriteString(s[last:i]); err != nil {
			return err
		}
		if _, err := w.WriteString(replacement); err != nil {
			return err
		}
		last = i + 1
	}
	_, err := w.WriteString(s[last:])
	return err
}

func writeInt(w *out, value int64) error {
	_, err := w.Write(strconv.AppendInt(w.scratch[:0], value, 10))
	return err
}

// Static writes the Static page.
func Static(w io.Writer) error {
	_, err := io.WriteString(w, staticPage)
	return err
}

// Nested writes the depth-8 Box chain of NestedSame and NestedCross.
func Nested(w io.Writer, label string) error {
	sw := wrap(w)
	for depth := int64(1); depth <= 8; depth++ {
		if _, err := sw.WriteString(`<div class="box" data-depth="`); err != nil {
			return err
		}
		if err := writeInt(sw, depth); err != nil {
			return err
		}
		if _, err := sw.WriteString(`">`); err != nil {
			return err
		}
	}
	if _, err := sw.WriteString(`<p class="leaf">`); err != nil {
		return err
	}
	if err := escape(sw, label); err != nil {
		return err
	}
	_, err := sw.WriteString(`</p></div></div></div></div></div></div></div></div>`)
	return err
}

// NestedSlots writes the depth-8 Panel chain with a title at every level.
func NestedSlots(w io.Writer, label string) error {
	sw := wrap(w)
	for depth := int64(1); depth <= 8; depth++ {
		if _, err := sw.WriteString(`<section class="panel" data-depth="`); err != nil {
			return err
		}
		if err := writeInt(sw, depth); err != nil {
			return err
		}
		if _, err := sw.WriteString(`"><div class="panel-title"><h2>Level `); err != nil {
			return err
		}
		if err := writeInt(sw, depth); err != nil {
			return err
		}
		if _, err := sw.WriteString(`</h2></div><div class="panel-body">`); err != nil {
			return err
		}
	}
	if _, err := sw.WriteString(`<p class="leaf">`); err != nil {
		return err
	}
	if err := escape(sw, label); err != nil {
		return err
	}
	if _, err := sw.WriteString(`</p>`); err != nil {
		return err
	}
	for range 8 {
		if _, err := sw.WriteString(`</div></section>`); err != nil {
			return err
		}
	}
	return nil
}

// ListHeavy writes the ListHeavy list.
func ListHeavy(w io.Writer, entries []model.Entry) error {
	sw := wrap(w)
	if _, err := sw.WriteString(`<ul class="entries">`); err != nil {
		return err
	}
	for i := range entries {
		entry := &entries[i]
		if _, err := sw.WriteString(`<li class="entry" data-score="`); err != nil {
			return err
		}
		if err := writeInt(sw, entry.Score); err != nil {
			return err
		}
		if _, err := sw.WriteString(`"><h3>`); err != nil {
			return err
		}
		if err := escape(sw, entry.Title); err != nil {
			return err
		}
		if _, err := sw.WriteString(`</h3>`); err != nil {
			return err
		}
		if entry.Note != nil {
			if _, err := sw.WriteString(`<p class="note">`); err != nil {
				return err
			}
			if err := escape(sw, *entry.Note); err != nil {
				return err
			}
			if _, err := sw.WriteString(`</p>`); err != nil {
				return err
			}
		}
		if _, err := sw.WriteString(`<ul class="tags">`); err != nil {
			return err
		}
		for j := range entry.Tags {
			if _, err := sw.WriteString(`<li class="tag">`); err != nil {
				return err
			}
			if err := escape(sw, entry.Tags[j].Label); err != nil {
				return err
			}
			if _, err := sw.WriteString(`</li>`); err != nil {
				return err
			}
		}
		if _, err := sw.WriteString(`</ul></li>`); err != nil {
			return err
		}
	}
	_, err := sw.WriteString(`</ul>`)
	return err
}

func writeFormStart(sw *out, action, csrf string) error {
	if _, err := sw.WriteString(`<form method="post" action="`); err != nil {
		return err
	}
	if err := escape(sw, action); err != nil {
		return err
	}
	if _, err := sw.WriteString(`" class="form" novalidate><input type="hidden" name="csrf" value="`); err != nil {
		return err
	}
	if err := escape(sw, csrf); err != nil {
		return err
	}
	_, err := sw.WriteString(`">`)
	return err
}

func writeFormEnd(sw *out, locked bool) error {
	if locked {
		_, err := sw.WriteString(`<button type="submit" disabled>Save</button></form>`)
		return err
	}
	_, err := sw.WriteString(`<button type="submit">Save</button></form>`)
	return err
}

func writeField(sw *out, name, label, value, kind string, hint *string, maxlength int64, step *float64, required, disabled bool) error {
	if _, err := sw.WriteString(`<div class="field"><div class="control"><label for="`); err != nil {
		return err
	}
	if err := escape(sw, name); err != nil {
		return err
	}
	if _, err := sw.WriteString(`">`); err != nil {
		return err
	}
	if err := escape(sw, label); err != nil {
		return err
	}
	if _, err := sw.WriteString(`</label><input type="`); err != nil {
		return err
	}
	if err := escape(sw, kind); err != nil {
		return err
	}
	if _, err := sw.WriteString(`" id="`); err != nil {
		return err
	}
	if err := escape(sw, name); err != nil {
		return err
	}
	if _, err := sw.WriteString(`" name="`); err != nil {
		return err
	}
	if err := escape(sw, name); err != nil {
		return err
	}
	if _, err := sw.WriteString(`" value="`); err != nil {
		return err
	}
	if err := escape(sw, value); err != nil {
		return err
	}
	if _, err := sw.WriteString(`" maxlength="`); err != nil {
		return err
	}
	if err := writeInt(sw, maxlength); err != nil {
		return err
	}
	if _, err := sw.WriteString(`"`); err != nil {
		return err
	}
	if required {
		if _, err := sw.WriteString(` required`); err != nil {
			return err
		}
	}
	if disabled {
		if _, err := sw.WriteString(` disabled`); err != nil {
			return err
		}
	}
	if _, err := sw.WriteString(` aria-describedby="`); err != nil {
		return err
	}
	if err := escape(sw, name); err != nil {
		return err
	}
	if _, err := sw.WriteString(`-hint"></div>`); err != nil {
		return err
	}
	if hint != nil {
		if _, err := sw.WriteString(`<small id="`); err != nil {
			return err
		}
		if err := escape(sw, name); err != nil {
			return err
		}
		if _, err := sw.WriteString(`-hint" class="hint">`); err != nil {
			return err
		}
		if err := escape(sw, *hint); err != nil {
			return err
		}
		if _, err := sw.WriteString(`</small>`); err != nil {
			return err
		}
	}
	if _, err := sw.WriteString(`<div class="extra">`); err != nil {
		return err
	}
	if step != nil {
		if _, err := sw.WriteString(`<span class="step" data-step="`); err != nil {
			return err
		}
		if _, err := sw.Write(strconv.AppendFloat(sw.scratch[:0], *step, 'f', -1, 64)); err != nil {
			return err
		}
		if _, err := sw.WriteString(`">step</span>`); err != nil {
			return err
		}
	}
	_, err := sw.WriteString(`</div></div>`)
	return err
}

// jsonScript writes compact JSON inside a script element with <, >, &,
// U+2028, and U+2029 as \u escapes (the encoding/json HTML-safe form).
func jsonScript(w *out, data []byte) error {
	last := 0
	for i := 0; i < len(data); i++ {
		var replacement string
		switch data[i] {
		case '<':
			replacement = `\u003c`
		case '>':
			replacement = `\u003e`
		case '&':
			replacement = `\u0026`
		case 0xE2:
			if i+2 < len(data) && data[i+1] == 0x80 && (data[i+2] == 0xA8 || data[i+2] == 0xA9) {
				replacement = `\u2028`
				if data[i+2] == 0xA9 {
					replacement = `\u2029`
				}
				if _, err := w.Write(data[last:i]); err != nil {
					return err
				}
				if _, err := w.WriteString(replacement); err != nil {
					return err
				}
				i += 2
				last = i + 1
			}
			continue
		default:
			continue
		}
		if _, err := w.Write(data[last:i]); err != nil {
			return err
		}
		if _, err := w.WriteString(replacement); err != nil {
			return err
		}
		last = i + 1
	}
	_, err := w.Write(data[last:])
	return err
}

// ScriptHeavy writes the ScriptHeavy section. Href values are written as
// given: the fixture's URLs all pass the URL policy.
func ScriptHeavy(w io.Writer, page *model.ScriptPage) error {
	sw := wrap(w)
	if _, err := sw.WriteString(`<section class="lecturenotes" data-lecturenotes="`); err != nil {
		return err
	}
	if err := writeInt(sw, page.ID); err != nil {
		return err
	}
	if _, err := sw.WriteString(`"><script id="lecturenotes-data" type="application/json">`); err != nil {
		return err
	}
	if err := jsonScript(sw, page.Data); err != nil {
		return err
	}
	if _, err := sw.WriteString("\n</script><h2 class=\"lecturenotes-title\">"); err != nil {
		return err
	}
	if err := escape(sw, page.Title); err != nil {
		return err
	}
	if _, err := sw.WriteString(`</h2><p class="summary">`); err != nil {
		return err
	}
	if err := escape(sw, page.Summary); err != nil {
		return err
	}
	if _, err := sw.WriteString(`</p><ul class="links">`); err != nil {
		return err
	}
	for i := range page.Links {
		link := &page.Links[i]
		if _, err := sw.WriteString(`<li class="link"><a href="`); err != nil {
			return err
		}
		if err := escape(sw, link.Href); err != nil {
			return err
		}
		if _, err := sw.WriteString(`" title="`); err != nil {
			return err
		}
		if err := escape(sw, link.Title); err != nil {
			return err
		}
		if _, err := sw.WriteString(`">`); err != nil {
			return err
		}
		if err := escape(sw, link.Label); err != nil {
			return err
		}
		if _, err := sw.WriteString(`</a><a class="download" href="`); err != nil {
			return err
		}
		if err := escape(sw, link.Download); err != nil {
			return err
		}
		if _, err := sw.WriteString(`" download>Download</a><p class="description">`); err != nil {
			return err
		}
		if err := escape(sw, link.Description); err != nil {
			return err
		}
		if _, err := sw.WriteString(`</p></li>`); err != nil {
			return err
		}
	}
	if _, err := sw.WriteString(`</ul><script>`); err != nil {
		return err
	}
	if _, err := sw.WriteString(page.Boot); err != nil {
		return err
	}
	_, err := sw.WriteString(`</script></section>`)
	return err
}
