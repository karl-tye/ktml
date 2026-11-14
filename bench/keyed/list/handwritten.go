package list

import (
	"html"
	"io"
	"strconv"
)

// HandwrittenFlatList is the plain handwritten lower bound: one pass, no key
// preflight.
func HandwrittenFlatList(w io.Writer, items []Item) error {
	if _, err := io.WriteString(w, `<ul class="items">`); err != nil {
		return err
	}
	for i := range items {
		if err := writeFlatItem(w, &items[i]); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, `</ul>`)
	return err
}

// HandwrittenFlatListPreflight has the same semantics as the generated
// renderer: a map preflight rejects duplicate keys before the list body.
func HandwrittenFlatListPreflight(w io.Writer, items []Item) error {
	if _, err := io.WriteString(w, `<ul class="items">`); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(items))
	for i := range items {
		if _, duplicate := seen[items[i].Id]; duplicate {
			return errDuplicate
		}
		seen[items[i].Id] = struct{}{}
	}
	for i := range items {
		if err := writeFlatItem(w, &items[i]); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, `</ul>`)
	return err
}

func writeFlatItem(w io.Writer, item *Item) error {
	if _, err := io.WriteString(w, `<li class="item" data-count="`+strconv.FormatInt(item.Count, 10)+`"><span>`); err != nil {
		return err
	}
	if _, err := io.WriteString(w, html.EscapeString(item.Title)); err != nil {
		return err
	}
	_, err := io.WriteString(w, `</span></li>`)
	return err
}

// HandwrittenNestedList is the plain nested lower bound.
func HandwrittenNestedList(w io.Writer, rows []Row) error {
	for i := range rows {
		if err := writeRow(w, &rows[i], false); err != nil {
			return err
		}
	}
	return nil
}

// HandwrittenNestedListPreflight preflights both levels with maps.
func HandwrittenNestedListPreflight(w io.Writer, rows []Row) error {
	seen := make(map[int64]struct{}, len(rows))
	for i := range rows {
		if _, duplicate := seen[rows[i].Id]; duplicate {
			return errDuplicate
		}
		seen[rows[i].Id] = struct{}{}
	}
	for i := range rows {
		if err := writeRow(w, &rows[i], true); err != nil {
			return err
		}
	}
	return nil
}

func writeRow(w io.Writer, row *Row, preflight bool) error {
	if _, err := io.WriteString(w, `<section class="row"><h2>`+html.EscapeString(row.Title)+`</h2><ul>`); err != nil {
		return err
	}
	if preflight {
		seen := make(map[int64]struct{}, len(row.Children))
		for i := range row.Children {
			if _, duplicate := seen[row.Children[i].Id]; duplicate {
				return errDuplicate
			}
			seen[row.Children[i].Id] = struct{}{}
		}
	}
	for i := range row.Children {
		if _, err := io.WriteString(w, `<li>`+html.EscapeString(row.Children[i].Label)+`</li>`); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, `</ul></section>`)
	return err
}

type duplicateError struct{}

func (duplicateError) Error() string { return "duplicate key" }

var errDuplicate error = duplicateError{}
