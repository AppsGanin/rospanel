package xray

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// liveCfg builds a node config the way the panel shapes one: an API inbound, the three
// built-in lanes, a custom Shadowsocks-2022 inbound and a SOCKS proxy with accounts.
// Each list names the emails on one user-carrying inbound.
type liveCfg struct {
	vless, reality, hysteria, ss []string
	uuidOf                       func(email string) string
	realityPort                  int
	socksPass                    string
}

func (c liveCfg) json(t *testing.T) []byte {
	t.Helper()
	uuid := c.uuidOf
	if uuid == nil {
		uuid = func(email string) string { return "id-" + email }
	}
	vless := func(emails []string, flow string) []map[string]any {
		out := []map[string]any{}
		for _, e := range emails {
			cl := map[string]any{"id": uuid(e), "email": e}
			if flow != "" {
				cl["flow"] = flow
			}
			out = append(out, cl)
		}
		return out
	}
	hy := []map[string]any{}
	for _, e := range c.hysteria {
		hy = append(hy, map[string]any{"auth": "pw-" + e, "email": e})
	}
	ss := []map[string]any{}
	for _, e := range c.ss {
		ss = append(ss, map[string]any{"password": "key-" + e, "email": e})
	}
	port := c.realityPort
	if port == 0 {
		port = 8443
	}
	pass := c.socksPass
	if pass == "" {
		pass = "secret"
	}
	cfg := map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"inbounds": []any{
			map[string]any{"tag": "api", "listen": "127.0.0.1", "port": 10085, "protocol": "dokodemo-door", "settings": map[string]any{"address": "127.0.0.1"}},
			map[string]any{"tag": "vless-in", "port": 443, "protocol": "vless", "settings": map[string]any{
				"clients": vless(c.vless, "xtls-rprx-vision"), "decryption": "none",
				"fallbacks": []any{map[string]any{"dest": "127.0.0.1:8080"}}}},
			map[string]any{"tag": "vless-reality-in", "port": port, "protocol": "vless", "settings": map[string]any{
				"clients": vless(c.reality, ""), "decryption": "none"}},
			map[string]any{"tag": "hysteria-in", "port": 443, "protocol": "hysteria", "settings": map[string]any{
				"version": 2, "users": hy}},
			map[string]any{"tag": "inb-9", "port": 9500, "protocol": "shadowsocks", "settings": map[string]any{
				"method": "2022-blake3-aes-128-gcm", "password": "server-key", "network": "tcp,udp", "users": ss}},
			map[string]any{"tag": "socks-in", "port": 1080, "protocol": "socks", "settings": map[string]any{
				"auth": "password", "accounts": []any{map[string]any{"user": "ops", "pass": pass}}}},
		},
		"outbounds": []any{map[string]any{"protocol": "freedom", "tag": "direct"}},
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// planSummary flattens a plan into "tag: -removed +added" lines, emails only.
func planSummary(changes []userChange) []string {
	var out []string
	for _, c := range changes {
		var add []string
		for _, a := range c.add {
			add = append(add, a.(map[string]any)["email"].(string))
		}
		line := fmt.Sprintf("%s: -%s +%s", c.tag, strings.Join(c.remove, ","), strings.Join(add, ","))
		if c.rebuild {
			line += " rebuild"
		}
		out = append(out, line)
	}
	return out
}

// Only a difference in who is on an inbound may skip the restart. Everything around
// the users — a port, a proxy account, anything outside the inbounds — is structural,
// and so is a user the API could not address or a change too big to apply by hand.
func TestPlanUserChanges(t *testing.T) {
	base := liveCfg{
		vless: []string{"u1", "u2"}, reality: []string{"u1", "u2"},
		hysteria: []string{"u1", "u2"}, ss: []string{"u1", "u2"},
	}
	cases := []struct {
		name string
		next liveCfg
		ok   bool
		want []string
	}{
		{"identical", base, true, nil},
		{"order alone", liveCfg{vless: []string{"u2", "u1"}, reality: base.reality, hysteria: base.hysteria, ss: base.ss}, true, nil},
		{"a user joins and one leaves", liveCfg{
			vless: []string{"u2", "u3"}, reality: []string{"u2", "u3"}, hysteria: []string{"u2", "u3"}, ss: []string{"u2", "u3"},
		}, true, []string{
			"vless-in: -u1 +u3", "vless-reality-in: -u1 +u3", "hysteria-in: -u1 +u3 rebuild", "inb-9: -u1 +u3",
		}},
		{"a rotated credential is removed and added", liveCfg{
			vless: base.vless, reality: base.reality, hysteria: base.hysteria, ss: base.ss,
			uuidOf: func(e string) string {
				if e == "u2" {
					return "rotated"
				}
				return "id-" + e
			},
		}, true, []string{"vless-in: -u2 +u2", "vless-reality-in: -u2 +u2"}},
		{"a lane emptied", liveCfg{vless: nil, reality: base.reality, hysteria: base.hysteria, ss: base.ss}, true, []string{"vless-in: -u1,u2 +"}},
		{"a port moved", liveCfg{vless: []string{"u1"}, reality: base.reality, hysteria: base.hysteria, ss: base.ss, realityPort: 9443}, false, nil},
		{"a proxy account changed", liveCfg{vless: base.vless, reality: base.reality, hysteria: base.hysteria, ss: base.ss, socksPass: "other"}, false, nil},
		{"shadowsocks left with nobody", liveCfg{vless: base.vless, reality: base.reality, hysteria: base.hysteria, ss: nil}, false, nil},
	}
	cur := base.json(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			changes, ok := planUserChanges(cur, c.next.json(t))
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v (plan %v)", ok, c.ok, planSummary(changes))
			}
			if got := planSummary(changes); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("plan\n got  %q\n want %q", got, c.want)
			}
		})
	}

	// A hysteria rebuild re-adds the whole pushed inbound, users included.
	next := liveCfg{vless: base.vless, reality: base.reality, hysteria: []string{"u1", "u2", "u3"}, ss: base.ss}.json(t)
	changes, ok := planUserChanges(cur, next)
	if !ok || len(changes) != 1 || !changes[0].rebuild {
		t.Fatalf("hysteria change: ok=%v %v", ok, planSummary(changes))
	}
	if users, _ := changes[0].inbound["settings"].(map[string]any)["users"].([]any); len(users) != 3 {
		t.Fatalf("the rebuilt inbound carries %d users, want all 3", len(users))
	}

	// Users the API could not address by email.
	for name, mutate := range map[string]func(string) string{
		"no email":       func(s string) string { return strings.Replace(s, `"email": "u2"`, `"email": ""`, 1) },
		"a shared email": func(s string) string { return strings.Replace(s, `"email": "u2"`, `"email": "u1"`, 1) },
		"users not listy": func(s string) string {
			return strings.Replace(s, `"decryption": "none",`, `"decryption": "none", "clients": 5,`, 1)
		},
	} {
		broken := mutate(string(liveCfg{vless: []string{"u1", "u2", "u3"}, reality: base.reality, hysteria: base.hysteria, ss: base.ss}.json(t)))
		if _, ok := planUserChanges(cur, []byte(broken)); ok {
			t.Errorf("%s: planned a live update", name)
		}
	}

	// Past the cap a restart is quicker than the API.
	many := make([]string, liveUserChangesMax+1)
	for i := range many {
		many[i] = fmt.Sprintf("u%d", 1000+i)
	}
	if _, ok := planUserChanges(cur, liveCfg{vless: many, reality: base.reality, hysteria: base.hysteria, ss: base.ss}.json(t)); ok {
		t.Error("a change past the cap was planned as a live update")
	}
	if _, ok := planUserChanges(cur, []byte("not json")); ok {
		t.Error("an unreadable config was planned as a live update")
	}
}

// fakeLiveXray is a stand-in Xray: `run -c` records a start and waits, `api …` records
// its arguments (and an adu file's emails) and answers the way the real CLI does. A
// file named fail-adu makes adu add nobody; one named fail-test makes validation refuse.
func fakeLiveXray(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "xray")
	log := filepath.Join(dir, "api.log")
	starts := filepath.Join(dir, "starts.log")
	fail := filepath.Join(dir, "fail-adu")
	refuse := filepath.Join(dir, "fail-test")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = run ] && [ \"$2\" = -test ]; then [ -f " + refuse + " ] && exit 23; exit 0; fi\n" +
		"if [ \"$1\" = run ]; then echo start >> " + starts + "; /bin/sleep 60 & wait; exit 0; fi\n" +
		"if [ \"$1\" = api ]; then\n" +
		"  echo \"$*\" >> " + log + "\n" +
		"  if [ \"$2\" = adu ]; then\n" +
		"    n=$(grep -o '\"email\":\"[^\"]*\"' \"$4\" | tee -a " + log + " | wc -l | tr -d ' ')\n" +
		"    if [ -f " + fail + " ]; then n=0; fi\n" +
		"    echo \"Added $n user(s) in total.\"\n" +
		"  fi\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func countLines(t *testing.T, path, prefix string) int {
	t.Helper()
	b, _ := os.ReadFile(path)
	n := 0
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, prefix) {
			n++
		}
	}
	return n
}

// End to end against a running (fake) Xray: a users-only change goes through the API
// with no restart, removals before additions; a structural change restarts; and a live
// update that the API refuses falls back to the restart that is always correct.
func TestApplyRawLiveChangesUsersWithoutRestart(t *testing.T) {
	dir := t.TempDir()
	bin := fakeLiveXray(t, dir)
	cfgPath := filepath.Join(dir, "config.json")
	apiLog := filepath.Join(dir, "api.log")
	starts := filepath.Join(dir, "starts.log")
	sup := NewSupervisor(bin, cfgPath, dir)
	t.Cleanup(sup.Stop)
	const addr = "127.0.0.1:20085"

	a := liveCfg{vless: []string{"u1", "u2"}, reality: []string{"u1", "u2"}, hysteria: []string{"u1", "u2"}, ss: []string{"u1", "u2"}}
	if how, err := sup.ApplyRawLive(addr, a.json(t)); err != nil || how != RawRestarted {
		t.Fatalf("first apply: %v %v", how, err)
	}
	waitFor(t, "xray to start", func() bool { return countLines(t, starts, "start") == 1 && sup.Running() })

	if how, err := sup.ApplyRawLive(addr, a.json(t)); err != nil || how != RawUnchanged {
		t.Fatalf("same config: %v %v, want unchanged", how, err)
	}

	b := liveCfg{vless: []string{"u2", "u3"}, reality: []string{"u2", "u3"}, hysteria: []string{"u2", "u3"}, ss: []string{"u2", "u3"}}
	how, err := sup.ApplyRawLive(addr, b.json(t))
	if err != nil || how != RawLive {
		t.Fatalf("users-only change: %v %v, want live", how, err)
	}
	if n := countLines(t, starts, "start"); n != 1 {
		t.Fatalf("a users-only change restarted xray (%d starts)", n)
	}
	if got, _ := os.ReadFile(cfgPath); string(got) != string(b.json(t)) {
		t.Fatal("the live-applied config was not recorded for the next start")
	}
	calls, _ := os.ReadFile(apiLog)
	log := string(calls)
	for _, want := range []string{
		"api rmu --server=" + addr + " -tag=vless-in u1",
		"api rmu --server=" + addr + " -tag=vless-reality-in u1",
		"api rmu --server=" + addr + " -tag=inb-9 u1",
		"api rmi --server=" + addr + " hysteria-in",
		"api adi --server=" + addr,
	} {
		if !strings.Contains(log, want) {
			t.Errorf("api calls lack %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, "-tag=hysteria-in") {
		t.Errorf("hysteria users went through rmu, which reports success and removes nothing:\n%s", log)
	}
	if strings.Index(log, "api adu") < strings.LastIndex(log, "api rmu") {
		t.Errorf("users were added before the revoked ones were removed:\n%s", log)
	}
	if added := countLines(t, apiLog, `"email":"u3"`); added != 3 {
		t.Errorf("u3 went into %d adu inbounds, want vless, reality and shadowsocks", added)
	}

	// Structural: restarts.
	c := b
	c.realityPort = 9443
	if how, err := sup.ApplyRawLive(addr, c.json(t)); err != nil || how != RawRestarted {
		t.Fatalf("structural change: %v %v, want a restart", how, err)
	}
	waitFor(t, "the restart", func() bool { return countLines(t, starts, "start") == 2 && sup.Running() })

	// The API refuses the additions: the restart takes over and the config still lands.
	if err := os.WriteFile(filepath.Join(dir, "fail-adu"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	d := c
	d.vless = []string{"u2", "u3", "u4"}
	if how, err := sup.ApplyRawLive(addr, d.json(t)); err != nil || how != RawRestarted {
		t.Fatalf("refused live update: %v %v, want the restart fallback", how, err)
	}
	waitFor(t, "the fallback restart", func() bool { return countLines(t, starts, "start") == 3 && sup.Running() })
	if got, _ := os.ReadFile(cfgPath); string(got) != string(d.json(t)) {
		t.Fatal("the fallback did not apply the pushed config")
	}

	// Refused all the way — the API, then the config itself: whatever the partial live
	// update changed is undone by restarting from the config still on disk.
	if err := os.WriteFile(filepath.Join(dir, "fail-test"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	e := d
	e.vless = []string{"u5"}
	if _, err := sup.ApplyRawLive(addr, e.json(t)); err == nil {
		t.Fatal("a config xray refused was reported applied")
	}
	waitFor(t, "the restore restart", func() bool { return countLines(t, starts, "start") == 4 && sup.Running() })
	if got, _ := os.ReadFile(cfgPath); string(got) != string(d.json(t)) {
		t.Fatal("a refused config replaced the one on disk")
	}
}
