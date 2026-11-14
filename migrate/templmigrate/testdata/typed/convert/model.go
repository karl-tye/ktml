package convert

import "time"

type Status string

const (
	StatusOpen   Status = "open"
	StatusClosed Status = "closed"
)

// Phase is an item's progress, a closed set of constants.
type Phase int

const (
	PhaseTodo Phase = iota + 1
	PhaseDoing
	PhaseDone
)

const hiddenClass = "hidden"

type Item struct {
	ID    string
	Label string
	Done  bool
	Due   *time.Time
	Phase Phase
}

type Board struct {
	Title  string
	Items  []Item
	Tags   []string
	Owner  *string
	Status Status
	Count  int
	Ratio  float64
	Link   string
}

func Summary(board Board) string { return board.Title + " summary" }

// UserKey is the request-context key of the signed-in user's name.
type UserKey struct{}
