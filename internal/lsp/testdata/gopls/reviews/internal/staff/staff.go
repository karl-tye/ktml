// Package staff holds the bookshop's staff members and their roles.
package staff

type Role string

const (
	Bookseller Role = "bookseller"
	Manager    Role = "manager"
	Owner      Role = "owner"
)

type Staff struct {
	Id   string
	Name string
	Role Role
}

func (s *Staff) ID() string { return s.Id }

// Moderates reports whether the staff member may publish and remove reviews.
func (s *Staff) Moderates() bool { return s.Role == Manager || s.Role == Owner }
