package xray

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// inboundsCfg is a config with the API inbound, the TCP-TLS lane and the given custom
// inbounds (tag → protocol, port), each holding the given VLESS users.
func inboundsCfg(t *testing.T, users []string, custom [][3]any, routing string) []byte {
	t.Helper()
	clients := []any{}
	for _, e := range users {
		clients = append(clients, map[string]any{"id": "id-" + e, "email": e})
	}
	in := []any{
		map[string]any{"tag": "api", "listen": "127.0.0.1", "port": 10085, "protocol": "dokodemo-door", "settings": map[string]any{"address": "127.0.0.1"}},
		map[string]any{"tag": "vless-in", "port": 443, "protocol": "vless", "settings": map[string]any{"clients": clients, "decryption": "none"}},
	}
	for _, c := range custom {
		tag, protocol, port := c[0].(string), c[1].(string), c[2].(int)
		settings := map[string]any{"clients": clients, "decryption": "none"}
		switch protocol {
		case "hysteria":
			hy := []any{}
			for _, e := range users {
				hy = append(hy, map[string]any{"auth": "pw-" + e, "email": e})
			}
			settings = map[string]any{"version": 2, "users": hy}
		case "trojan":
			tr := []any{}
			for _, e := range users {
				tr = append(tr, map[string]any{"password": "pw-" + e, "email": e})
			}
			settings = map[string]any{"clients": tr}
		}
		in = append(in, map[string]any{"tag": tag, "port": port, "protocol": protocol, "settings": settings})
	}
	cfg := map[string]any{
		"inbounds":  in,
		"outbounds": []any{map[string]any{"protocol": "freedom", "tag": routing}},
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPlanInboundChanges(t *testing.T) {
	t.Parallel()
	u := []string{"u1", "u2"}
	one := [][3]any{{"custom-1", "vless", 2443}}
	base := inboundsCfg(t, u, one, "direct")

	type want struct {
		ok      bool
		remove  []string
		add     []string
		changes []string
	}
	cases := []struct {
		name string
		next []byte
		want want
	}{
		{"an inbound added", inboundsCfg(t, u, [][3]any{{"custom-1", "vless", 2443}, {"custom-2", "trojan", 3443}}, "direct"),
			want{ok: true, add: []string{"custom-2"}}},
		{"an inbound removed", inboundsCfg(t, u, nil, "direct"),
			want{ok: true, remove: []string{"custom-1"}}},
		{"an inbound's port changed", inboundsCfg(t, u, [][3]any{{"custom-1", "vless", 4443}}, "direct"),
			want{ok: true, remove: []string{"custom-1"}, add: []string{"custom-1"}}},
		{"an inbound added while users changed", inboundsCfg(t, []string{"u2", "u3"}, [][3]any{{"custom-1", "vless", 2443}, {"custom-2", "vless", 3443}}, "direct"),
			want{ok: true, add: []string{"custom-2"}, changes: []string{"vless-in: -u1 +u3", "custom-1: -u1 +u3"}}},
		// Users alone are the user sync's: nothing for this path to do.
		{"users alone", inboundsCfg(t, []string{"u1"}, one, "direct"), want{}},
		{"nothing", base, want{}},
		// Hysteria2 carries per-process state the supervisor keeps: still a restart.
		{"a Hysteria2 inbound added", inboundsCfg(t, u, [][3]any{{"custom-1", "vless", 2443}, {"custom-2", "hysteria", 3443}}, "direct"), want{}},
		// Anything outside the custom inbounds is structural.
		{"an inbound added with routing changed", inboundsCfg(t, u, [][3]any{{"custom-1", "vless", 2443}, {"custom-2", "vless", 3443}}, "egress"), want{}},
	}
	for _, c := range cases {
		ops, changes, ok := planInboundChanges(base, c.next)
		if ok != c.want.ok {
			t.Errorf("%s: ok=%v, want %v", c.name, ok, c.want.ok)
			continue
		}
		if !ok {
			continue
		}
		var add []string
		for _, in := range ops.add {
			add = append(add, in["tag"].(string))
		}
		if !slices.Equal(ops.remove, c.want.remove) || !slices.Equal(add, c.want.add) {
			t.Errorf("%s: remove %v add %v, want remove %v add %v", c.name, ops.remove, add, c.want.remove, c.want.add)
		}
		if got := planSummary(changes); !slices.Equal(got, c.want.changes) {
			t.Errorf("%s: user changes %v, want %v", c.name, got, c.want.changes)
		}
	}
}

// A node's config that adds and then removes a custom inbound is applied through the
// API — the same process keeps running, and the inbound goes up and down whole.
func TestApplyRawLiveChangesInboundsWithoutRestart(t *testing.T) {
	dir := t.TempDir()
	bin := fakeLiveXray(t, dir)
	cfgPath := filepath.Join(dir, "config.json")
	apiLog := filepath.Join(dir, "api.log")
	starts := filepath.Join(dir, "starts.log")
	sup := NewSupervisor(bin, cfgPath, dir)
	t.Cleanup(sup.Stop)
	const addr = "127.0.0.1:20085"
	u := []string{"u1", "u2"}

	if how, err := sup.ApplyRawLive(addr, inboundsCfg(t, u, nil, "direct")); err != nil || how != RawRestarted {
		t.Fatalf("first apply: %v %v", how, err)
	}
	waitFor(t, "xray to start", func() bool { return countLines(t, starts, "start") == 1 && sup.Running() })

	added := inboundsCfg(t, u, [][3]any{{"custom-1", "vless", 2443}}, "direct")
	if how, err := sup.ApplyRawLive(addr, added); err != nil || how != RawLive {
		t.Fatalf("an inbound added: %v %v, want live", how, err)
	}
	adi, err := os.ReadFile(filepath.Join(dir, "adi-0.json"))
	if err != nil {
		t.Fatalf("no inbound was added through the API: %v", err)
	}
	if !strings.Contains(string(adi), `"custom-1"`) || !strings.Contains(string(adi), `"u2"`) {
		t.Errorf("adi was handed %s, want custom-1 with its users", adi)
	}
	if onDisk, _ := os.ReadFile(cfgPath); string(onDisk) != string(added) {
		t.Error("the config on disk is not the one now running")
	}

	if how, err := sup.ApplyRawLive(addr, inboundsCfg(t, u, nil, "direct")); err != nil || how != RawLive {
		t.Fatalf("the inbound removed: %v %v, want live", how, err)
	}
	if countLines(t, apiLog, "api rmi --server="+addr+" custom-1") != 1 {
		t.Error("the removed inbound was not taken down through the API")
	}
	if n := countLines(t, starts, "start"); n != 1 {
		t.Errorf("xray was restarted (%d starts) for custom inbound changes", n)
	}

	// Refused by the API: the restart, which is always right, takes over.
	if err := os.WriteFile(filepath.Join(dir, "refuse-adi"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if how, err := sup.ApplyRawLive(addr, added); err != nil || how != RawRestarted {
		t.Fatalf("a refused adi: %v %v, want a restart", how, err)
	}
}

// The master's Apply takes the same path for a custom inbound added, and still
// restarts for a change it cannot make live.
func TestApplyAddsACustomInboundWithoutRestart(t *testing.T) {
	dir := t.TempDir()
	bin := fakeLiveXray(t, dir)
	starts := filepath.Join(dir, "starts.log")
	sup := NewSupervisor(bin, filepath.Join(dir, "config.json"), dir)
	t.Cleanup(sup.Stop)

	cfg := func(custom ...Inbound) *Config {
		c := &Config{
			Log: &Log{Loglevel: "warning"},
			Inbounds: append([]Inbound{
				{Tag: "api", Listen: "127.0.0.1", Port: APIPort, Protocol: "dokodemo-door", Settings: map[string]any{"address": "127.0.0.1"}},
				{Tag: "vless-in", Port: 443, Protocol: "vless", Settings: map[string]any{"clients": []any{map[string]any{"id": "id-u1", "email": "u1"}}, "decryption": "none"}},
			}, custom...),
			Outbounds: []Outbound{{Tag: "direct", Protocol: "freedom"}},
		}
		return c
	}
	custom := Inbound{Tag: "custom-4", Port: 2443, Protocol: "trojan", Settings: map[string]any{"clients": []any{map[string]any{"password": "pw-u1", "email": "u1"}}}}

	if err := sup.Apply(cfg()); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	waitFor(t, "xray to start", func() bool { return countLines(t, starts, "start") == 1 && sup.Running() })

	if err := sup.Apply(cfg(custom)); err != nil {
		t.Fatalf("custom inbound added: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "adi-0.json")); err != nil {
		t.Fatal("the custom inbound was not added through the API")
	}
	if n := countLines(t, starts, "start"); n != 1 {
		t.Fatalf("xray was restarted (%d starts) to add a custom inbound", n)
	}

	// A live change that failed part way leaves the process marked for a restart
	// (appliedCfg cleared): the next Apply restarts, even for an inbound alone.
	sup.mu.Lock()
	sup.appliedCfg = nil
	sup.mu.Unlock()
	if err := sup.Apply(cfg()); err != nil {
		t.Fatalf("custom inbound removed while marked for a restart: %v", err)
	}
	waitFor(t, "the restart a process marked for one gets", func() bool { return countLines(t, starts, "start") == 2 })

	withDebug := cfg(custom)
	withDebug.Log = &Log{Loglevel: "debug"}
	if err := sup.Apply(withDebug); err != nil {
		t.Fatalf("log level changed: %v", err)
	}
	waitFor(t, "a restart for a change outside the inbounds", func() bool { return countLines(t, starts, "start") == 3 })
}
