// Presenters map the reviews domain into the Host Schema records that the
// KTML components in ui.ktml render.

package reviews

import (
	"fmt"
	"time"

	"example.com/larkspur/internal/staff"
	"github.com/karl-tye/ktml"
)

// NewPageView presents Page.
func NewPageView(reviews []Review, booksellers []staff.Staff, actor staff.Staff, month time.Month, year int, now time.Time) PageView {
	var v PageView
	v.Dashboard = NewDashboardView(reviews, booksellers, actor, month, year, "", now)
	return v
}

// NewDashboardView presents Dashboard.
func NewDashboardView(reviews []Review, booksellers []staff.Staff, actor staff.Staff, month time.Month, year int, booksellerID string, now time.Time) DashboardView {
	var v DashboardView
	v.Month = month.String()
	v.Year = fmt.Sprint(year)
	v.PreviousMonthHref = ktml.NewURL(monthURL(month, year, -1, booksellerID))
	v.NextMonthHref = ktml.NewURL(monthURL(month, year, 1, booksellerID))
	v.NewReviewForm = NewNewReviewFormView(booksellers, actor, month, year, booksellerID, now)
	v.ReviewsGrid = NewReviewsGridView(reviews, actor, month, year, booksellerID)
	return v
}

// NewNewReviewFormView presents NewReviewForm.
func NewNewReviewFormView(booksellers []staff.Staff, actor staff.Staff, month time.Month, year int, booksellerID string, now time.Time) NewReviewFormView {
	var v NewReviewFormView
	v.Month = fmt.Sprint(int(month))
	v.Year = fmt.Sprint(year)
	v.BooksellerID = booksellerID
	v.CanChooseBookseller = actor.Moderates()
	if v.CanChooseBookseller {
		for _, bookseller := range booksellers {
			var item NewReviewFormBookseller
			item.Key = bookseller.Id
			item.Id = bookseller.ID()
			item.Selected = bookseller.ID() == booksellerID
			item.Name = bookseller.Name
			v.Booksellers = append(v.Booksellers, item)
		}
	} else {
		v.ActorID = actor.ID()
	}
	v.LatestDate = now.Format("2006-01-02")
	return v
}

// NewReviewsGridView presents ReviewsGrid.
func NewReviewsGridView(reviews []Review, actor staff.Staff, month time.Month, year int, booksellerID string) ReviewsGridView {
	var v ReviewsGridView
	v.ReviewCount = int64(len(reviews))
	v.NoReviews = len(reviews) == 0
	for _, review := range reviews {
		var item ReviewsGridReview
		item.Key = review.ID
		item.ReviewCard = NewReviewCardView(review, actor, month, year, booksellerID)
		v.Reviews = append(v.Reviews, item)
	}
	return v
}

// NewReviewCardView presents ReviewCard.
func NewReviewCardView(review Review, actor staff.Staff, month time.Month, year int, booksellerID string) ReviewCardView {
	var v ReviewCardView
	v.ReviewID = review.ID
	v.Reader = review.Reader
	v.BookTitle = review.BookTitle
	v.StarRating = newStarRatingView(review.Stars)
	v.ReviewDate = review.Date.Format("2 Jan 2006")
	v.Text = review.Text
	v.Published = review.Published
	v.CanPublish = !review.Published && actor.Moderates()
	v.CanDelete = actor.Moderates()
	if v.CanPublish || v.CanDelete {
		v.Month = fmt.Sprint(int(month))
		v.Year = fmt.Sprint(year)
		v.BooksellerID = booksellerID
	}
	return v
}

// newStarRatingView presents starRating.
func newStarRatingView(stars float64) StarRatingView {
	var v StarRatingView
	for i := 1; i <= 5; i++ {
		var star StarRatingStar
		star.Key = int64(i)
		star.Filled = float64(i) <= stars
		v.Stars = append(v.Stars, star)
	}
	v.RatingLabel = fmt.Sprintf("%.1f", stars)
	return v
}
