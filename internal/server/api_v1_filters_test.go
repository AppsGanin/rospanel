package server

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// Every query parameter the API advertises must actually change the answer.
//
// A declared-but-unwired filter fails the way the `groups` field did: the panel
// answers 200 with a body that quietly ignored what was asked, and the caller has
// no way to tell a filter that matched everything from one nobody read. Answering
// the wrong question confidently is worse than refusing it, and here it is worse
// still — an integration that pages the audit trail with ?actor= and gets the
// unfiltered trail back cannot detect that from the outside.
//
// So each parameter is called with a value that MUST change the response, against
// fixtures built to make it bite. The list is checked against the generated spec at
// the end, so a new parameter cannot be added without one.
func TestAPIQueryFiltersActuallyFilter(t *testing.T) {
	h, mgr, st := nodeAPITestServer(t)
	base, key := apiFixture(t, h, st)
	now := time.Now()
	day := func(n int) string { return now.AddDate(0, 0, -n).Format("2006-01-02") }

	// ---- fixtures: every filter below needs something it can exclude ----------
	alpha, err := mgr.CreateUser(t.Context(), "alpha", 0, 0)
	if err != nil {
		t.Fatalf("create alpha: %v", err)
	}
	beta, err := mgr.CreateUser(t.Context(), "beta", 0, 0)
	if err != nil {
		t.Fatalf("create beta: %v", err)
	}
	if err := st.SetUserEnabled(beta.ID, false); err != nil {
		t.Fatalf("disable beta: %v", err)
	}
	// One tagged user, so ?tag has someone to leave out.
	if err := st.SetUserTags(alpha.ID, []string{"vip"}); err != nil {
		t.Fatalf("tag alpha: %v", err)
	}

	// Two days of traffic for two users on two servers.
	for _, u := range []*model.User{alpha, beta} {
		for _, d := range []string{day(1), day(2)} {
			for _, node := range []int64{model.LocalNodeID, 2} {
				if err := st.AddDailyTrafficNode(u.ID, node, d, 1_000, 2_000); err != nil {
					t.Fatalf("traffic: %v", err)
				}
			}
		}
	}
	// Journals: two rows each, differing in every field a filter can name.
	for i, ev := range []model.UserEvent{
		{UserID: alpha.ID, UserName: "alpha", Action: model.EventUserCreated, ActorKind: "admin", ActorName: "root"},
		{UserID: beta.ID, UserName: "beta", Action: model.EventTrafficReset, ActorKind: "api", ActorName: "key"},
	} {
		ev.CreatedAt = now.Unix() - int64(i)
		if err := st.AddUserEvent(ev); err != nil {
			t.Fatalf("user event: %v", err)
		}
	}
	audits := auditFixtures(t, st, now)
	// Connections, devices and blocklist matches — two of each, so a limit bites.
	for i, ip := range []string{"198.51.100.1", "198.51.100.2"} {
		if err := st.AddConnection(alpha.ID, ip, now.Unix()-int64(i)); err != nil {
			t.Fatalf("connection: %v", err)
		}
		if _, err := st.RegisterDevice(alpha.ID, model.Device{
			HWID: fmt.Sprintf("hw-%d", i), OS: "ios", IP: ip,
			FirstSeen: now.Unix(), LastSeen: now.Unix(),
		}, 0); err != nil {
			t.Fatalf("device: %v", err)
		}
	}
	// Two config save-points, so the snapshot list has a second page to skip to.
	for _, label := range []string{"first", "second"} {
		if _, err := st.CreateConfigSnapshot(label, false, `{}`); err != nil {
			t.Fatalf("snapshot %s: %v", label, err)
		}
	}
	if err := st.AddAbuseMatches([]store.AbuseHit{
		{UserID: alpha.ID, Domain: "a.example", Category: "ads", Day: day(1), Count: 1, SeenAt: now.Unix()},
		{UserID: alpha.ID, Domain: "b.example", Category: "ads", Day: day(2), Count: 1, SeenAt: now.Unix()},
	}); err != nil {
		t.Fatalf("abuse: %v", err)
	}
	// Plans: one offered, one disabled, so include_disabled has something to add.
	for _, p := range []*model.TariffPlan{
		{Name: "Offered", Slug: "offered", PriceRub: 100, PeriodDays: 30, Enabled: true},
		{Name: "Hidden", Slug: "hidden", PriceRub: 200, PeriodDays: 30},
	} {
		if err := st.SaveTariffPlan(p); err != nil {
			t.Fatalf("plan %s: %v", p.Name, err)
		}
	}
	plans, err := st.ListTariffPlans(true)
	if err != nil || len(plans) < 2 {
		t.Fatalf("plans: %v (%d)", err, len(plans))
	}
	// Orders: two, one cancelled, so ?status excludes one.
	for i := range 2 {
		o, err := st.CreatePaymentOrder(alpha.ID, plans[0].ID, 100)
		if err != nil {
			t.Fatalf("order: %v", err)
		}
		if i == 1 {
			if err := st.SetPaymentOrderStatus(o.ID, "cancelled", 0); err != nil {
				t.Fatalf("cancel order: %v", err)
			}
		}
	}

	// ---- one biting value per advertised parameter ---------------------------
	// Keyed "<path>?<param>". The value must exclude something the unfiltered call
	// returns; identical bodies mean the parameter was read by nobody.
	probes := map[string]string{
		"/v1/users?status":                  "status=disabled",
		"/v1/users?search":                  "search=alpha",
		"/v1/users?tag":                     "tag=vip",
		"/v1/users?limit":                   "limit=1",
		"/v1/users?offset":                  "offset=1",
		"/v1/users/{id}/connections?limit":  "limit=1",
		"/v1/users/{id}/connections?offset": "offset=1",
		"/v1/users/{id}/devices?limit":      "limit=1",
		"/v1/users/{id}/devices?offset":     "offset=1",
		"/v1/users/{id}/abuse?limit":        "limit=1",
		"/v1/users/{id}/events?limit":       "limit=1",
		// This one's cursor has to be one of ALPHA's rows: a newer id belonging to
		// another user excludes nothing from alpha's own journal.
		"/v1/users/{id}/events?before":       "before=" + itoa64(lastEventIDFor(t, st, alpha.ID)),
		"/v1/events?action":                  "action=" + model.EventTrafficReset,
		"/v1/events?actor":                   "actor=api",
		"/v1/events?user_id":                 "user_id=" + itoa64(alpha.ID),
		"/v1/events?limit":                   "limit=1",
		"/v1/events?before":                  "before=" + itoa64(lastEventID(t, st)),
		"/v1/admin-audit?action":             "action=" + audits.action,
		"/v1/admin-audit?actor":              "actor=" + audits.actor,
		"/v1/admin-audit?category":           "category=" + audits.category,
		"/v1/admin-audit?limit":              "limit=1",
		"/v1/admin-audit?before":             "before=" + itoa64(audits.lastID),
		"/v1/billing/plans?include_disabled": "include_disabled=true",
		"/v1/billing/orders?status":          "status=cancelled",
		"/v1/billing/orders?limit":           "limit=1",
		"/v1/billing/orders?offset":          "offset=1",
		"/v1/stats/abuse?limit":              "limit=1",
		"/v1/stats/users?from":               "from=" + day(1),
		"/v1/stats/users?to":                 "to=" + day(2),
		"/v1/stats/users?limit":              "limit=1",
		"/v1/stats/users?offset":             "offset=1",
		"/v1/stats/series?from":              "from=" + day(1),
		"/v1/stats/series?to":                "to=" + day(2),
		"/v1/stats/series?user_id":           "user_id=" + itoa64(alpha.ID),
		"/v1/stats/nodes?from":               "from=" + day(1),
		"/v1/stats/nodes?to":                 "to=" + day(2),
		"/v1/stats/nodes?user_id":            "user_id=" + itoa64(alpha.ID),
		"/v1/stats/nodes/series?from":        "from=" + day(1),
		"/v1/stats/nodes/series?to":          "to=" + day(2),
		"/v1/stats/nodes/series?user_id":     "user_id=" + itoa64(alpha.ID),
		// The configuration lists page like every other list.
		"/v1/config/snapshots?limit":  "limit=1",
		"/v1/config/snapshots?offset": "offset=1",
		"/v1/stats/countries?limit":   "limit=1",
		"/v1/stats/countries?offset":  "offset=1",
		"/v1/stats/asns?limit":        "limit=1",
		"/v1/stats/asns?offset":       "offset=1",
	}

	// ---- walk the published spec, so nothing can be added untested -----------
	spec := OpenAPISpec(base)
	paths, _ := spec["paths"].(map[string]any)
	var pairs []string
	for path, item := range paths {
		ops, _ := item.(map[string]any)
		op, _ := ops["get"].(map[string]any)
		if op == nil {
			continue
		}
		params, _ := op["parameters"].([]any)
		for _, raw := range params {
			p, _ := raw.(map[string]any)
			if in, _ := p["in"].(string); in != "query" {
				continue
			}
			name, _ := p["name"].(string)
			pairs = append(pairs, path+"?"+name)
		}
	}
	sort.Strings(pairs)

	for _, pair := range pairs {
		query, ok := probes[pair]
		if !ok {
			t.Errorf("%s is advertised but this test has no value that would prove it works", pair)
			continue
		}
		path, _, _ := strings.Cut(pair, "?")
		target := base + strings.ReplaceAll(path, "{id}", itoa64(alpha.ID))

		plain := apiGet(t, h, target, key)
		if plain.Code != http.StatusOK {
			t.Errorf("%s: unfiltered call answered %d: %s", pair, plain.Code, plain.Body.String())
			continue
		}
		filtered := apiGet(t, h, target+"?"+query, key)
		if filtered.Code != http.StatusOK {
			t.Errorf("%s: ?%s answered %d: %s", pair, query, filtered.Code, filtered.Body.String())
			continue
		}
		if plain.Body.String() == filtered.Body.String() {
			t.Errorf("%s: ?%s changed nothing — the parameter is advertised but not applied", pair, query)
		}
	}
}

// A list that takes a window must report one, and one that reports a window must
// take it. The pair is what makes paging discoverable: `meta.total` is how a caller
// learns there is a second page at all, so a route offering limit/offset without it
// silently truncates, and a route promising meta without accepting a window
// describes paging nobody can drive.
func TestAPIPagedListsDeclareAndReturnTheirWindow(t *testing.T) {
	h, mgr, st := nodeAPITestServer(t)
	base, key := apiFixture(t, h, st)
	user, err := mgr.CreateUser(t.Context(), "paged", 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	spec := OpenAPISpec(base)
	paths, _ := spec["paths"].(map[string]any)
	for path, item := range paths {
		ops, _ := item.(map[string]any)
		op, _ := ops["get"].(map[string]any)
		if op == nil {
			continue
		}
		params, _ := op["parameters"].([]any)
		takesWindow := false
		for _, raw := range params {
			p, _ := raw.(map[string]any)
			if name, _ := p["name"].(string); name == "offset" {
				takesWindow = true
			}
		}
		declaresMeta := strings.Contains(responseSchemaKeys(op), "meta")
		if takesWindow != declaresMeta {
			t.Errorf("%s: accepts offset=%v but declares meta=%v — paging is half-described",
				path, takesWindow, declaresMeta)
			continue
		}
		if !declaresMeta {
			continue
		}
		target := base + strings.ReplaceAll(path, "{id}", itoa64(user.ID))
		rec := apiGet(t, h, target, key)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: %d %s", path, rec.Code, rec.Body.String())
			continue
		}
		if !strings.Contains(rec.Body.String(), `"meta"`) {
			t.Errorf("%s promises a meta block and returned none: %s", path, rec.Body.String())
		}
	}
}

// responseSchemaKeys flattens an operation's success-response property names.
func responseSchemaKeys(op map[string]any) string {
	responses, _ := op["responses"].(map[string]any)
	for status, raw := range responses {
		if !strings.HasPrefix(status, "2") {
			continue
		}
		resp, _ := raw.(map[string]any)
		content, _ := resp["content"].(map[string]any)
		js, _ := content["application/json"].(map[string]any)
		schema, _ := js["schema"].(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		var names []string
		for k := range props {
			names = append(names, k)
		}
		sort.Strings(names)
		return strings.Join(names, ",")
	}
	return ""
}

// auditIDs carries the values that make each admin-audit filter bite.
type auditIDs struct {
	action, actor, category string
	lastID                  int64
}

// auditFixtures writes two admin rows that differ in action, actor and category, and
// reports the values to filter on. The action is taken from the panel's own catalog
// so the category filter — which expands to the actions it holds — has a real one.
func auditFixtures(t *testing.T, st *store.Store, now time.Time) auditIDs {
	t.Helper()
	category := model.AdminAuditCategories[0]
	actions := model.AdminAuditActionsIn(category)
	if len(actions) == 0 {
		t.Fatalf("category %q holds no actions", category)
	}
	// A second action from any OTHER category, so filtering by the first excludes it.
	var other string
	for _, cat := range model.AdminAuditCategories[1:] {
		if in := model.AdminAuditActionsIn(cat); len(in) > 0 {
			other = in[0]
			break
		}
	}
	if other == "" {
		t.Fatal("only one admin-audit category has actions; the category filter cannot be proven")
	}
	rows := []model.AdminAudit{
		{Action: actions[0], ActorKind: "admin", ActorName: "root", IP: "203.0.113.1"},
		{Action: other, ActorKind: "api", ActorName: "integration", IP: "203.0.113.2"},
	}
	for i, ev := range rows {
		ev.CreatedAt = now.Unix() - int64(i)
		if err := st.AddAdminAudit(ev); err != nil {
			t.Fatalf("admin audit: %v", err)
		}
	}
	list, err := st.ListAdminAudit(store.AdminAuditFilter{Limit: 10})
	if err != nil || len(list) < 2 {
		t.Fatalf("read back admin audit: %v (%d rows)", err, len(list))
	}
	return auditIDs{
		action: actions[0], actor: "root", category: category,
		lastID: list[0].ID,
	}
}

// lastEventID is the newest user-event id, used as a paging cursor.
func lastEventID(t *testing.T, st *store.Store) int64 {
	t.Helper()
	events, err := st.ListEvents(store.UserEventFilter{Limit: 1})
	if err != nil || len(events) == 0 {
		t.Fatalf("read back user events: %v (%d rows)", err, len(events))
	}
	return events[0].ID
}

// lastEventIDFor is the newest event id belonging to one user, and insists the user
// has another one behind it — a cursor with nothing to exclude would let an ignored
// `before` pass as working.
func lastEventIDFor(t *testing.T, st *store.Store, userID int64) int64 {
	t.Helper()
	events, err := st.ListEvents(store.UserEventFilter{UserID: userID, Limit: 10})
	if err != nil || len(events) < 2 {
		t.Fatalf("user %d needs at least two events to page: %v (%d rows)", userID, err, len(events))
	}
	return events[0].ID
}

func itoa64(v int64) string { return uid(v) }
