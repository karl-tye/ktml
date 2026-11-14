# Phase 2 exit-gate fixture

`TestLSP_GIVENAReviewsComponent_WHENAuthored_*` copies this directory, writes
a `ktml.config.json` that loads the gohtml, alpine, and htmx plugins and the
Tailwind adapter, and authors `reviews/ui.ktml` through `ktml lsp`.

The fixture is the reader reviews dashboard of a fictional bookshop,
Larkspur Books:

- `reviews/` and `ui/list.ktml` with their Host Schemas are the dashboard's
  components and the shared list components it imports.
- `site.css` is the Tailwind CSS v4 entry with the bookshop's `paper` and
  `ink` themes, which define colours such as `bg-surface0`.
- `web/router.go` registers the `/reviews` Chi routes the components'
  `hx-*` attributes request.
