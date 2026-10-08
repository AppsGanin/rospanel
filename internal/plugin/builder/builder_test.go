package builder

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/plugin/devkit"
	"github.com/AppsGanin/rospanel/internal/plugin/jsvm"
	"github.com/AppsGanin/rospanel/internal/plugin/manifest"
)

func welcome() Spec {
	return Spec{ID: "welcome", Version: "1.0.0", Name: "Welcome", Rules: []Rule{{
		Event:      "user.registered",
		Conditions: []Condition{{Field: "data.lang", Op: "eq", Value: "ru"}},
		Actions: []Action{
			{Type: "telegram", Chat: "-100500", Text: "New: {{user.name}}"},
			{Type: "extend", Days: 3},
			{Type: "tag", Tag: "welcomed"},
		},
	}, {
		Schedule: "0 9 * * *",
		Actions:  []Action{{Type: "http", URL: "https://hooks.example.com/daily?at={{now}}", Body: `{"text": "{{event}}"}`, Auth: true}},
	}}}
}

func TestValidate(t *testing.T) {
	if p := Validate(welcome()); len(p) > 0 {
		t.Fatalf("a good spec: %v", p)
	}
	for _, c := range []struct {
		edit func(*Spec)
		want string
	}{
		{func(s *Spec) { s.ID = "X" }, "id"},
		{func(s *Spec) { s.Rules = nil }, "rules: 1-"},
		{func(s *Spec) { s.Rules[0].Event = "user.flew" }, "unknown event"},
		{func(s *Spec) { s.Rules[0].Schedule = "* * * * *" }, "not both"},
		{func(s *Spec) { s.Rules[1].Schedule = "every day" }, "schedule"},
		{func(s *Spec) { s.Rules[1].Actions = []Action{{Type: "extend", Days: 1}} }, "no user to extend"},
		{func(s *Spec) { s.Rules[0].Actions[0].Text = "" }, "text: required"},
		{func(s *Spec) { s.Rules[0].Actions[1].Days = 0 }, "days"},
		{func(s *Spec) { s.Rules[0].Conditions[0].Op = "like" }, "op"},
		{func(s *Spec) { s.Rules[0].Conditions[0].Field = "data;x" }, "field"},
		{func(s *Spec) { s.Rules[1].Actions[0].URL = "https://{{data.host}}/x" }, "placeholder"},
		{func(s *Spec) { s.Rules[1].Actions[0].URL = "https://10.0.0.1/x" }, "not an IP"},
		{func(s *Spec) { s.Rules[1].Actions[0].URL = "ftp://x.io/" }, "http(s)"},
		{func(s *Spec) { s.Rules[0].Actions[2].Tag = "a,b" }, "tag"},
	} {
		s := welcome()
		c.edit(&s)
		if p := strings.Join(Validate(s), "; "); !strings.Contains(p, c.want) {
			t.Errorf("want %q in %q", c.want, p)
		}
	}
}

func TestCompileManifest(t *testing.T) {
	files, err := Compile(welcome(), "4.4.0")
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := devkit.PackFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := manifest.Read(raw, "4.4.0")
	if err != nil {
		t.Fatalf("the plugin does not validate: %v", err)
	}
	m := pkg.Manifest
	if !slices.Equal(m.Permissions, []string{"users.manage", "users.view"}) {
		t.Errorf("permissions: %v", m.Permissions)
	}
	if !slices.Equal(m.Net, []string{"hooks.example.com", "api.telegram.org"}) {
		t.Errorf("net: %v", m.Net)
	}
	if len(m.Provides.Cron) != 1 || m.Provides.Cron[0].Name != "rule_2" || !slices.Equal(m.Provides.Events, []string{"user.registered"}) {
		t.Errorf("provides: %+v", m.Provides)
	}
	var keys []string
	for _, f := range m.Settings {
		keys = append(keys, f.Key)
	}
	if !slices.Equal(keys, []string{"telegram_token", "http_authorization"}) {
		t.Errorf("settings: %v", keys)
	}
	var back Spec
	if err := json.Unmarshal(files[SpecFile], &back); err != nil || back.Rules[0].Actions[1].Days != 3 {
		t.Errorf("rules.json: %v %+v", err, back)
	}
	if strings.Contains(string(raw), "rules.json") {
		t.Error("rules.json went into the package")
	}
}

// A money event brings billing.view with it, as the manifest rule wants.
func TestCompileMoneyEvent(t *testing.T) {
	s := Spec{ID: "paid", Version: "1.0.0", Name: "Paid", Rules: []Rule{{Event: "payment.paid", Actions: []Action{{Type: "log", Text: "{{data.amount_rub}}"}}}}}
	files, err := Compile(s, "")
	if err != nil {
		t.Fatal(err)
	}
	raw, _, _ := devkit.PackFiles(files)
	pkg, err := manifest.Read(raw, "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(pkg.Manifest.Permissions, []string{"billing.view", "users.view"}) {
		t.Fatalf("permissions: %v", pkg.Manifest.Permissions)
	}
}

// The generated smoke test passes in the real sandbox.
func TestSmokeTestPasses(t *testing.T) {
	files, err := Compile(welcome(), "4.4.0")
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	res, err := devkit.RunTestFiles(context.Background(), files, devkit.TestOptions{PanelVersion: "4.4.0", Out: &out})
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	for _, r := range res {
		if !r.OK {
			t.Errorf("%s: %s", r.Name, r.Error)
		}
	}
	if len(res) != 2 {
		t.Fatalf("tests: %+v", res)
	}
}

// The rules do what they say: a matching event fills the text and runs every
// action, a retried delivery repeats nothing, a non-matching one does nothing.
func TestRulesRun(t *testing.T) {
	files, err := Compile(welcome(), "4.4.0")
	if err != nil {
		t.Fatal(err)
	}
	files["test.js"] = []byte(`
const user = { id: 7, name: "Ann", lang: "ru" };
function mocks() {
  mock.http("https://api.telegram.org/", { status: 200, body: "{}" });
  mock.api("POST", "/v1/users/bulk", { status: 200, body: {} });
  mock.api("GET", "/v1/users/7", { status: 200, body: { data: { id: 7, tags: ["vip"] } } });
  mock.api("PATCH", "/v1/users/7", { status: 200, body: {} });
}
test("a matching event runs every action, once", () => {
  mocks();
  const e = { id: "e1", event: "user.registered", created_at: 0, data: user };
  plugin.call("onEvent", e);
  plugin.call("onEvent", e); // the outbox retrying
  const calls = mock.calls();
  assert.equal(calls.length, 4);
  assert.equal(JSON.parse(calls[0].body), { chat_id: "-100500", text: "New: Ann", disable_web_page_preview: true });
  assert.equal(calls[0].url, "https://api.telegram.org/bot123:test/sendMessage");
  assert.equal(JSON.parse(calls[1].body), { ids: [7], action: "extend", days: 3 });
  assert.equal(JSON.parse(calls[3].body), { tags: ["vip", "welcomed"] });
});
test("a failed action is retried, the done ones are not", () => {
  mock.http("https://api.telegram.org/", { status: 500, body: "down" });
  const e = { id: "e2", event: "user.registered", created_at: 0, data: user };
  assert.throws(() => plugin.call("onEvent", e));
  mocks();
  plugin.call("onEvent", e);
  assert.equal(mock.calls().filter((c) => c.url.includes("telegram")).length, 2);
  assert.equal(mock.calls().filter((c) => c.url === "/v1/users/bulk").length, 1);
});
test("a condition that does not hold runs nothing", () => {
  mocks();
  plugin.event("user.registered", { id: 8, name: "Bob", lang: "en" });
  assert.equal(mock.calls().length, 0);
});
test("the scheduled rule escapes values into its JSON body", () => {
  mock.http("https://hooks.example.com/", { status: 200, body: "{}" });
  plugin.call("rule_2");
  const c = mock.calls()[0];
  assert.equal(JSON.parse(c.body), { text: "cron" });
  assert.ok(c.url.startsWith("https://hooks.example.com/daily?at=20"), c.url);
});
`)
	var out strings.Builder
	res, err := devkit.RunTestFiles(context.Background(), files, devkit.TestOptions{PanelVersion: "4.4.0", Out: &out})
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	for _, r := range res {
		if !r.OK {
			t.Errorf("%s: %s\n%s", r.Name, r.Error, r.Stack)
		}
	}
}

// main.js holds what the rules use: an action taken out takes its code with it.
func TestMainHoldsOnlyWhatRulesUse(t *testing.T) {
	spec := Spec{ID: "only", Version: "1.0.0", Name: "Only", Rules: []Rule{{
		Event: "user.created", Actions: []Action{{Type: "enable"}},
	}}}
	files, err := Compile(spec, "")
	if err != nil {
		t.Fatal(err)
	}
	main := string(files["main.js"])
	for _, gone := range []string{"function telegram", "function discord", "function request", "function fill", "function holds", `case "telegram"`, "function retag"} {
		if strings.Contains(main, gone) {
			t.Errorf("main.js has %q for rules that do not use it", gone)
		}
	}
	for _, kept := range []string{`case "enable"`, "function userId", "function answered", "export function onEvent", "const matches = () => true"} {
		if !strings.Contains(main, kept) {
			t.Errorf("main.js lacks %q", kept)
		}
	}
	spec.Rules[0].Actions = []Action{{Type: "telegram", Chat: "1", Text: "hi"}}
	spec.Rules[0].Conditions = []Condition{{Field: "user.lang", Op: "eq", Value: "ru"}}
	files, _ = Compile(spec, "")
	main = string(files["main.js"])
	if strings.Contains(main, `case "enable"`) || strings.Contains(main, "function userId") || !strings.Contains(main, "function telegram") || !strings.Contains(main, "function holds") {
		t.Fatalf("after switching to telegram:\n%s", main)
	}
}

// Every kind of action, on an event or a schedule, with conditions or without,
// makes a plugin whose code runs: no part calls one that was left out.
func TestEveryCombinationRuns(t *testing.T) {
	samples := map[string]Action{
		"telegram": {Type: "telegram", Chat: "{{user.telegram_id}}", Text: "hi {{user.name}}"},
		"discord":  {Type: "discord", Text: "hi"},
		"http":     {Type: "http", URL: "https://hooks.example.com/x", Auth: true},
		"extend":   {Type: "extend", Days: 3},
		"enable":   {Type: "enable"},
		"disable":  {Type: "disable"},
		"tag":      {Type: "tag", Tag: "t"},
		"untag":    {Type: "untag", Tag: "t"},
		"log":      {Type: "log", Text: "{{event}}"},
	}
	engine := jsvm.NewEngine(jsvm.Options{}) // compiled once, as the panel's is
	defer engine.Close(context.Background())
	for _, typ := range ActionTypes {
		for _, scheduled := range []bool{false, true} {
			if scheduled && slices.Contains(userActions, typ) {
				continue
			}
			for _, conds := range []bool{false, true} {
				r := Rule{Event: "user.created", Actions: []Action{samples[typ]}}
				if scheduled {
					a := samples[typ]
					if typ == "telegram" {
						a.Chat = "" // the chat from the settings: no user to write to
					}
					r = Rule{Schedule: "0 9 * * *", Actions: []Action{a}}
				}
				if conds {
					r.Conditions = []Condition{{Field: "event", Op: "not_empty"}}
				}
				files, err := Compile(Spec{ID: "combo", Version: "1.0.0", Name: "Combo", Rules: []Rule{r}}, "")
				if err != nil {
					t.Fatalf("%s: %v", typ, err)
				}
				res, err := devkit.RunTestFiles(context.Background(), files, devkit.TestOptions{Engine: engine})
				if err != nil {
					t.Fatalf("%s scheduled=%v conds=%v: %v\n%s", typ, scheduled, conds, err, files["main.js"])
				}
				for _, x := range res {
					if !x.OK {
						t.Errorf("%s scheduled=%v conds=%v: %s: %s", typ, scheduled, conds, x.Name, x.Error)
					}
				}
			}
		}
	}
}

// A message to a group or channel takes the chat from the plugin's settings, which
// the plugin then has; one to the user needs none.
func TestTelegramChatFromSettings(t *testing.T) {
	spec := Spec{ID: "tgchat", Version: "1.0.0", Name: "TG", Rules: []Rule{{
		Event: "user.created", Actions: []Action{{Type: "telegram", Text: "new: {{user.name}}"}},
	}}}
	files, err := Compile(spec, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(files["plugin.json"]), `"telegram_chat"`) {
		t.Fatalf("no chat setting:\n%s", files["plugin.json"])
	}
	files["test.js"] = []byte(`
test("writes to the chat from the settings", () => {
  mock.http("https://api.telegram.org/", { status: 200, body: "{}" });
  plugin.event("user.created", { id: 1, name: "Ann" });
  assert.equal(JSON.parse(mock.calls()[0].body).chat_id, "-100123");
});
`)
	res, err := devkit.RunTestFiles(context.Background(), files, devkit.TestOptions{})
	if err != nil || len(res) != 1 || !res[0].OK {
		t.Fatalf("%+v %v", res, err)
	}
	spec.Rules[0].Actions[0].Chat = "{{user.telegram_id}}"
	files, _ = Compile(spec, "")
	if strings.Contains(string(files["plugin.json"]), `"telegram_chat"`) {
		t.Fatal("a message to the user asks for a chat setting")
	}
	spec.Rules[0] = Rule{Schedule: "0 9 * * *", Actions: []Action{{Type: "telegram", Chat: "{{user.telegram_id}}", Text: "x"}}}
	if _, err := Compile(spec, ""); err == nil || !strings.Contains(err.Error(), "no user to write to") {
		t.Fatalf("a scheduled message to the user: %v", err)
	}
}
