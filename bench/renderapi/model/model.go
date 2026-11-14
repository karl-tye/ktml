// Package model holds the view types of the templ baseline and the
// handwritten lower bound. The KTML trees use the records generated from
// ktml/**/*.ktmlschema instead; the benchmark fixtures fill both from the same
// data.
package model

import "encoding/json"

// Tag is one item of an Entry's nested list.
type Tag struct {
	ID    int64
	Label string
}

// Entry is one ListHeavy item.
type Entry struct {
	ID    string
	Title string
	Score int64
	Note  *string
	Tags  []Tag
}

// FormFields is the number of FormHeavy fields.
const FormFields = 40

// FormValues holds the FormHeavy field values.
type FormValues struct {
	Fields [FormFields]string
	Help   string
}

// Link is one ScriptHeavy link.
type Link struct {
	ID          int64
	Label       string
	Title       string
	Description string
	Href        string
	Download    string
}

// ScriptPage is the ScriptHeavy view.
type ScriptPage struct {
	ID      int64
	Title   string
	Summary string
	// Data is compact JSON that is not yet HTML-escaped: it still contains
	// raw <, >, and &, so every implementation must escape it for a script
	// element.
	Data  json.RawMessage
	Links []Link
	Boot  string
}
