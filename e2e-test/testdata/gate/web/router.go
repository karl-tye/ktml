// Package web registers the bookshop back office's reviews routes.
package web

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

type ReviewHandler struct{}

func (ReviewHandler) List(http.ResponseWriter, *http.Request)    {}
func (ReviewHandler) Create(http.ResponseWriter, *http.Request)  {}
func (ReviewHandler) Approve(http.ResponseWriter, *http.Request) {}
func (ReviewHandler) Delete(http.ResponseWriter, *http.Request)  {}

func Router() chi.Router {
	reviews := ReviewHandler{}
	r := chi.NewRouter()
	r.Route("/reviews", func(r chi.Router) {
		r.Get("/", reviews.List)
		r.Post("/", reviews.Create)
		r.Post("/{id}/approve", reviews.Approve)
		r.Delete("/{id}", reviews.Delete)
	})
	return r
}
