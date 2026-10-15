package core

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/reach"
)

func reachRep(countries ...reach.Country) ReachReport {
	return ReachReport{ServerID: model.LocalNodeID, Countries: countries}
}

func TestReachTransitions(t *testing.T) {
	t.Parallel()
	m, _ := newNotifyManager(t)
	tgt := reachTarget{id: model.LocalNodeID, name: "master", host: "vpn.example.com", port: 443}
	bad := reach.Country{Code: "RU", OK: 1, Blocked: 9}
	good := reach.Country{Code: "RU", OK: 9, Blocked: 1}
	thin := reach.Country{Code: "RU", OK: 0, Blocked: 2, Failed: 8}

	if msgs := m.reachTransitions(tgt, reachRep(bad), 50); len(msgs) != 0 {
		t.Fatalf("one bad round paged the admins: %q", msgs)
	}
	// Two probes answering is no verdict: the streak neither grows nor resets.
	if msgs := m.reachTransitions(tgt, reachRep(thin), 50); len(msgs) != 0 {
		t.Fatalf("a round with too few answers spoke: %q", msgs)
	}
	msgs := m.reachTransitions(tgt, reachRep(bad), 50)
	if len(msgs) != 1 || !strings.Contains(msgs[0], "RU") || !strings.Contains(msgs[0], "1") {
		t.Fatalf("second bad round: %q", msgs)
	}
	if msgs := m.reachTransitions(tgt, reachRep(bad), 50); len(msgs) != 0 {
		t.Fatalf("an outage already reported was reported again: %q", msgs)
	}
	if msgs := m.reachTransitions(tgt, reachRep(good), 50); len(msgs) != 1 {
		t.Fatalf("recovery: %q", msgs)
	}
	if msgs := m.reachTransitions(tgt, reachRep(good), 50); len(msgs) != 0 {
		t.Fatalf("a second good round spoke: %q", msgs)
	}
	// A bad round between good ones never pages.
	m.reachTransitions(tgt, reachRep(bad), 50)
	if msgs := m.reachTransitions(tgt, reachRep(good), 50); len(msgs) != 0 {
		t.Fatalf("recovery from an outage nobody was told about: %q", msgs)
	}
}

func TestReachConfigValidation(t *testing.T) {
	t.Parallel()
	m, _ := newNotifyManager(t)
	ok := ReachConfig{Countries: []string{"ru", "CN", "RU"}, Probes: 10, Every: 60, Threshold: 50}
	if err := m.SetReachConfig(ok, nil); err != nil {
		t.Fatal(err)
	}
	got, err := m.ReachConfig()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Countries, ",") != "RU,CN" || got.HasToken {
		t.Fatalf("stored %+v", got)
	}
	tok := "secret"
	if err := m.SetReachConfig(ok, &tok); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.ReachConfig(); !got.HasToken {
		t.Fatal("token not kept")
	}
	if err := m.SetReachConfig(ok, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.ReachConfig(); !got.HasToken {
		t.Fatal("saving without a token dropped the stored one")
	}
	// No countries switches the check off, and a check then says so.
	if err := m.SetReachConfig(ReachConfig{Probes: 10, Threshold: 50}, nil); err != nil {
		t.Fatalf("switching off: %v", err)
	}
	if _, err := m.CheckReach(t.Context(), model.LocalNodeID); err == nil || !strings.Contains(err.Error(), "выключена") {
		t.Fatalf("a check with no countries: %v", err)
	}
	for _, c := range []ReachConfig{
		{Countries: []string{"RUS"}, Probes: 1, Threshold: 50},
		{Countries: []string{"RU", "CN", "IR", "BY", "KZ", "TR", "VN"}, Probes: 1, Threshold: 50},
		{Countries: []string{"RU", "CN"}, Probes: 30, Threshold: 50}, // 60 probes > 50
		{Countries: []string{"RU"}, Probes: 5, Every: 5, Threshold: 50},
		{Countries: []string{"RU"}, Probes: 5, Threshold: 0},
	} {
		if err := m.SetReachConfig(c, nil); err == nil {
			t.Errorf("accepted %+v", c)
		}
	}
}

func TestCheckReachRecordsTheReport(t *testing.T) {
	t.Parallel()
	m, _ := newNotifyManager(t)
	gp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"m1","probesCount":1}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"finished","results":[{"probe":{"country":"RU","city":"Moscow"},
			"result":{"status":"finished","stats":{"avg":40,"rcv":2}}}]}`))
	}))
	defer gp.Close()
	m.SetReachEndpoint(gp.URL)
	cur, err := m.store.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetTLSMode(cur.TLSMode, "vpn.example.com", "vpn.example.com", cur.ACMEEmail); err != nil {
		t.Fatal(err)
	}
	if got := m.LastReach(model.LocalNodeID); got.At != 0 {
		t.Fatalf("checked before any check: %+v", got)
	}
	rep, err := m.CheckReach(t.Context(), model.LocalNodeID)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Host != "vpn.example.com" || len(rep.Countries) != 1 || rep.Countries[0].OK != 1 {
		t.Fatalf("report %+v", rep)
	}
	if got := m.LastReach(model.LocalNodeID); got.At == 0 {
		t.Fatal("the check was not kept for the server list")
	}
	if _, err := m.CheckReach(t.Context(), 999); err == nil {
		t.Fatal("checked a server that does not exist")
	}
}
