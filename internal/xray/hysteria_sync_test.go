package xray

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// alterCall is one AlterInbound call as the fake API decoded it.
type alterCall struct {
	tag, op, email, auth string
}

func (c alterCall) String() string {
	if c.op == "add" {
		return fmt.Sprintf("add %s:%s to %s", c.email, c.auth, c.tag)
	}
	return fmt.Sprintf("%s %s from %s", c.op, c.email, c.tag)
}

// fakeHandlerAPI is Xray's HandlerService as far as AlterInbound goes, over the same
// cleartext HTTP/2 gRPC the real one speaks. fail decides a call's answer: a gRPC status
// and message, or "" for success.
type fakeHandlerAPI struct {
	addr string
	mu   sync.Mutex
	got  []alterCall
	fail func(alterCall) (status, message string)
}

func startFakeHandlerAPI(t *testing.T) *fakeHandlerAPI {
	t.Helper()
	f := &fakeHandlerAPI{}
	var protocols http.Protocols
	protocols.SetUnencryptedHTTP2(true)
	srv := &http.Server{Handler: f, Protocols: &protocols}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	f.addr = ln.Addr().String()
	return f
}

func (f *fakeHandlerAPI) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.got {
		out = append(out, c.String())
	}
	return out
}

func (f *fakeHandlerAPI) setFail(fail func(alterCall) (status, message string)) {
	f.mu.Lock()
	f.fail = fail
	f.mu.Unlock()
}

func (f *fakeHandlerAPI) reset() {
	f.mu.Lock()
	f.got = nil
	f.mu.Unlock()
}

func (f *fakeHandlerAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	if r.ProtoMajor != 2 || r.URL.Path != handlerAlterInbound || r.Header.Get("Content-Type") != "application/grpc" ||
		len(body) < 5 || body[0] != 0 || int(binary.BigEndian.Uint32(body[1:5])) != len(body)-5 {
		http.Error(w, "not a grpc AlterInbound call", http.StatusBadRequest)
		return
	}
	call, err := decodeAlterInbound(body[5:])
	w.Header().Set("Content-Type", "application/grpc")
	if err != nil {
		w.Header().Set("Grpc-Status", "3")
		w.Header().Set("Grpc-Message", err.Error())
		w.WriteHeader(http.StatusOK)
		return
	}
	f.mu.Lock()
	f.got = append(f.got, call)
	fail := f.fail
	f.mu.Unlock()
	status, message := "0", ""
	if fail != nil {
		if s, m := fail(call); s != "" {
			status, message = s, m
		}
	}
	w.Header().Set("Trailer", "Grpc-Status, Grpc-Message")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte{0, 0, 0, 0, 0}) // AlterInboundResponse has no fields
	w.Header().Set("Grpc-Status", status)
	w.Header().Set("Grpc-Message", message)
}

// pbDecode splits a message into its fields, all expected length-delimited and single.
func pbDecode(b []byte) (map[uint64][]byte, error) {
	out := map[uint64][]byte{}
	for len(b) > 0 {
		key, n := binary.Uvarint(b)
		if n <= 0 || key&7 != 2 {
			return nil, errors.New("not a length-delimited field")
		}
		b = b[n:]
		size, n := binary.Uvarint(b)
		if n <= 0 || uint64(len(b)-n) < size {
			return nil, errors.New("truncated field")
		}
		if _, dup := out[key>>3]; dup {
			return nil, fmt.Errorf("field %d repeated", key>>3)
		}
		out[key>>3] = b[n : n+int(size)]
		b = b[n+int(size):]
	}
	return out, nil
}

func decodeTyped(b []byte, wantType string) ([]byte, error) {
	f, err := pbDecode(b)
	if err != nil {
		return nil, err
	}
	if string(f[1]) != wantType {
		return nil, fmt.Errorf("typed message %q, want %q", f[1], wantType)
	}
	return f[2], nil
}

func decodeAlterInbound(b []byte) (alterCall, error) {
	req, err := pbDecode(b)
	if err != nil {
		return alterCall{}, err
	}
	call := alterCall{tag: string(req[1])}
	opFields, err := pbDecode(req[2])
	if err != nil {
		return call, err
	}
	switch string(opFields[1]) {
	case "xray.app.proxyman.command.AddUserOperation":
		op, err := pbDecode(opFields[2])
		if err != nil {
			return call, err
		}
		user, err := pbDecode(op[1])
		if err != nil {
			return call, err
		}
		accountMsg, err := decodeTyped(user[3], "xray.proxy.hysteria.account.Account")
		if err != nil {
			return call, err
		}
		account, err := pbDecode(accountMsg)
		if err != nil {
			return call, err
		}
		call.op, call.email, call.auth = "add", string(user[2]), string(account[1])
	case "xray.app.proxyman.command.RemoveUserOperation":
		op, err := pbDecode(opFields[2])
		if err != nil {
			return call, err
		}
		call.op, call.email = "remove", string(op[1])
	default:
		return call, fmt.Errorf("operation %q", opFields[1])
	}
	return call, nil
}

// hyTestCfg is a running config for these tests: an API inbound, the built-in QUIC lane
// and a custom one, and routing with a balancer — everything the cut-off rules need,
// unless one of the flags takes a piece of it away.
type hyTestCfg struct {
	lane, custom  []string
	authOf        func(email string) string
	noRouting     bool // no RoutingService in the api services
	untagged      bool // one rule without a ruleTag
	noBlock       bool // no blackhole outbound
	liveTagged    bool // one rule carries a tag of the kind the live rules use
	duplicateTags bool
}

func (c hyTestCfg) users(emails []string) []HysteriaClient {
	auth := c.authOf
	if auth == nil {
		auth = func(e string) string { return "pw-" + e }
	}
	out := []HysteriaClient{}
	for _, e := range emails {
		out = append(out, HysteriaClient{Auth: auth(e), Email: e})
	}
	return out
}

func (c hyTestCfg) inbounds() []Inbound {
	return []Inbound{
		{Tag: "hysteria-in", Port: 443, Protocol: "hysteria", Settings: HysteriaInboundSettings{Version: 2, Users: c.users(c.lane)}},
		{Tag: "inb-7", Port: 7443, Protocol: "hysteria", Settings: HysteriaInboundSettings{Version: 2, Users: c.users(c.custom)}},
	}
}

func (c hyTestCfg) json(t *testing.T) []byte {
	t.Helper()
	services := []string{"StatsService", "HandlerService", "RoutingService"}
	if c.noRouting {
		services = services[:2]
	}
	rules := []map[string]any{
		{"type": "field", "ruleTag": "rule-0", "inboundTag": []string{"api"}, "outboundTag": "api"},
		{"type": "field", "ruleTag": "rule-1", "ip": []string{"10.0.0.0/8"}, "outboundTag": "block"},
		{"type": "field", "ruleTag": "rule-2", "network": "tcp,udp", "balancerTag": "warp-out"},
	}
	switch {
	case c.untagged:
		delete(rules[1], "ruleTag")
	case c.liveTagged:
		rules[1]["ruleTag"] = "live1-0"
	case c.duplicateTags:
		rules[2]["ruleTag"] = "rule-1"
	}
	outbounds := []map[string]any{{"tag": "direct", "protocol": "freedom"}}
	if !c.noBlock {
		outbounds = append(outbounds, map[string]any{"tag": "block", "protocol": "blackhole"})
	}
	cfg := map[string]any{
		"api": map[string]any{"tag": "api", "services": services},
		"inbounds": append([]any{
			map[string]any{"tag": "api", "listen": "127.0.0.1", "port": 10085, "protocol": "dokodemo-door"},
			map[string]any{"tag": "vless-in", "port": 443, "protocol": "vless", "settings": map[string]any{"clients": []any{}}},
		}, anySlice(c.inbounds())...),
		"outbounds": outbounds,
		"routing": map[string]any{
			"rules":     rules,
			"balancers": []any{map[string]any{"tag": "warp-out", "selector": []string{"warp"}}},
		},
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func anySlice(in []Inbound) []any {
	out := make([]any, len(in))
	for i := range in {
		out[i] = in[i]
	}
	return out
}

// hySupervisor starts a (fake) Xray on cfg and returns it with its directory and API.
func hySupervisor(t *testing.T, cfg []byte) (*Supervisor, string, *fakeHandlerAPI) {
	t.Helper()
	dir := t.TempDir()
	sup := NewSupervisor(fakeLiveXray(t, dir), filepath.Join(dir, "config.json"), dir)
	sup.waitFn = func(time.Duration) {}
	t.Cleanup(sup.Stop)
	if err := sup.ApplyRaw(cfg); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "xray to start", sup.Running)
	return sup, dir, startFakeHandlerAPI(t)
}

// cliCalls lists the api calls the fake CLI saw since the last call, without the
// --server flag, and clears the log.
func cliCalls(t *testing.T, dir string) []string {
	t.Helper()
	path := filepath.Join(dir, "api.log")
	b, _ := os.ReadFile(path)
	_ = os.Remove(path)
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" || strings.HasPrefix(line, `"email"`) {
			continue
		}
		var kept []string
		for _, f := range strings.Fields(line) {
			if !strings.HasPrefix(f, "--server=") && !strings.HasPrefix(f, "-timeout=") && !strings.Contains(f, string(filepath.Separator)) {
				kept = append(kept, f)
			}
		}
		out = append(out, strings.Join(kept, " "))
	}
	return out
}

// rulesSent reads the rules the n-th adrules call was handed.
func rulesSent(t *testing.T, dir string, n int) []map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("adrules-%d.json", n)))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Routing struct {
			Rules     []map[string]any `json:"rules"`
			Balancers []any            `json:"balancers"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Routing.Balancers != nil {
		t.Errorf("adrules %d carries balancers, which Xray refuses to add twice", n)
	}
	return doc.Routing.Rules
}

func ruleTagsOf(rules []map[string]any) []string {
	var out []string
	for _, r := range rules {
		out = append(out, fmt.Sprint(r["ruleTag"]))
	}
	return out
}

func runningProc(sup *Supervisor) *proc {
	sup.mu.Lock()
	defer sup.mu.Unlock()
	return sup.cur
}

func syncHy(t *testing.T, sup *Supervisor, api *fakeHandlerAPI, c hyTestCfg) error {
	t.Helper()
	return sup.SyncHysteria(api.addr, c.inbounds())
}

// Users join and leave one by one through the API, and a removed user's open connection
// is cut off by a rule put in front of all the others — the old rules replaced from the
// last to the first, the process never restarted and no inbound rebuilt.
func TestSyncHysteriaChangesUsersOneByOne(t *testing.T) {
	start := hyTestCfg{lane: []string{"u1", "u2"}, custom: []string{"u1"}}
	sup, dir, api := hySupervisor(t, start.json(t))

	if err := syncHy(t, sup, api, hyTestCfg{lane: []string{"u1", "u3"}, custom: []string{"u1"}}); err != nil {
		t.Fatal(err)
	}
	if got, want := api.calls(), []string{"remove u2 from hysteria-in", "add u3:pw-u3 to hysteria-in"}; !slices.Equal(got, want) {
		t.Errorf("api calls %q, want %q", got, want)
	}
	if got, want := cliCalls(t, dir), []string{"api adrules -append", "api rmrules rule-2 rule-1 rule-0"}; !slices.Equal(got, want) {
		t.Errorf("cli calls %q, want %q", got, want)
	}
	rules := rulesSent(t, dir, 0)
	if got, want := ruleTagsOf(rules), []string{"live1-cut-0", "live1-0", "live1-1", "live1-2"}; !slices.Equal(got, want) {
		t.Fatalf("rules sent %q, want %q", got, want)
	}
	cut := map[string]any{"type": "field", "ruleTag": "live1-cut-0", "inboundTag": []any{"hysteria-in"}, "user": []any{"u2"}, "outboundTag": "block"}
	if !reflect.DeepEqual(rules[0], cut) {
		t.Errorf("cut-off rule %v, want %v", rules[0], cut)
	}
	// The copies are the running rules with nothing but the tag changed.
	balancerCopy := map[string]any{"type": "field", "ruleTag": "live1-2", "network": "tcp,udp", "balancerTag": "warp-out"}
	if !reflect.DeepEqual(rules[3], balancerCopy) {
		t.Errorf("copy of the balancer rule %v, want %v", rules[3], balancerCopy)
	}

	if v := runningProc(sup).cutOff.Load(); v == nil || v.block != "block" || !reflect.DeepEqual(v.users, map[string]struct{}{"hysteria-in\x00u2": {}}) {
		t.Errorf("the access-log tap reads the cut-off as %+v", v)
	}

	// u2 comes back: added, and the rule that cut them off goes.
	api.reset()
	if err := syncHy(t, sup, api, hyTestCfg{lane: []string{"u1", "u2", "u3"}, custom: []string{"u1"}}); err != nil {
		t.Fatal(err)
	}
	if got, want := api.calls(), []string{"add u2:pw-u2 to hysteria-in"}; !slices.Equal(got, want) {
		t.Errorf("api calls %q, want %q", got, want)
	}
	if got, want := cliCalls(t, dir), []string{"api adrules -append", "api rmrules live1-2 live1-1 live1-0 live1-cut-0"}; !slices.Equal(got, want) {
		t.Errorf("cli calls %q, want %q", got, want)
	}
	if got, want := ruleTagsOf(rulesSent(t, dir, 1)), []string{"live2-0", "live2-1", "live2-2"}; !slices.Equal(got, want) {
		t.Errorf("rules sent %q, want %q", got, want)
	}
	if v := runningProc(sup).cutOff.Load(); v != nil {
		t.Errorf("nobody is cut off, and the access-log tap reads %+v", v)
	}

	// Nothing to change: nothing asked.
	api.reset()
	if err := syncHy(t, sup, api, hyTestCfg{lane: []string{"u3", "u2", "u1"}, custom: []string{"u1"}}); err != nil {
		t.Fatal(err)
	}
	if got, cli := api.calls(), cliCalls(t, dir); len(got) != 0 || len(cli) != 0 {
		t.Errorf("an unchanged set of users made calls: api %q, cli %q", got, cli)
	}

	// Two inbounds lose users at once: one cut-off rule each, in tag order, users sorted.
	if err := syncHy(t, sup, api, hyTestCfg{lane: []string{"u2"}, custom: nil}); err != nil {
		t.Fatal(err)
	}
	if got, want := api.calls(), []string{"remove u1 from hysteria-in", "remove u3 from hysteria-in", "remove u1 from inb-7"}; !slices.Equal(got, want) {
		t.Errorf("api calls %q, want %q", got, want)
	}
	rules = rulesSent(t, dir, 2)
	if got, want := ruleTagsOf(rules)[:2], []string{"live3-cut-0", "live3-cut-1"}; !slices.Equal(got, want) {
		t.Fatalf("rules sent %q, want cut-off rules first", ruleTagsOf(rules))
	}
	if !reflect.DeepEqual(rules[0]["user"], []any{"u1", "u3"}) || !reflect.DeepEqual(rules[0]["inboundTag"], []any{"hysteria-in"}) ||
		!reflect.DeepEqual(rules[1]["user"], []any{"u1"}) || !reflect.DeepEqual(rules[1]["inboundTag"], []any{"inb-7"}) {
		t.Errorf("cut-off rules %v %v", rules[0], rules[1])
	}
	if got := cliCalls(t, dir); !slices.Equal(got, []string{"api adrules -append", "api rmrules live2-2 live2-1 live2-0"}) {
		t.Errorf("cli calls %q", got)
	}
	if n := countLines(t, filepath.Join(dir, "starts.log"), "start"); n != 1 {
		t.Errorf("xray was started %d times, want once", n)
	}
}

// When a removed user's connection cannot be cut off by a rule, the inbound is rebuilt —
// every connection on it ends, as before — rather than leaving that user their access.
// Users who only join still go in one by one.
func TestSyncHysteriaRebuildsWhatItCannotCutOff(t *testing.T) {
	for name, broken := range map[string]hyTestCfg{
		"no routing service":      {noRouting: true},
		"an untagged rule":        {untagged: true},
		"no block outbound":       {noBlock: true},
		"a rule tagged like ours": {liveTagged: true},
		"two rules with one tag":  {duplicateTags: true},
	} {
		t.Run(name, func(t *testing.T) {
			start := broken
			start.lane, start.custom = []string{"u1", "u2"}, []string{"u1"}
			sup, dir, api := hySupervisor(t, start.json(t))

			joins := broken
			joins.lane, joins.custom = []string{"u1", "u2", "u3"}, []string{"u1"}
			if err := syncHy(t, sup, api, joins); err != nil {
				t.Fatal(err)
			}
			if got := api.calls(); !slices.Equal(got, []string{"add u3:pw-u3 to hysteria-in"}) {
				t.Errorf("a join: api calls %q", got)
			}
			if got := cliCalls(t, dir); len(got) != 0 {
				t.Errorf("a join: cli calls %q", got)
			}

			api.reset()
			leaves := broken
			leaves.lane, leaves.custom = []string{"u3"}, []string{"u1"}
			if err := syncHy(t, sup, api, leaves); err != nil {
				t.Fatal(err)
			}
			if got := api.calls(); len(got) != 0 {
				t.Errorf("a removal: api calls %q, want the rebuild alone", got)
			}
			if got, want := cliCalls(t, dir), []string{"api rmi hysteria-in", "api adi"}; !slices.Equal(got, want) {
				t.Errorf("a removal: cli calls %q, want %q", got, want)
			}
			assertRebuiltWith(t, dir, 0, "hysteria-in", []string{"u3"})
		})
	}
}

// assertRebuiltWith checks the n-th inbound handed to adi and its users.
func assertRebuiltWith(t *testing.T, dir string, n int, tag string, emails []string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("adi-%d.json", n)))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Inbounds []struct {
			Tag      string `json:"tag"`
			Port     int    `json:"port"`
			Settings struct {
				Users []HysteriaClient `json:"users"`
			} `json:"settings"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Inbounds) != 1 || doc.Inbounds[0].Tag != tag || doc.Inbounds[0].Port == 0 {
		t.Fatalf("rebuilt %s", b)
	}
	var got []string
	for _, u := range doc.Inbounds[0].Settings.Users {
		got = append(got, u.Email)
	}
	if !slices.Equal(got, emails) {
		t.Errorf("rebuilt %s with %q, want %q", tag, got, emails)
	}
}

// A user whose auth changed keeps the email of their old connection, so no rule can tell
// the two apart: the inbound is rebuilt, and the users it had cut off need no rule after.
func TestSyncHysteriaRebuildsForAChangedAuth(t *testing.T) {
	start := hyTestCfg{lane: []string{"u1", "u2", "u3"}, custom: []string{"u1"}}
	sup, dir, api := hySupervisor(t, start.json(t))
	if err := syncHy(t, sup, api, hyTestCfg{lane: []string{"u1", "u2"}, custom: []string{"u1"}}); err != nil {
		t.Fatal(err)
	}
	cliCalls(t, dir)
	api.reset()

	rekeyed := hyTestCfg{lane: []string{"u1", "u2"}, custom: []string{"u1"}, authOf: func(e string) string {
		if e == "u2" {
			return "new-key"
		}
		return "pw-" + e
	}}
	if err := syncHy(t, sup, api, rekeyed); err != nil {
		t.Fatal(err)
	}
	if got := api.calls(); len(got) != 0 {
		t.Errorf("api calls %q, want the rebuild alone", got)
	}
	want := []string{"api rmi hysteria-in", "api adi", "api adrules -append", "api rmrules live1-2 live1-1 live1-0 live1-cut-0"}
	if got := cliCalls(t, dir); !slices.Equal(got, want) {
		t.Errorf("cli calls %q, want %q", got, want)
	}
	if got := ruleTagsOf(rulesSent(t, dir, 1)); !slices.Equal(got, []string{"live2-0", "live2-1", "live2-2"}) {
		t.Errorf("rules after the rebuild %q, want no cut-off rule", got)
	}
}

// Past hysteriaCutMax the cut-off rule would be checked name by name against too many
// users on every stream: the inbound is rebuilt instead, and its connections all end.
func TestSyncHysteriaRebuildsPastTheCutOffLimit(t *testing.T) {
	var many []string
	for i := range hysteriaCutMax + 1 {
		many = append(many, fmt.Sprintf("u%d", i))
	}
	start := hyTestCfg{lane: append([]string{"keep"}, many...), custom: []string{"keep"}}
	sup, dir, api := hySupervisor(t, start.json(t))

	// Exactly the limit: cut off by a rule.
	if err := syncHy(t, sup, api, hyTestCfg{lane: []string{"keep", many[hysteriaCutMax]}, custom: []string{"keep"}}); err != nil {
		t.Fatal(err)
	}
	if got := len(api.calls()); got != hysteriaCutMax {
		t.Errorf("%d api calls, want a removal for each of %d users", got, hysteriaCutMax)
	}
	if got := cliCalls(t, dir); !slices.Equal(got, []string{"api adrules -append", "api rmrules rule-2 rule-1 rule-0"}) {
		t.Errorf("cli calls %q", got)
	}

	// One past it: rebuilt, and the rule is no longer needed.
	api.reset()
	if err := syncHy(t, sup, api, hyTestCfg{lane: []string{"keep"}, custom: []string{"keep"}}); err != nil {
		t.Fatal(err)
	}
	if got := api.calls(); len(got) != 0 {
		t.Errorf("api calls %q, want the rebuild alone", got)
	}
	want := []string{"api rmi hysteria-in", "api adi", "api adrules -append", "api rmrules live1-2 live1-1 live1-0 live1-cut-0"}
	if got := cliCalls(t, dir); !slices.Equal(got, want) {
		t.Errorf("cli calls %q, want %q", got, want)
	}
	assertRebuiltWith(t, dir, 0, "hysteria-in", []string{"keep"})
}

// A failure part way leaves the process holding users no config describes: it is not
// touched again, and the next Apply restarts it instead of finding it current. The
// restarted process starts over from its own config.
func TestSyncHysteriaFailureLeavesTheProcessForARestart(t *testing.T) {
	for name, breakIt := range map[string]func(dir string, api *fakeHandlerAPI){
		"the api refuses a user": func(_ string, api *fakeHandlerAPI) {
			api.setFail(func(c alterCall) (string, string) {
				if c.email == "u3" {
					return "2", "failed to add user"
				}
				return "", ""
			})
		},
		"the rules are refused": func(dir string, _ *fakeHandlerAPI) {
			if err := os.WriteFile(filepath.Join(dir, "refuse-adrules"), nil, 0o600); err != nil {
				panic(err)
			}
		},
		"the old rules stay": func(dir string, _ *fakeHandlerAPI) {
			if err := os.WriteFile(filepath.Join(dir, "refuse-rmrules"), nil, 0o600); err != nil {
				panic(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			start := hyTestCfg{lane: []string{"u1", "u2"}, custom: []string{"u1"}}
			data := start.json(t)
			sup, dir, api := hySupervisor(t, data)
			breakIt(dir, api)
			err := syncHy(t, sup, api, hyTestCfg{lane: []string{"u1", "u3"}, custom: []string{"u1"}})
			if err == nil {
				t.Fatal("a failed change was reported applied")
			}
			sup.mu.Lock()
			applied := sup.appliedCfg
			sup.mu.Unlock()
			if applied != nil {
				t.Error("the process still reads as running its config")
			}

			api.reset()
			cliCalls(t, dir)
			if err := syncHy(t, sup, api, hyTestCfg{lane: []string{"u1"}, custom: []string{"u1"}}); err == nil {
				t.Error("a process left part way was changed again")
			}
			if got, cli := api.calls(), cliCalls(t, dir); len(got) != 0 || len(cli) != 0 {
				t.Errorf("calls to a process left part way: api %q, cli %q", got, cli)
			}

			// Restarted on the config it had: back to work, from that config's users.
			_ = os.Remove(filepath.Join(dir, "refuse-adrules"))
			_ = os.Remove(filepath.Join(dir, "refuse-rmrules"))
			api.setFail(nil)
			if err := sup.ApplyRaw(data); err != nil {
				t.Fatal(err)
			}
			waitFor(t, "the restart", func() bool { return countLines(t, filepath.Join(dir, "starts.log"), "start") == 2 && sup.Running() })
			cliCalls(t, dir)
			if err := syncHy(t, sup, api, hyTestCfg{lane: []string{"u1", "u3"}, custom: []string{"u1"}}); err != nil {
				t.Fatal(err)
			}
			if got, want := api.calls(), []string{"remove u2 from hysteria-in", "add u3:pw-u3 to hysteria-in"}; !slices.Equal(got, want) {
				t.Errorf("after the restart: api calls %q, want %q", got, want)
			}
			if got, want := cliCalls(t, dir), []string{"api adrules -append", "api rmrules rule-2 rule-1 rule-0"}; !slices.Equal(got, want) {
				t.Errorf("after the restart: cli calls %q, want %q", got, want)
			}
		})
	}
}

// An inbound the running process does not have — one the config gained since, whose
// restart is still to come — is left for that restart.
func TestSyncHysteriaLeavesAnInboundTheProcessLacks(t *testing.T) {
	sup, dir, api := hySupervisor(t, hyTestCfg{lane: []string{"u1"}, custom: []string{"u1"}}.json(t))
	extra := Inbound{Tag: "inb-9", Port: 9443, Protocol: "hysteria", Settings: HysteriaInboundSettings{Version: 2, Users: []HysteriaClient{{Auth: "pw-u1", Email: "u1"}}}}
	if err := sup.SyncHysteria(api.addr, append(hyTestCfg{lane: []string{"u1"}, custom: []string{"u1"}}.inbounds(), extra)); err != nil {
		t.Fatal(err)
	}
	if got, cli := api.calls(), cliCalls(t, dir); len(got) != 0 || len(cli) != 0 {
		t.Errorf("calls for an inbound the process lacks: api %q, cli %q", got, cli)
	}
}

// Users the API cannot tell apart are refused before anything is changed.
func TestSyncHysteriaRefusesUsersItCannotAddress(t *testing.T) {
	for name, users := range map[string][]HysteriaClient{
		"no email":                        {{Auth: "pw", Email: ""}},
		"shared email":                    {{Auth: "a", Email: "u9"}, {Auth: "b", Email: "u9"}},
		"shared email, the first keyless": {{Auth: "", Email: "u9"}, {Auth: "b", Email: "u9"}},
	} {
		t.Run(name, func(t *testing.T) {
			sup, dir, api := hySupervisor(t, hyTestCfg{lane: []string{"u1"}, custom: []string{"u1"}}.json(t))
			in := Inbound{Tag: "hysteria-in", Protocol: "hysteria", Settings: HysteriaInboundSettings{Version: 2, Users: users}}
			if err := sup.SyncHysteria(api.addr, []Inbound{in}); err == nil {
				t.Fatal("accepted")
			}
			if got, cli := api.calls(), cliCalls(t, dir); len(got) != 0 || len(cli) != 0 {
				t.Errorf("calls: api %q, cli %q", got, cli)
			}
		})
	}
}

// An empty auth is a key every client holds — what a password that failed to decrypt
// reads as. Such a user is kept off the inbound, and cut off if they were on it, rather
// than refused: a refusal would restart Xray over every change for as long as it lasts.
func TestSyncHysteriaKeepsAKeylessUserOut(t *testing.T) {
	sup, dir, api := hySupervisor(t, hyTestCfg{lane: []string{"u1", "u2"}, custom: []string{"u1"}}.json(t))
	in := Inbound{Tag: "hysteria-in", Protocol: "hysteria", Settings: HysteriaInboundSettings{Version: 2, Users: []HysteriaClient{
		{Auth: "pw-u1", Email: "u1"}, {Auth: "", Email: "u2"}, {Auth: "", Email: "u3"},
	}}}
	if err := sup.SyncHysteria(api.addr, []Inbound{in}); err != nil {
		t.Fatal(err)
	}
	if got, want := api.calls(), []string{"remove u2 from hysteria-in"}; !slices.Equal(got, want) {
		t.Errorf("api calls %q, want %q", got, want)
	}
	if got, want := cliCalls(t, dir), []string{"api adrules -append", "api rmrules rule-2 rule-1 rule-0"}; !slices.Equal(got, want) {
		t.Errorf("cli calls %q, want %q", got, want)
	}

	// A rebuild puts the keyless in with everyone else — it re-adds the inbound as
	// given — and the change after it takes them off again.
	api.reset()
	rekeyed := Inbound{Tag: "hysteria-in", Protocol: "hysteria", Settings: HysteriaInboundSettings{Version: 2, Users: []HysteriaClient{
		{Auth: "new-key", Email: "u1"}, {Auth: "", Email: "u3"},
	}}}
	if err := sup.SyncHysteria(api.addr, []Inbound{rekeyed}); err != nil {
		t.Fatal(err)
	}
	if got, want := cliCalls(t, dir), []string{"api rmi hysteria-in", "api adi", "api adrules -append", "api rmrules live1-2 live1-1 live1-0 live1-cut-0"}; !slices.Equal(got, want) {
		t.Errorf("cli calls %q, want %q", got, want)
	}
	if err := sup.SyncHysteria(api.addr, []Inbound{rekeyed}); err != nil {
		t.Fatal(err)
	}
	if got, want := api.calls(), []string{"remove u3 from hysteria-in"}; !slices.Equal(got, want) {
		t.Errorf("after the rebuild: api calls %q, want %q", got, want)
	}
}

// A stopped Xray is not changed live: the caller's full reload starts it.
func TestSyncHysteriaNeedsARunningXray(t *testing.T) {
	dir := t.TempDir()
	sup := NewSupervisor(fakeLiveXray(t, dir), filepath.Join(dir, "config.json"), dir)
	api := startFakeHandlerAPI(t)
	if err := sup.SyncHysteria(api.addr, hyTestCfg{lane: []string{"u1"}}.inbounds()); err == nil {
		t.Fatal("a change to a stopped xray was reported applied")
	}
}

// Xray logs every stream a cut-off user's open connection opens — into the block outbound
// — as accepted. Those are not the user being online, and only those are skipped: the
// same user elsewhere, and anyone else sent to block by an ordinary rule, still count.
func TestAccessTapSkipsStreamsTheRoutingCutsOff(t *testing.T) {
	dir := t.TempDir()
	sup := NewSupervisor("", filepath.Join(dir, "config.json"), dir)
	var seen []string
	sup.SetOnAccess(func(email, ip, dest string) { seen = append(seen, email+"@"+ip+" "+dest) })
	p := &proc{}
	p.cutOff.Store(&cutOffView{block: "block", users: map[string]struct{}{"hysteria-in\x00u2": {}}})
	lines := strings.Join([]string{
		"2026/09/17 10:24:08.199289 from 5.6.7.8:55772 accepted tcp:cut.example:443 [hysteria-in -> block] email: u2",
		"2026/09/17 10:24:08.199289 from 5.6.7.8:55772 accepted tcp:direct.example:443 [hysteria-in >> direct] email: u2",
		"2026/09/17 10:24:08.199289 from 5.6.7.8:55772 accepted tcp:ruled.example:443 [hysteria-in -> direct] email: u2",
		"2026/09/17 10:24:08.199289 from 5.6.7.8:55773 accepted tcp:other-inbound.example:443 [inb-7 -> block] email: u2",
		"2026/09/17 10:24:08.199289 from 5.6.7.8:55774 accepted tcp:vless.example:443 [vless-in -> block] email: u2",
		"2026/09/17 10:24:08.199289 from 9.9.9.9:4000 accepted tcp:ads.example:443 [hysteria-in -> block] email: u1",
		"2026/09/17 10:24:08.199289 from 9.9.9.9:4001 accepted tcp:ads.example:443 [hysteria-in -> block] email: u22",
	}, "\n")
	sup.tap(p, strings.NewReader(lines), io.Discard, true)
	want := []string{
		"u2@5.6.7.8 direct.example", "u2@5.6.7.8 ruled.example", "u2@5.6.7.8 other-inbound.example", "u2@5.6.7.8 vless.example",
		"u1@9.9.9.9 ads.example", "u22@9.9.9.9 ads.example",
	}
	if !slices.Equal(seen, want) {
		t.Errorf("sightings\n got  %q\n want %q", seen, want)
	}
	if tail := sup.LogTail(); len(tail) != 7 {
		t.Errorf("the log viewer got %d lines, want all 7: the cut-off streams are worth seeing there", len(tail))
	}

	// Nobody cut off: every line counts.
	seen = nil
	sup.tap(&proc{}, strings.NewReader(lines), io.Discard, true)
	if len(seen) != 7 {
		t.Errorf("with nobody cut off, %d of 7 lines counted", len(seen))
	}
}
