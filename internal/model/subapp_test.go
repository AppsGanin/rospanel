package model

import "testing"

func TestSubAppLinkChecks(t *testing.T) {
	for _, l := range []string{"myapp://import/{url}", "my.app+x://a?u={url_enc}", "https://x.example/?s={url_b64}"} {
		if !ValidSubCustomAppLink(l) {
			t.Errorf("%q refused", l)
		}
	}
	for _, l := range []string{
		"myapp://import/", "javascript:{url}", "Data:{url}", "x://\u202e{url}", "x://\x00{url}", "x://a\\b{url}",
		"x://\u00a0{url}", "{url}", ":{url}", "x:" + string(make([]byte, 1001)) + "{url}",
	} {
		if ValidSubCustomAppLink(l) {
			t.Errorf("%q accepted", l)
		}
	}
	for _, l := range []string{"https://apps.apple.com/app/id1", "http://x.example/a.apk"} {
		if !ValidWizardLink(l) {
			t.Errorf("%q refused", l)
		}
	}
	for _, l := range []string{"javascript:alert(1)", "https://", "ftp://x.example", "https://x.example/\u202e", "//x.example", "https://x.example/'"} {
		if ValidWizardLink(l) {
			t.Errorf("%q accepted", l)
		}
	}
}
