package core

import (
	"context"
	"errors"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// A proxy-list host that blinks must not empty the lane. The pool used to be rebuilt
// from whatever the refresh fetched, so one failed fetch replaced a working pool
// with nothing until the next refresh — up to the whole interval. An empty lane is
// one the config cannot build: its traffic leaked out directly, and under
// StrictEgress it is dropped, while every upstream the list named was alive.
func TestAFailedListFetchKeepsTheLastGoodProxies(t *testing.T) {
	var answer func() ([]string, error)
	prev := fetchProxyList
	fetchProxyList = func(context.Context, string) ([]string, error) { return answer() }
	t.Cleanup(func() { fetchProxyList = prev })

	m := &Manager{}
	rc := model.RoutingConfig{Lanes: []model.EgressLane{{
		ID: "ru", Name: "RU", Enabled: true, URLs: []string{"https://lists.example/ru.txt"},
	}}}
	count := func() int { return len(m.buildProxies(rc)["ru"]) }

	answer = func() ([]string, error) { return nil, errors.New("connection refused") }
	if n := count(); n != 0 {
		t.Fatalf("a list that has never answered produced %d proxies — there is nothing to keep yet", n)
	}

	answer = func() ([]string, error) {
		return []string{"socks5://10.0.0.1:1080", "socks5://10.0.0.2:1080"}, nil
	}
	if n := count(); n != 2 {
		t.Fatalf("a good fetch produced %d proxies, want 2", n)
	}

	answer = func() ([]string, error) { return nil, errors.New("503 Service Unavailable") }
	if n := count(); n != 2 {
		t.Errorf("the list host failed once and the lane went from 2 proxies to %d", n)
	}

	// A good fetch is authoritative, including one that lists fewer — or none.
	answer = func() ([]string, error) { return []string{"socks5://10.0.0.3:1080"}, nil }
	if n := count(); n != 1 {
		t.Errorf("the list shrank to 1 and the lane kept %d", n)
	}
	answer = func() ([]string, error) { return []string{}, nil }
	if n := count(); n != 0 {
		t.Errorf("the list was emptied on purpose and the lane kept %d", n)
	}
}
