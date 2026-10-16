package utils

import (
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
)

// Rich-text notes (invoices, purchase orders) arrive as HTML from the
// RichTextEditor. Only the formatting the editor can make survives: no
// scripts, styles, event handlers, images or iframes. Links keep http(s) /
// mailto only and open safely in a new tab.
var richTextPolicy = func() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements("p", "br", "strong", "b", "em", "i", "u", "s", "ul", "ol", "li", "blockquote", "h3", "h4")
	p.AllowAttrs("href").OnElements("a")
	p.AllowURLSchemes("http", "https", "mailto")
	p.RequireParseableURLs(true)
	p.AddTargetBlankToFullyQualifiedLinks(true)
	p.RequireNoReferrerOnLinks(true)
	return p
}()

var emptyRichText = regexp.MustCompile(`^(\s|<p>\s*</p>|<p><br\s*/?></p>|<br\s*/?>)*$`)

// The editor leaves an empty paragraph after a list (its "trailing node").
var trailingEmptyParas = regexp.MustCompile(`(\s*<p>\s*(<br\s*/?>)?\s*</p>)+\s*$`)

// SanitizeRichText cleans a note's HTML. A note with no visible text (the
// editor's empty paragraph) becomes "".
func SanitizeRichText(html string) string {
	clean := strings.TrimSpace(richTextPolicy.Sanitize(html))
	if emptyRichText.MatchString(clean) {
		return ""
	}
	return trailingEmptyParas.ReplaceAllString(clean, "")
}
