package dashboard

import (
	"strings"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// ── one way to page a long delivery list ───────────────────────────────

// maxPage bounds every delivery page; huma refuses a bigger limit.
const maxPage = 200

// PageQuery is the paging and the filter every long delivery list takes.
// The UI keeps both in the URL, so a page is a link.
type PageQuery struct {
	Offset int    `query:"offset" minimum:"0" doc:"Where the page starts. Past the end answers the last page"`
	Limit  int    `query:"limit" minimum:"0" maximum:"200" doc:"How many per page; 0 means the list's default"`
	Q      string `query:"q" doc:"Keep only what contains this text, ignoring case"`
}

// Page says which slice of a list an answer holds.
type Page struct {
	Total      int  `json:"total" doc:"How many match, over every page"`
	Offset     int  `json:"offset"`
	Limit      int  `json:"limit"`
	NextOffset *int `json:"nextOffset,omitempty" doc:"Where the next page starts; absent on the last page"`
}

// pageOf is the page of all at offset, def per page when limit is 0. An
// offset past the end answers the last page, so a stale link is never a
// dead end.
func pageOf[T any](all []T, offset, limit, def int) ([]T, Page) {
	p := fleet.Paginate(all, limit, offset, def, maxPage)
	if len(p.Items) == 0 && len(all) > 0 {
		p = fleet.Paginate(all, p.Limit, (len(all)-1)/p.Limit*p.Limit, def, maxPage)
	}
	return p.Items, Page{Total: len(all), Offset: p.Offset, Limit: p.Limit, NextOffset: p.NextOffset}
}

// matches: q is empty, or one of fields contains it, ignoring case.
func matches(q string, fields ...string) bool {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return true
	}
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), q) {
			return true
		}
	}
	return false
}

// filter keeps the items keep says yes to.
func filter[T any](all []T, keep func(T) bool) []T {
	out := []T{}
	for _, x := range all {
		if keep(x) {
			out = append(out, x)
		}
	}
	return out
}
