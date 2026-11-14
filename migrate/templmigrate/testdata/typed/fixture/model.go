package fixture

import "time"

type Role string

type Item struct {
	ID    string
	Label string
	Done  bool
}

type Order struct {
	OrderID string
	Total   int
}

type Untracked struct {
	Name string
}

type Page struct {
	Title   string
	Items   []Item
	Orders  []Order
	Plain   []Untracked
	Tags    []string
	Note    *string
	Count   int
	Visible bool
	Created time.Time
	Link    string
}

const BoxClass = "box"

func label(item Item) string { return item.Label }
