package handlers

import "testing"

func TestValidLogo(t *testing.T) {
	for in, want := range map[string]bool{
		"":                                   true, // remove
		"https://cdn.example.com/logo.png":   true,
		"data:image/png;base64,iVBORw0KGgo=": true,
		"data:image/svg+xml;base64,PHN2Zz4=": false, // svg can carry script
		"data:image/png;base64,***":          false,
		"javascript:alert(1)":                false,
	} {
		if got := validLogo(in); got != want {
			t.Errorf("validLogo(%q) = %v, want %v", in, got, want)
		}
	}
	if a, b := toBrandingDTO("", "data:image/png;base64,AAAA"), toBrandingDTO("", "data:image/png;base64,BBBB"); a.LogoVersion == "" || a.LogoVersion == b.LogoVersion {
		t.Errorf("a new logo needs a new version: %q vs %q", a.LogoVersion, b.LogoVersion)
	}
}
