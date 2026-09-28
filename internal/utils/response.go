package utils

import (
	"reflect"

	"github.com/gin-gonic/gin"
)

// Meta carries pagination info in the response envelope.
type Meta struct {
	Page       int   `json:"page"`
	PerPage    int   `json:"per_page"`
	Total      int64 `json:"total"`
	TotalPages int   `json:"total_pages"`
	// Summary carries whole-result figures a paged screen still needs (KPI
	// cards, footer totals) that can't be summed from just the current page.
	Summary interface{} `json:"summary,omitempty"`
}

// Envelope is the API-wide response shape: { success, data, meta, error }.
type Envelope struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data"`
	Meta    *Meta       `json:"meta,omitempty"`
	Error   *ErrorBody  `json:"error"`
}

// emptyIfNil turns a nil slice into an empty one (and does the same for the
// values of a top-level map such as gin.H) so lists always serialise as [],
// never null — clients iterate them without a null check.
func emptyIfNil(data interface{}) interface{} {
	v := reflect.ValueOf(data)
	switch v.Kind() {
	case reflect.Slice:
		if v.IsNil() {
			return reflect.MakeSlice(v.Type(), 0, 0).Interface()
		}
	case reflect.Map:
		if m, ok := data.(gin.H); ok {
			for k, val := range m {
				m[k] = emptyIfNil(val)
			}
		}
	}
	return data
}

// OK writes a successful envelope with no pagination meta.
func OK(c *gin.Context, status int, data interface{}) {
	c.JSON(status, Envelope{Success: true, Data: emptyIfNil(data), Error: nil})
}

// OKWithMeta writes a successful envelope including pagination meta.
func OKWithMeta(c *gin.Context, status int, data interface{}, meta Meta) {
	if meta.PerPage > 0 {
		meta.TotalPages = int((meta.Total + int64(meta.PerPage) - 1) / int64(meta.PerPage))
	}
	if meta.TotalPages < 1 {
		meta.TotalPages = 1
	}
	c.JSON(status, Envelope{Success: true, Data: emptyIfNil(data), Meta: &meta, Error: nil})
}
