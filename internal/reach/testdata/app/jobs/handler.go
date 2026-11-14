// Package jobs holds the fixture's handlers.
package jobs

import (
	"net/http"

	"example.com/app/ui"
)

// Feeder streams feed items.
type Feeder interface {
	Items() []any
}

// Handler serves jobs.
type Handler struct {
	feed Feeder
}

// List renders the list page; ListPage is a component of this package.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	_ = ListPage{View: ListView{}}.Render(r.Context(), w)
}

// Show renders a job through a helper.
func (h *Handler) Show(w http.ResponseWriter, r *http.Request) {
	_ = showPage().Render(r.Context(), w)
}

func showPage() JobDetail {
	return JobDetail{Title: "Job"}
}

// Feed reaches its component through an interface method.
func (h *Handler) Feed(w http.ResponseWriter, r *http.Request) {
	_ = h.feed.Items()
}

type sseFeed struct{}

func (sseFeed) Items() []any {
	return []any{ui.FeedItem{Text: "Item"}}
}

// Unused builds a component nothing routes to.
func Unused() any { return Orphan{} }
