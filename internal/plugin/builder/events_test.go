package builder

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/i18n/dictcheck"
	"github.com/AppsGanin/rospanel/internal/logbuf"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/plugin/devkit"
)

// Every event a rule can start on is described, in the catalog's order, and every
// description is in both dictionaries.
func TestEventCatalogComplete(t *testing.T) {
	c := EventCatalog()
	if len(c.Events) != len(model.WebhookEventCatalog) {
		t.Fatalf("%d events described, %d in the catalog", len(c.Events), len(model.WebhookEventCatalog))
	}
	hints := map[string]bool{}
	for i, e := range c.Events {
		if e.Event != model.WebhookEventCatalog[i] {
			t.Errorf("#%d: %s, the catalog has %s", i, e.Event, model.WebhookEventCatalog[i])
		}
		for _, fl := range e.Fields {
			if !strings.HasPrefix(fl.Path, "data.") || !fieldRe.MatchString(fl.Path) {
				t.Errorf("%s: field %q", e.Event, fl.Path)
			}
			hints[fl.Hint] = true
		}
	}
	for _, fl := range append(c.Context, c.User...) {
		hints[fl.Hint] = true
	}
	for _, dict := range []string{"ru.ts", "en.ts"} {
		d, err := dictcheck.Load(".", dict)
		if err != nil {
			t.Skipf("frontend dictionary not available (%v)", err)
		}
		for h := range hints {
			if _, ok := d.Resolve("evField." + h); !ok {
				t.Errorf("%s: evField.%s does not resolve", dict, h)
			}
		}
	}
}

// A user's events outside user.* — plan, balance, referral, promo — are a user's too:
// user.* reads them and the actions on a user reach the right one. The whole event
// goes as the HTTP body when none is typed.
func TestUserOutsideUserEvents(t *testing.T) {
	spec := Spec{ID: "plans", Version: "1.0.0", Name: "Plans", Rules: []Rule{{
		Event:      "plan.changed",
		Conditions: []Condition{{Field: "user.lang", Op: "eq", Value: "ru"}},
		Actions: []Action{
			{Type: "discord", Text: "{{user.name}}: {{data.prev_plan}} → {{data.plan}}"},
			{Type: "tag", Tag: "paid"},
			{Type: "http", URL: "https://hooks.example.com/plan"},
		},
	}}}
	files, err := Compile(spec, "")
	if err != nil {
		t.Fatal(err)
	}
	files["test.js"] = []byte(`
test("plan.changed is about its user", () => {
  mock.api("GET", "/v1/users/42", { status: 200, body: { data: { id: 42, tags: [] } } });
  mock.api("PATCH", "/v1/users/42", { status: 200, body: {} });
  mock.http("https://hooks.example.com/", { status: 200, body: "{}" });
  mock.http("https://discord.com/", { status: 204, body: "" });
  plugin.event("plan.changed", { id: 42, name: "ivan", lang: "ru", plan: "Месяц", prev_plan: "Пробный" });
  const calls = mock.calls();
  assert.equal(JSON.parse(calls[0].body).content, "ivan: Пробный → Месяц");
  assert.equal(JSON.parse(calls[2].body), { tags: ["paid"] });
  const sent = JSON.parse(calls[3].body);
  assert.equal(sent.event, "plan.changed");
  assert.equal(sent.data.plan, "Месяц");
  assert.ok(sent.id && sent.created_at > 0, JSON.stringify(sent));
});
`)
	var out strings.Builder
	res, err := devkit.RunTestFiles(context.Background(), files, devkit.TestOptions{Out: &out})
	if err != nil || len(res) != 1 || !res[0].OK {
		t.Fatalf("%+v %v\n%s", res, err, out.String())
	}
}

// An action on a user is refused on an event no user stands behind.
func TestUserActionNeedsUser(t *testing.T) {
	p := Validate(Spec{ID: "nodes", Version: "1.0.0", Name: "Nodes", Rules: []Rule{{
		Event: "node.down", Actions: []Action{{Type: "disable"}},
	}}})
	if len(p) != 1 || !strings.Contains(p[0], "not about a user") {
		t.Fatalf("%v", p)
	}
}

// {{path|format}}: times in the panel's timezone, the days left, bytes in GB,
// kopecks in roubles; an unknown format is refused before it runs.
func TestFormats(t *testing.T) {
	logbuf.SetLocation(time.FixedZone("MSK", 3*3600))
	defer logbuf.SetLocation(time.UTC)
	spec := Spec{ID: "fmts", Version: "1.0.0", Name: "Formats", Rules: []Rule{{
		Event: "user.expiring",
		Actions: []Action{{Type: "discord",
			Text: "{{user.expire_at|date}} | {{user.expire_at|datetime}} | {{data.until|days}} | {{data.used|gb}} | {{data.balance_kop|rub}} | {{data.none|date}} | {{user.expire_at}}"}},
	}}}
	files, err := Compile(spec, "")
	if err != nil {
		t.Fatal(err)
	}
	files["test.js"] = []byte(`
test("formats", () => {
  mock.http("https://discord.com/", { status: 204, body: "" });
  const until = Math.floor(Date.now() / 1000) + 2.5 * 86400;
  plugin.event("user.expiring", { id: 1, expire_at: 1769904000, until, used: 85899345920, balance_kop: 15050 });
  assert.equal(JSON.parse(mock.calls()[0].body).content,
    "01.02.2026 | 01.02.2026 03:00 | 3 | 80 | 150.5 | — | 1769904000");
});
`)
	var out strings.Builder
	res, err := devkit.RunTestFiles(context.Background(), files, devkit.TestOptions{Out: &out})
	if err != nil || len(res) != 1 || !res[0].OK {
		t.Fatalf("%+v %v\n%s", res, err, out.String())
	}
	spec.Rules[0].Actions[0].Text = "{{user.expire_at|weekday}}"
	if p := Validate(spec); len(p) != 1 || !strings.Contains(p[0], "unknown format |weekday") {
		t.Fatalf("%v", p)
	}
}

// What the review found: each refused before it runs, or run without failing.
func TestRuleTraps(t *testing.T) {
	spec := func(rules ...Rule) Spec { return Spec{ID: "traps", Version: "1.0.0", Name: "Traps", Rules: rules} }
	refused := map[string]Spec{
		"not about a user": spec(Rule{Event: "user.deleted", Actions: []Action{{Type: "tag", Tag: "gone"}}}),
		"without end":      spec(Rule{Event: "user.limits_changed", Actions: []Action{{Type: "extend", Days: 1}}}),
		"switch each other": spec(
			Rule{Event: "user.disabled", Actions: []Action{{Type: "enable"}}},
			Rule{Event: "user.enabled", Actions: []Action{{Type: "disable"}}}),
		"Discord takes no more": spec(Rule{Event: "user.created", Actions: []Action{{Type: "discord", Text: strings.Repeat("x", 2001)}}}),
		"a host name like":      spec(Rule{Event: "user.created", Actions: []Action{{Type: "http", URL: "https://a_b.example.com/x"}}}),
	}
	for want, s := range refused {
		if p := Validate(s); len(p) == 0 || !strings.Contains(strings.Join(p, "\n"), want) {
			t.Errorf("%s: %v", want, p)
		}
	}

	files, err := Compile(spec(
		Rule{Event: "registration.rejected", Actions: []Action{{Type: "tag", Tag: "rejected"}}},
		Rule{Event: "user.created", Actions: []Action{{Type: "untag", Tag: " VIP "}, {Type: "log", Text: "x __USER_EVENTS__ y"}}},
		Rule{Event: "user.expiring", Conditions: []Condition{{Field: "data.balance_kop", Op: "lt", Value: "1000"}},
			Actions: []Action{{Type: "discord", Text: "low"}}},
	), "")
	if err != nil {
		t.Fatal(err)
	}
	files["test.js"] = []byte(`
test("a rejection with no account behind it passes", () => {
  plugin.event("registration.rejected", { request_id: 1, name: "x", reason: "operator" });
  assert.equal(mock.calls().length, 0);
});
test("untag matches the tag as the panel stores it", () => {
  mock.api("GET", "/v1/users/5", { status: 200, body: { data: { id: 5, tags: ["vip", "x"] } } });
  mock.api("PATCH", "/v1/users/5", { status: 200, body: {} });
  plugin.event("user.created", { id: 5 });
  assert.equal(JSON.parse(mock.calls()[1].body), { tags: ["x"] });
});
test("an untag of a tag the user lacks writes nothing", () => {
  mock.api("GET", "/v1/users/6", { status: 200, body: { data: { id: 6, tags: ["x"] } } });
  plugin.event("user.created", { id: 6 });
  assert.equal(mock.calls().length, 1);
});
test("a field the event lacks is not less than anything", () => {
  mock.http("https://discord.com/", { status: 204, body: "" });
  plugin.event("user.expiring", { id: 7, days_left: 3 });
  assert.equal(mock.calls().length, 0);
  plugin.event("user.expiring", { id: 7, days_left: 3, balance_kop: 500 });
  assert.equal(mock.calls().length, 1);
});
`)
	var out strings.Builder
	res, err := devkit.RunTestFiles(context.Background(), files, devkit.TestOptions{Out: &out})
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	for _, r := range res {
		if !r.OK {
			t.Errorf("%s: %s", r.Name, r.Error)
		}
	}
}
