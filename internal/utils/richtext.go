package utils

import (
	"html"
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

var (
	plainPolicy   = bluemonday.StrictPolicy()
	plainBreaks   = regexp.MustCompile(`(?i)<br\s*/?>|</(p|li|h3|h4|blockquote)\s*>`)
	plainListItem = regexp.MustCompile(`(?i)<li(\s[^>]*)?>`)
)

// RichTextPlain turns a rich-text note into plain text for a place that can't
// show HTML (a Telegram alert): a line per paragraph or list item, list items
// marked "• ", entities decoded, blank lines and runs of spaces dropped. The
// result is text, not HTML: escape it again before putting it in HTML.
func RichTextPlain(s string) string {
	s = plainBreaks.ReplaceAllString(s, "\n")
	s = plainListItem.ReplaceAllString(s, "• ")
	s = html.UnescapeString(plainPolicy.Sanitize(s))
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		if line = strings.Join(strings.Fields(line), " "); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
