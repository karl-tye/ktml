package widgets

// Item is a handwritten domain type the templates render.
type Item struct {
	ID    string
	Label string
	Done  bool
}

// UserKey is the request-context key Greeting reads.
type UserKey struct{}

// label is an unexported helper: only an in-place presenter can call it.
func label(item Item) string {
	if item.Done {
		return item.Label + " (done)"
	}
	return item.Label
}
