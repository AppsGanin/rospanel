package xray

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// liveCfg builds a node config the way the panel shapes one: an API inbound, the three
// built-in lanes, a custom Shadowsocks-2022 inbound and a SOCKS proxy with accounts.
// Each list names the emails on one user-carrying inbound.
type liveCfg struct {
	vless, reality, hysteria, ss []string
	uuidOf                       func(email string) string
	realityPort                  int
	socksPass                    string
	routable                     bool // the api serves routing and every rule has a tag
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
	if c.routable {
		cfg["api"] = map[string]any{"tag": "api", "services": []string{"StatsService", "HandlerService", "RoutingService"}}
		cfg["outbounds"] = append(cfg["outbounds"].([]any), map[string]any{"protocol": "blackhole", "tag": "block"})
		cfg["routing"] = map[string]any{"rules": []any{
			map[string]any{"type": "field", "ruleTag": "rule-0", "inboundTag": []string{"api"}, "outboundTag": "api"},
			map[string]any{"type": "field", "ruleTag": "rule-1", "ip": []string{"10.0.0.0/8"}, "outboundTag": "block"},
		}}
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
		if c.hysteria {
			line += " hysteria"
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
			"vless-in: -u1 +u3", "vless-reality-in: -u1 +u3", "hysteria-in: -u1 +u3 hysteria", "inb-9: -u1 +u3",
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

	// A hysteria change carries the whole pushed inbound, users included, for a rebuild.
	next := liveCfg{vless: base.vless, reality: base.reality, hysteria: []string{"u1", "u2", "u3"}, ss: base.ss}.json(t)
	changes, ok := planUserChanges(cur, next)
	if !ok || len(changes) != 1 || !changes[0].hysteria {
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
// file named fail-adu makes adu add nobody; one named fail-test makes validation refuse;
// one named refuse-<call> makes that api call fail. The files adrules and adi are handed
// are kept as adrules-<n>.json and adi-<n>.json, numbered from 0.
func fakeLiveXray(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "xray")
	log := filepath.Join(dir, "api.log")
	starts := filepath.Join(dir, "starts.log")
	fail := filepath.Join(dir, "fail-adu")
	refuse := filepath.Join(dir, "fail-test")
	dial := filepath.Join(dir, "dial-fails")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = run ] && [ \"$2\" = -test ]; then [ -f " + refuse + " ] && exit 23; exit 0; fi\n" +
		"if [ \"$1\" = run ]; then echo start >> " + starts + "; /bin/sleep 60 & wait; exit 0; fi\n" +
		"if [ \"$1\" = api ]; then\n" +
		"  if [ -f " + dial + " ]; then n=$(cat " + dial + "); if [ \"$n\" -gt 0 ]; then echo $((n-1)) > " + dial + "; echo 'failed to dial' >&2; exit 1; fi; fi\n" +
		"  echo \"$*\" >> " + log + "\n" +
		"  if [ -f " + dir + "/refuse-$2 ]; then echo \"failed to perform $2\" >&2; exit 1; fi\n" +
		"  if [ \"$2\" = adrules ] || [ \"$2\" = adi ]; then eval f=\\${$#}; n=$(ls " + dir + " | grep -c \"^$2-\"); cp \"$f\" " + dir + "/$2-$n.json; fi\n" +
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

// A node's users-only change whose API calls cannot reach Xray for a moment is asked
// again and still applied live — not handed to a restart, which at 50,000 users starts a
// second Xray exactly when the box has no memory for one.
func TestApplyRawLiveAsksAgainWhenTheAPICannotBeReached(t *testing.T) {
	dir := t.TempDir()
	bin := fakeLiveXray(t, dir)
	cfgPath := filepath.Join(dir, "config.json")
	starts := filepath.Join(dir, "starts.log")
	sup := NewSupervisor(bin, cfgPath, dir)
	var waits []time.Duration
	sup.waitFn = func(d time.Duration) { waits = append(waits, d) }
	t.Cleanup(sup.Stop)
	const addr = "127.0.0.1:20085"

	a := liveCfg{vless: []string{"u1", "u2"}, reality: []string{"u1", "u2"}, hysteria: []string{"u1", "u2"}, ss: []string{"u1", "u2"}}
	if how, err := sup.ApplyRawLive(addr, a.json(t)); err != nil || how != RawRestarted {
		t.Fatalf("first apply: %v %v", how, err)
	}
	waitFor(t, "xray to start", func() bool { return countLines(t, starts, "start") == 1 && sup.Running() })

	if err := os.WriteFile(filepath.Join(dir, "dial-fails"), []byte("2"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := liveCfg{vless: []string{"u2", "u3"}, reality: []string{"u2", "u3"}, hysteria: []string{"u2", "u3"}, ss: []string{"u2", "u3"}}
	how, err := sup.ApplyRawLive(addr, b.json(t))
	if err != nil || how != RawLive {
		t.Fatalf("users-only change after two failed dials: %v %v, want live", how, err)
	}
	if n := countLines(t, starts, "start"); n != 1 {
		t.Errorf("xray was restarted (%d starts) for a call that only could not connect", n)
	}
	if len(waits) != 2 {
		t.Errorf("waited %v, want two waits before the call got through", waits)
	}
	if added := countLines(t, filepath.Join(dir, "api.log"), `"email":"u3"`); added != 3 {
		t.Errorf("u3 went into %d adu inbounds after the retries, want 3", added)
	}
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

// A node's pushed change of Hysteria2 users goes in user by user and cuts the removed
// users' open connections off by routing — no inbound rebuilt, so nobody else on the
// lane loses theirs — while the TCP lanes change through the CLI as before.
func TestApplyRawLiveChangesHysteriaUsersWithoutARebuild(t *testing.T) {
	dir := t.TempDir()
	sup := NewSupervisor(fakeLiveXray(t, dir), filepath.Join(dir, "config.json"), dir)
	t.Cleanup(sup.Stop)
	api := startFakeHandlerAPI(t)

	a := liveCfg{vless: []string{"u1", "u2"}, reality: []string{"u1", "u2"}, hysteria: []string{"u1", "u2"}, ss: []string{"u1", "u2"}, routable: true}
	if how, err := sup.ApplyRawLive(api.addr, a.json(t)); err != nil || how != RawRestarted {
		t.Fatalf("first apply: %v %v", how, err)
	}
	waitFor(t, "xray to start", sup.Running)
	cliCalls(t, dir)

	b := a
	b.hysteria = []string{"u2", "u3"}
	b.vless = []string{"u2", "u3"}
	if how, err := sup.ApplyRawLive(api.addr, b.json(t)); err != nil || how != RawLive {
		t.Fatalf("users-only change: %v %v, want live", how, err)
	}
	if got, want := api.calls(), []string{"remove u1 from hysteria-in", "add u3:pw-u3 to hysteria-in"}; !slices.Equal(got, want) {
		t.Errorf("api calls %q, want %q", got, want)
	}
	want := []string{"api rmu -tag=vless-in u1", "api adu", "api adrules -append", "api rmrules rule-1 rule-0"}
	if got := cliCalls(t, dir); !slices.Equal(got, want) {
		t.Errorf("cli calls %q, want %q", got, want)
	}
	if got := ruleTagsOf(rulesSent(t, dir, 0)); !slices.Equal(got, []string{"live1-cut-0", "live1-0", "live1-1"}) {
		t.Errorf("rules sent %q", got)
	}
	if n := countLines(t, filepath.Join(dir, "starts.log"), "start"); n != 1 {
		t.Errorf("xray was started %d times, want once", n)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "config.json")); string(got) != string(b.json(t)) {
		t.Error("the live-applied config was not recorded for the next start")
	}

	// The API refuses a user: the pushed config is applied by a restart instead, and the
	// restarted process's users are read from that config.
	api.setFail(func(c alterCall) (string, string) { return "2", "no" })
	c := b
	c.hysteria = []string{"u3"}
	if how, err := sup.ApplyRawLive(api.addr, c.json(t)); err != nil || how != RawRestarted {
		t.Fatalf("refused change: %v %v, want the restart fallback", how, err)
	}
	waitFor(t, "the fallback restart", func() bool { return countLines(t, filepath.Join(dir, "starts.log"), "start") == 2 && sup.Running() })
	api.setFail(nil)
	api.reset()
	d := c
	d.hysteria = []string{"u3", "u4"}
	if how, err := sup.ApplyRawLive(api.addr, d.json(t)); err != nil || how != RawLive {
		t.Fatalf("change after the restart: %v %v, want live", how, err)
	}
	if got, want := api.calls(), []string{"add u4:pw-u4 to hysteria-in"}; !slices.Equal(got, want) {
		t.Errorf("after the restart: api calls %q, want %q", got, want)
	}
}
