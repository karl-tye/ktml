package reviews

import (
	"fmt"
	"net/url"
	"time"

	"example.com/larkspur/internal/staff"
)

// Review is a reader's review of a book, credited to the bookseller who
// recommended it.
type Review struct {
	ID         string
	Reader     string
	BookTitle  string
	Stars      float64
	Text       string
	Date       time.Time
	Published  bool
	Bookseller staff.Staff
}

func monthURL(month time.Month, year, offset int, booksellerID string) string {
	period := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC).AddDate(0, offset, 0)
	values := url.Values{"month": {fmt.Sprint(int(period.Month()))}, "year": {fmt.Sprint(period.Year())}, "bookseller": {booksellerID}}
	return "/reviews/?" + values.Encode()
}
