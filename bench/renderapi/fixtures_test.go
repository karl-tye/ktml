package renderapi_test

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/karl-tye/ktml"
	blist "github.com/karl-tye/ktml/bench/renderapi/b/list"
	bscript "github.com/karl-tye/ktml/bench/renderapi/b/script"
	hlist "github.com/karl-tye/ktml/bench/renderapi/h/list"
	hscript "github.com/karl-tye/ktml/bench/renderapi/h/script"
	"github.com/karl-tye/ktml/bench/renderapi/model"
)

// The fixtures are built once, outside the timed loops. Each KTML tree gets
// its own copy in its generated record types; templ and the handwritten
// lower bound read the model types.

const nestedLabel = `Leaf <"label"> & 'co'`

func listEntries(n int) []model.Entry {
	entries := make([]model.Entry, n)
	for i := range entries {
		entries[i] = model.Entry{
			ID:    "entry-" + strconv.Itoa(i),
			Title: "Entry <" + strconv.Itoa(i) + "> & \"co\"",
			Score: int64(i * 7),
			Tags:  []model.Tag{{ID: 1, Label: "one"}, {ID: 2, Label: "two & a half"}, {ID: 3, Label: "<three>"}},
		}
		if i%2 == 0 {
			note := "Note for " + strconv.Itoa(i) + ": 'quoted' & <b>bold</b>"
			entries[i].Note = &note
		}
	}
	return entries
}

func formValues() *model.FormValues {
	values := &model.FormValues{Help: `Help: <b>"bold"</b> & 'more'`}
	for i := range values.Fields {
		values.Fields[i] = "value " + strconv.Itoa(i) + ` "quoted" & 'single' </input><script>alert(1)</script>`
	}
	return values
}

const (
	formAction = "/jobs/42/edit?mode=full&return=%2Fjobs"
	formCSRF   = `tok&en<"'>`
)

// highEscape is text where about one byte in five is escaped.
func highEscape(i, size int) string {
	var builder strings.Builder
	for builder.Len() < size {
		builder.WriteString(`<"` + strconv.Itoa(i) + `"> & 'q' `)
	}
	return builder.String()
}

func scriptPage() *model.ScriptPage {
	type slide struct {
		ID    int      `json:"id"`
		Title string   `json:"title"`
		Notes string   `json:"notes"`
		Tags  []string `json:"tags"`
	}
	type lecturenotes struct {
		ID     int     `json:"id"`
		Title  string  `json:"title"`
		Owner  string  `json:"owner"`
		Slides []slide `json:"slides"`
	}
	data := lecturenotes{ID: 512, Title: "Wk 4 talk <draft> & notes", Owner: "Labs & Design"}
	for i := range 20 {
		data.Slides = append(data.Slides, slide{ID: i, Title: "Slide <" + strconv.Itoa(i) + ">", Notes: "Notes & 'quotes' </script>", Tags: []string{"a&b", "<c>"}})
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false) // keep raw <, >, and & for the script escaper
	if err := encoder.Encode(data); err != nil {
		panic(err)
	}
	page := &model.ScriptPage{
		ID:      512,
		Title:   `Lecture deck <512> "W4" & 'review'`,
		Summary: highEscape(512, 1024),
		Data:    json.RawMessage(bytes.TrimSuffix(encoded.Bytes(), []byte("\n"))),
		Boot:    strings.Repeat(`window.lecturenotes && window.lecturenotes.boot(document.getElementById("lecturenotes-data"));`, 5),
	}
	for i := range 40 {
		page.Links = append(page.Links, model.Link{
			ID:          int64(i),
			Label:       "Slide " + strconv.Itoa(i) + " & notes",
			Title:       `Slide "` + strconv.Itoa(i) + `" <draft>`,
			Description: highEscape(i, 160),
			Href:        "/lecture-notes/512/slides/" + strconv.Itoa(i) + "?view=full&lang=en",
			Download:    "/files/slide-" + strconv.Itoa(i) + ".pdf?sig=a1&exp=99",
		})
	}
	return page
}

// scriptData is the JSONValue the presenter passes to the KTML trees: the
// same compact JSON that templ.JSONScript re-encodes, with the newline its
// json.Encoder writes.
func scriptData(page *model.ScriptPage) ktml.JSONValue {
	return ktml.TrustJSON(string(page.Data) + "\n")
}

func hEntries(entries []model.Entry) []hlist.Entry {
	out := make([]hlist.Entry, len(entries))
	for i, e := range entries {
		tags := make([]hlist.Tag, len(e.Tags))
		for j, t := range e.Tags {
			tags[j] = hlist.Tag{Id: t.ID, Label: t.Label}
		}
		out[i] = hlist.Entry{Id: e.ID, Title: e.Title, Score: e.Score, Note: e.Note, Tags: tags}
	}
	return out
}

func bEntries(entries []model.Entry) []blist.Entry {
	out := make([]blist.Entry, len(entries))
	for i, e := range entries {
		tags := make([]blist.Tag, len(e.Tags))
		for j, t := range e.Tags {
			tags[j] = blist.Tag{Id: t.ID, Label: t.Label}
		}
		out[i] = blist.Entry{Id: e.ID, Title: e.Title, Score: e.Score, Note: e.Note, Tags: tags}
	}
	return out
}

func hLinks(links []model.Link) []hscript.Link {
	out := make([]hscript.Link, len(links))
	for i, l := range links {
		out[i] = hscript.Link{Id: l.ID, Label: l.Label, Title: l.Title, Description: l.Description, Href: ktml.NewURL(l.Href), Download: ktml.TrustURL(l.Download)}
	}
	return out
}

func bLinks(links []model.Link) []bscript.Link {
	out := make([]bscript.Link, len(links))
	for i, l := range links {
		out[i] = bscript.Link{Id: l.ID, Label: l.Label, Title: l.Title, Description: l.Description, Href: ktml.NewURL(l.Href), Download: ktml.TrustURL(l.Download)}
	}
	return out
}
