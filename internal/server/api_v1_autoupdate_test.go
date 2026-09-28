package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

// The auto-update schedule is set and read over /v1; a broken cron is refused before
// it can silently mean "never".
func TestAPIAutoUpdateSchedule(t *testing.T) {
	t.Parallel()
	h, _, st := nodeAPITestServer(t)
	base, key := apiFixture(t, h, st)
	if rec := apiDo(t, h, http.MethodPost, base+"/v1/system/auto-update", key, `{"cron":"every night"}`); rec.Code == http.StatusOK {
		t.Fatalf("a broken cron was saved: %s", rec.Body.String())
	}
	if rec := apiDo(t, h, http.MethodPost, base+"/v1/system/auto-update", key, `{"cron":"0 4 * * *","nodes":false}`); rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	// A field left out keeps its value.
	if rec := apiDo(t, h, http.MethodPost, base+"/v1/system/auto-update", key, `{"nodes":true}`); rec.Code != http.StatusOK {
		t.Fatalf("partial save: %d", rec.Code)
	}
	var got struct {
		Data struct {
			Cron  string `json:"cron"`
			Nodes bool   `json:"nodes"`
		} `json:"data"`
	}
	rec := apiGet(t, h, base+"/v1/system/auto-update", key)
	if json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.Data.Cron != "0 4 * * *" || !got.Data.Nodes {
		t.Fatalf("read back: %s", rec.Body.String())
	}
}
