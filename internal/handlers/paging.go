package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"com-mart/backend/internal/utils"
)

// maxUnpaged caps a list fetched without page/per_page (dropdown sources and
// the POS grid, which need the whole set to filter locally).
const maxUnpaged = 5000

// pager is the paging mode of one list request. A request that sends `page`
// or `per_page` gets one page (default 20, max 100 per page) plus
// meta {page, per_page, total, total_pages, summary}. A request that sends
// neither gets the whole list (capped) with no meta, so pickers and the till
// keep working unchanged.
type pager struct {
	utils.PageParams
	on bool
}

func pagerOf(c *gin.Context) pager {
	_, hasPage := c.GetQuery("page")
	_, hasPer := c.GetQuery("per_page")
	return pager{PageParams: utils.ParsePageParams(c), on: hasPage || hasPer}
}

// apply limits a query to the requested page.
func (pg pager) apply(q *gorm.DB) *gorm.DB {
	if pg.on {
		return q.Limit(pg.PerPage).Offset(pg.Offset())
	}
	return q.Limit(maxUnpaged)
}

// respond writes rows for the current mode. summary is only sent when paged.
func (pg pager) respond(c *gin.Context, rows interface{}, total int64, summary interface{}) {
	if !pg.on {
		utils.OK(c, http.StatusOK, rows)
		return
	}
	utils.OKWithMeta(c, http.StatusOK, rows, utils.Meta{Page: pg.Page, PerPage: pg.PerPage, Total: total, Summary: summary})
}

// slicePage returns the current page of an already-built list (used by the
// aggregate reports, whose rows come from GROUP BY and are cut in memory).
func slicePage[T any](pg pager, rows []T) []T {
	if !pg.on {
		return rows
	}
	start := pg.Offset()
	if start >= len(rows) {
		return []T{}
	}
	end := start + pg.PerPage
	if end > len(rows) {
		end = len(rows)
	}
	return rows[start:end]
}

// likeArg wraps a search term for LIKE.
func likeArg(s string) string { return "%" + s + "%" }
