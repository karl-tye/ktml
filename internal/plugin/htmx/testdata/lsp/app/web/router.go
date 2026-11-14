package web

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

type Router struct {
	*chi.Mux
}

func NewRouter(vendors *Vendors) http.Handler {
	mux := Router{chi.NewRouter()}
	mux.Get("/", home)
	mux.Route("/vendors", func(r chi.Router) {
		r.Get("/", vendors.List)
		r.Post("/{id}/approve", vendors.Approve)
		r.Get("/{id}/invoices/{invoiceID}", vendors.Invoice)
	})
	mux.Handle("/static/*", static)
	mux.Get(dynamicPath(), home)
	return mux
}
