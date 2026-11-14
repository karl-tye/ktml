// Package templmigrate inventories templ sources for migration to KTML.
//
// It reads .templ files with the version-pinned templ parser/v2 AST and
// classifies every node with exactly one migration status (PLAN "Templ
// Migration Tool"). Generated _templ.go files are only a reference: the
// package regenerates them in memory to borrow go/types information for the
// Go expressions embedded in the templ source.
package templmigrate

import (
	"encoding/json"
	"fmt"
)

// Status is the migration status of one templ construct.
type Status int

const (
	// Converted constructs translate mechanically into KTML.
	Converted Status = iota
	// PresentationScaffolded constructs need Host Schema declarations,
	// presenter code, or both.
	PresentationScaffolded
	// ReviewRequired constructs cross a trust boundary or need a human
	// decision before conversion.
	ReviewRequired
	// Blocked constructs have no KTML equivalent today.
	Blocked
)

// Statuses lists every status in severity order.
var Statuses = []Status{Converted, PresentationScaffolded, ReviewRequired, Blocked}

var statusNames = [...]string{"converted", "presentation_scaffolded", "review_required", "blocked"}

func (s Status) String() string {
	if s < 0 || int(s) >= len(statusNames) {
		return fmt.Sprintf("status(%d)", int(s))
	}
	return statusNames[s]
}

// MarshalJSON writes the PLAN status name.
func (s Status) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

// UnmarshalJSON reads a PLAN status name.
func (s *Status) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return err
	}
	for i, candidate := range statusNames {
		if candidate == name {
			*s = Status(i)
			return nil
		}
	}
	return fmt.Errorf("unknown status %q", name)
}

func maxStatus(a, b Status) Status {
	if b > a {
		return b
	}
	return a
}
