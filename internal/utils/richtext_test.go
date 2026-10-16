package utils

import (
	"strings"
	"testing"
)

func TestSanitizeRichText(t *testing.T) {
	cases := map[string]string{
		"<p>Deliver <strong>Friday</strong></p>":                    "<p>Deliver <strong>Friday</strong></p>",
		"<p>a</p><script>alert(1)</script>":                         "<p>a</p>",
		`<p onclick="x()">b</p>`:                                    "<p>b</p>",
		`<img src=x onerror=alert(1)>`:                              "",
		"<p></p>":                                                   "",
		"<ul><li>one</li><li>two</li></ul>":                         "<ul><li>one</li><li>two</li></ul>",
		"<ul><li><p>a</p></li></ul><p></p>":                         "<ul><li><p>a</p></li></ul>",
		"<p>x</p><p><br></p><p></p>":                                "<p>x</p>",
		"plain text, no tags":                                       "plain text, no tags",
		`<a href="javascript:alert(1)">x</a>`:                       "x",
		`<p style="color:red"><span class="x">kept text</span></p>`: "<p>kept text</p>",
	}
	for in, want := range cases {
		if got := SanitizeRichText(in); got != want {
			t.Errorf("SanitizeRichText(%q) = %q, want %q", in, got, want)
		}
	}
	link := SanitizeRichText(`<a href="https://example.com">site</a>`)
	if !strings.Contains(link, `href="https://example.com"`) || !strings.Contains(link, `target="_blank"`) || !strings.Contains(link, "noreferrer") {
		t.Errorf("safe link not kept as expected: %q", link)
	}
}
