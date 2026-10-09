package plugin

import (
	"context"
	"strings"
	"testing"
)

// Check in the editor loads the code: what would fail at the start fails here.
func TestProbe(t *testing.T) {
	h := newHarness(t)
	man := `{"id": "probe", "version": "1.0.0", "api": 1, "name": "Probe", "permissions": ["users.view"],
		"provides": {"events": ["user.created"], "cron": [{"name": "daily", "schedule": "0 9 * * *"}]}}`
	good := `export function onEvent(e) {} export function daily() {}`
	for _, c := range []struct {
		main  string
		extra map[string]string
		want  string
	}{
		{good, nil, ""},
		{`export function onEvent(e) { this is not js }`, nil, "SyntaxError"},
		{`throw new Error("boom at load"); export function onEvent() {} export function daily() {}`, nil, "boom at load"},
		{`export function onEvent(e) {}`, nil, "does not export what plugin.json declares: daily"},
		{`const x = panel.api("GET", "/v1/users"); ` + good, nil, "not available while main.js loads"},
		{good, map[string]string{"migrations/0001_init.sql": "CREATE TABLE ("}, "syntax error"},
		{`const lang = panel.config.lang || "ru"; ` + good, nil, ""}, // config is there, empty
	} {
		err := h.host.Probe(context.Background(), pkg(t, man, c.main, c.extra))
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%q: %v", c.main, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%q: want %q, got %v", c.main, c.want, err)
		}
	}
}
