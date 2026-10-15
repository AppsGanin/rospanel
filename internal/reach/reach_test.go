package reach

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func init() { pollEvery = time.Millisecond }

// fakeAPI answers one measurement with the given results body after one in-progress
// poll, and records what was asked.
func fakeAPI(t *testing.T, results string, asked *measureReq) *httptest.Server {
	t.Helper()
	polls := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/measurements":
			if asked != nil {
				_ = json.NewDecoder(r.Body).Decode(asked)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"m1","probesCount":4}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/measurements/m1":
			polls++
			if polls == 1 {
				_, _ = w.Write([]byte(`{"status":"in-progress","results":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{"status":"finished","results":` + results + `}`))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestTCPTalliesByCountry(t *testing.T) {
	results := `[
	 {"probe":{"country":"RU","city":"Moscow","network":"Timeweb","asn":9123},
	  "result":{"status":"finished","resolvedAddress":"203.0.113.5","stats":{"avg":49.5,"rcv":2}}},
	 {"probe":{"country":"RU","city":"Kazan","network":"MTS","asn":8359},
	  "result":{"status":"finished","resolvedAddress":"203.0.113.5","stats":{"avg":null,"rcv":0}}},
	 {"probe":{"country":"CN","city":"Beijing","network":"Tencent","asn":45090},
	  "result":{"status":"failed","rawOutput":"The measurement timed out."}},
	 {"probe":{"country":"CN","city":"Changsha","network":"Unicom","asn":4837},
	  "result":{"status":"finished","resolvedAddress":"203.0.113.5","stats":{"avg":188,"rcv":2}}}
	]`
	var asked measureReq
	srv := fakeAPI(t, results, &asked)
	defer srv.Close()

	c := &Client{Base: srv.URL}
	got, err := c.TCP(context.Background(), "vpn.example.com", 443, []string{"ru", "CN", "IR"}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if asked.Type != "ping" || asked.Options.Protocol != "TCP" || asked.Options.Port != 443 {
		t.Fatalf("asked %+v", asked)
	}
	if len(asked.Locations) != 3 || asked.Locations[0].Country != "RU" || asked.Locations[0].Limit != 5 {
		t.Fatalf("locations %+v", asked.Locations)
	}
	if len(got) != 3 {
		t.Fatalf("countries %d", len(got))
	}
	ru, cn, ir := got[0], got[1], got[2]
	if ru.Code != "RU" || ru.OK != 1 || ru.Blocked != 1 || ru.Percent() != 50 {
		t.Errorf("RU %+v", ru)
	}
	// The failed probe says nothing about the server: CN is 1 of 1, not 1 of 2.
	if cn.OK != 1 || cn.Failed != 1 || cn.Percent() != 100 {
		t.Errorf("CN %+v", cn)
	}
	if ir.Answered() != 0 || ir.Percent() != -1 || ir.Probes == nil {
		t.Errorf("IR %+v", ir)
	}
	if ru.Probes[0].RTT != 49.5 || ru.Probes[1].State != StateBlocked {
		t.Errorf("RU probes %+v", ru.Probes)
	}
}

func TestRateLimitAndValidation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Authorization"), "tok") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"type":"validation_error","message":"Parameter validation failed.","params":{"target":"\"target\" must not be a private hostname"}}}`))
			return
		}
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	_, err := (&Client{Base: srv.URL}).TCP(context.Background(), "h", 443, []string{"RU"}, 1)
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("want rate limit, got %v", err)
	}
	_, err = (&Client{Base: srv.URL, Token: "tok"}).TCP(context.Background(), "10.0.0.1", 443, []string{"RU"}, 1)
	if err == nil || !strings.Contains(err.Error(), "private hostname") {
		t.Fatalf("want validation message, got %v", err)
	}
}
