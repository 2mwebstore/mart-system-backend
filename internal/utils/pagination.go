package utils

import (
	"strconv"

	"github.com/gin-gonic/gin"
)

const (
	DefaultPage    = 1
	DefaultPerPage = 20
	MaxPerPage     = 100
)

// PageParams is the parsed page/per_page/sort/q query params shared by every
// list endpoint.
type PageParams struct {
	Page    int
	PerPage int
	Sort    string
	Q       string
}

// Offset returns the SQL OFFSET for these page params.
func (p PageParams) Offset() int {
	return (p.Page - 1) * p.PerPage
}

// ParsePageParams reads page, per_page, sort and q from the query string,
// clamping per_page to MaxPerPage and defaulting invalid/missing values.
func ParsePageParams(c *gin.Context) PageParams {
	page, err := strconv.Atoi(c.Query("page"))
	if err != nil || page < 1 {
		page = DefaultPage
	}

	perPage, err := strconv.Atoi(c.Query("per_page"))
	if err != nil || perPage < 1 {
		perPage = DefaultPerPage
	}
	if perPage > MaxPerPage {
		perPage = MaxPerPage
	}

	return PageParams{
		Page:    page,
		PerPage: perPage,
		Sort:    c.Query("sort"),
		Q:       c.Query("q"),
	}
}
