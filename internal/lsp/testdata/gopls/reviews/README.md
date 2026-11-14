# Reviews fixture

A small back-office module for a fictional bookshop, Larkspur Books: the
reader reviews dashboard (`internal/reviews/ui.ktml`, `reviews.ktmlschema`,
and the `ui_present.go` presenters that map the domain into the schema's
records) and the `internal/ui/list.ktml` components it imports, with
`ui.ktmlschema`. `review.go` and `internal/staff` hold the minimal domain
types the presenters use. The gopls adapter tests copy it into a module that
replaces `github.com/karl-tye/ktml` with this repository.
