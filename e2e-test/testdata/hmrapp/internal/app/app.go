// Package app holds the handlers of the HMR browser fixture: a full page and
// HTMX fragments rendered from generated KTML components.
package app

import (
	"bytes"
	"net/http"

	"example.com/hmrapp/web"
	"github.com/karl-tye/ktml"
)

var jobs = []web.Job{
	{Id: "j1", Title: "Boiler service", Status: "open"},
	{Id: "j2", Title: "Leaking tap", Status: "open"},
	{Id: "j3", Title: "Fuse box", Status: "closed"},
	{Id: "j4", Title: "Radiator bleed", Status: "open"},
}

func board() web.Board {
	reversed := make([]web.Job, 0, len(jobs))
	var open []web.Job
	for index := len(jobs) - 1; index >= 0; index-- {
		reversed = append(reversed, jobs[index])
	}
	for _, job := range jobs {
		if job.Status == "open" {
			open = append(open, job)
		}
	}
	return web.Board{Title: "Jobs", Jobs: jobs, Reversed: reversed, Open: open}
}

// Routes serves the dashboard page and its fragments.
func Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		render(w, r, web.Dashboard{Board: board()})
	})
	// Go passes a page component into the layout's content slot.
	mux.HandleFunc("GET /planner", func(w http.ResponseWriter, r *http.Request) {
		render(w, r, web.Layout{Title: "Planner", Children: web.Planner{Board: board()}})
	})
	mux.HandleFunc("GET /activity", func(w http.ResponseWriter, r *http.Request) {
		render(w, r, web.Activity{Entries: []web.Entry{
			{Id: "a1", Kind: "call", Text: "Customer rang"},
			{Id: "a2", Kind: "call", Text: "Engineer rang back"},
		}})
	})
	mux.HandleFunc("GET /notice", func(w http.ResponseWriter, r *http.Request) {
		render(w, r, web.Notice{Text: "Welcome"})
	})
	mux.HandleFunc("GET /extra", func(w http.ResponseWriter, r *http.Request) {
		render(w, r, web.Extra{Text: "Loaded by HTMX"})
	})
	mux.Handle("GET /static/", http.FileServer(http.Dir(".")))
	return mux
}

// render buffers a component so a render error is a clean 500.
func render(w http.ResponseWriter, r *http.Request, component ktml.Component) {
	var output bytes.Buffer
	if err := component.Render(r.Context(), &output); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = output.WriteTo(w)
}
