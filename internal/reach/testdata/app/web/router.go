// Package web registers the fixture's routes.
package web

import (
	"context"
	"io"
	"net/http"

	"example.com/app/jobs"
	"example.com/app/ui"
	"github.com/go-chi/chi/v5"
)

// Router serves the jobs pages.
func Router(h *jobs.Handler) http.Handler {
	r := chi.NewRouter()
	r.Get("/jobs", h.List)
	r.Route("/jobs/{id}", func(r chi.Router) {
		r.Get("/", h.Show)
		r.Post("/close", func(w http.ResponseWriter, req *http.Request) {
			render(w, req, ui.Notice{Text: "Closed"})
		})
	})
	r.Get("/feed", h.Feed)
	return r
}

type component interface {
	Render(ctx context.Context, w io.Writer) error
}

func render(w http.ResponseWriter, r *http.Request, c component) {
	_ = c.Render(r.Context(), w)
}
