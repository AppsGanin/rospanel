package plugin

import (
	"net/http/httptest"
	"testing"
)

// Nothing a plugin answers on the panel's origin may run as a script there: every
// JavaScript type is served as text.
func TestWriteHTTPNoScript(t *testing.T) {
	for _, ct := range []string{"", "text/javascript", "application/x-ecmascript", "text/jscript", "text/livescript; charset=utf-8", "TEXT/JavaScript"} {
		w := httptest.NewRecorder()
		WriteHTTP(w, &HTTPResponse{Status: 200, Headers: map[string]string{"Content-Type": ct}, Body: "alert(1)"})
		if got := w.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
			t.Errorf("%q served as %q", ct, got)
		}
	}
	w := httptest.NewRecorder()
	WriteHTTP(w, &HTTPResponse{Status: 200, Headers: map[string]string{"Content-Type": "application/json"}, Body: "{}"})
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("json served as %q", got)
	}
}
