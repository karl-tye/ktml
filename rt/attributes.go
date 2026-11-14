package rt

import (
	"strconv"
	"strings"

	"github.com/karl-tye/ktml"
)

// Attributes writes a forwarded bag as attributes of the element being
// written: each entry with a leading space, in bag order, through the
// escaper of the context it was built for. Entries named in owned, which
// the element writes itself, are dropped; names compare case-insensitively.
func Attributes(w *Writer, bag ktml.Attributes, owned []string) error {
next:
	for index := range bag.Len() {
		entry := bag.At(index)
		for _, name := range owned {
			if strings.EqualFold(name, entry.Name()) {
				continue next
			}
		}
		if entry.Kind() == ktml.AttributePresence {
			if !entry.Present() {
				continue
			}
			if err := raw(w, " "); err != nil {
				return err
			}
			if err := raw(w, entry.Name()); err != nil {
				return err
			}
			continue
		}
		if err := raw(w, " "); err != nil {
			return err
		}
		if err := raw(w, entry.Name()); err != nil {
			return err
		}
		if err := raw(w, `="`); err != nil {
			return err
		}
		var err error
		switch entry.Kind() {
		case ktml.AttributeURL:
			err = URL(w, ktml.NewURL(entry.Value()))
		case ktml.AttributeScriptBool:
			err = raw(w, entry.Value())
		default:
			err = escape(w, entry.Value())
		}
		if err != nil {
			return err
		}
		if err := raw(w, `"`); err != nil {
			return err
		}
	}
	return nil
}

// FormatInt returns an integer as it is written in text or an attribute.
func FormatInt(value int64) string {
	return strconv.FormatInt(value, 10)
}

// FormatNumber returns a number as it is written in text or an attribute.
func FormatNumber(value float64) string {
	return string(AppendNumber(nil, value))
}
