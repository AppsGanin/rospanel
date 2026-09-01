package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/mcp"
	"github.com/AppsGanin/rospanel/internal/model"
)

// Every MCP tool, called for real, through the endpoint an assistant is pointed at.
//
// The translator's own tests can prove a call is turned into the right HTTP request
// and still miss the failure that matters: the panel refusing it. That is how
// post_users shipped answering "invalid request body" — the schema was right, the
// route was right, and the round trip had never been made. Nothing short of calling
// each tool would have caught it.
//
// The sweep is complete by construction. The read half is generated (a GET takes no
// arguments or takes an id, and the id is the only thing worth writing down), the
// write half is a script, and at the end every tool the endpoint offers must have
// been called — so an endpoint added to /v1 cannot reach an assistant untested.
func TestMCPEveryToolAnswers(t *testing.T) {
	h, mgr, st := nodeAPITestServer(t)
	base, key := apiFixture(t, h, st)
	url := base + "/v1/mcp/" + key + "/write"

	// A webhook needs somewhere to deliver to for the test-delivery tool to mean
	// anything.
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer hook.Close()

	called := map[string]bool{}
	// callRaw invokes one tool and returns its answer, or the reason it produced
	// none. Errors are returned rather than raised so one broken tool doesn't hide
	// the state of the other seventy-eight.
	callRaw := func(tool string, args map[string]any) (text, failure string) {
		called[tool] = true
		params, err := json.Marshal(map[string]any{"name": tool, "arguments": args})
		if err != nil {
			t.Fatalf("%s: encode arguments: %v", tool, err)
		}
		rec := rpc(t, h, url, fmt.Sprintf(
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":%s}`, params))
		if rec.Code != http.StatusOK {
			return "", fmt.Sprintf("HTTP %d: %s", rec.Code, rec.Body.String())
		}
		var out struct {
			Result *struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
				IsError bool `json:"isError"`
			} `json:"result"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			return "", fmt.Sprintf("undecodable reply: %v (%s)", err, rec.Body.String())
		}
		if out.Error != nil {
			return "", out.Error.Message
		}
		if out.Result == nil || len(out.Result.Content) == 0 {
			return "", "empty reply: " + rec.Body.String()
		}
		if text := out.Result.Content[0].Text; !out.Result.IsError {
			return text, ""
		} else {
			return "", text
		}
	}
	call := func(tool string, args map[string]any) string {
		text, failure := callRaw(tool, args)
		if failure != "" {
			t.Errorf("%s: %s", tool, failure)
		}
		return text
	}
	// newID reads the id out of a create call's answer. A tool that cannot say what
	// it made is a broken tool, so this reports rather than skipping quietly. An
	// order carries its id one level down, under the payment instructions.
	newID := func(tool, text string) int64 {
		if text == "" {
			return 0
		}
		var out struct {
			Data struct {
				ID    int64 `json:"id"`
				Order struct {
					ID int64 `json:"id"`
				} `json:"order"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			t.Errorf("%s: undecodable answer: %v (%s)", tool, err, text)
			return 0
		}
		if id := max(out.Data.ID, out.Data.Order.ID); id != 0 {
			return id
		}
		t.Errorf("%s: no id in the answer: %s", tool, text)
		return 0
	}

	// ---- fixtures the read half needs to have something to look at ----------
	user, err := mgr.CreateUser(t.Context(), "sweep-user", 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	node, err := mgr.CreateNode("sweep-node", "sweep.example.com")
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	plan := &model.TariffPlan{Name: "Sweep base", Slug: "sweep-base", PriceRub: 100, PeriodDays: 30}
	if err := st.SaveTariffPlan(plan); err != nil {
		t.Fatalf("create plan: %v", err)
	}
	order, err := st.CreatePaymentOrder(user.ID, plan.ID, 100)
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	for _, name := range []string{"sweep-approve", "sweep-reject"} {
		if _, err := st.CreateRegistrationRequest(int64(len(name)), name, 1_700_000_000); err != nil {
			t.Fatalf("create registration %s: %v", name, err)
		}
	}
	regs, err := st.ListRegistrationRequests()
	if err != nil || len(regs) < 2 {
		t.Fatalf("list registrations: %v (%d rows)", err, len(regs))
	}

	// ---- the read half, generated ------------------------------------------
	// Which id a GET wants is the only thing a machine can't work out.
	readIDs := map[string]int64{
		"get_users_by_id":             user.ID,
		"get_users_by_id_abuse":       user.ID,
		"get_users_by_id_connections": user.ID,
		"get_users_by_id_devices":     user.ID,
		"get_users_by_id_events":      user.ID,
		"get_nodes_by_id":             node.ID,
		"get_nodes_by_id_health":      node.ID,
		"get_nodes_by_id_logs":        model.LocalNodeID,
		"get_servers_by_id_inbounds":  model.LocalNodeID,
		"get_servers_by_id_routing":   model.LocalNodeID,
		"get_billing_orders_by_id":    order.ID,
	}
	tools := mcp.BuildTools(OpenAPISpec(base), true)
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	for _, tool := range tools {
		if tool.Mutating() {
			continue
		}
		args := map[string]any{}
		if id, ok := readIDs[tool.Name]; ok {
			args["id"] = id
		} else if strings.Contains(tool.Name, "_by_id") {
			t.Errorf("%s takes an id and this sweep has none for it", tool.Name)
			continue
		}
		if tool.Name == "get_healthz" {
			// The liveness probe answers 503 while Xray is down, which it is in a test
			// process with no Xray binary. Reporting that IS the tool working; what
			// would be wrong is any other failure.
			if _, failure := callRaw(tool.Name, args); !strings.Contains(failure, `"xray":"down"`) {
				t.Errorf("get_healthz: expected an honest 503, got: %s", failure)
			}
			continue
		}
		call(tool.Name, args)
	}

	// ---- the write half, in an order that leaves the panel as it found it ---
	created := newID("post_users", call("post_users", map[string]any{
		"body": map[string]any{"name": "sweep-created", "device_limit": 3},
	}))
	call("patch_users_by_id", map[string]any{
		"id":   created,
		"body": map[string]any{"name": "sweep-renamed", "speed_limit": 2048},
	})
	call("post_users_by_id_reset", map[string]any{"id": created})
	call("post_users_by_id_reset_period", map[string]any{
		"id": created, "body": map[string]any{"period": "monthly"},
	})
	call("post_users_by_id_rotate_sub", map[string]any{"id": created})
	call("post_users_by_id_devices_unbind", map[string]any{
		"id": created, "body": map[string]any{"all": true},
	})
	call("post_users_bulk", map[string]any{
		"body": map[string]any{"ids": []int64{created}, "action": "extend", "days": 3},
	})

	group := newID("post_groups", call("post_groups", map[string]any{
		"body": map[string]any{"name": "sweep-group", "grants": []string{}},
	}))
	call("post_groups_by_id", map[string]any{
		"id": group, "body": map[string]any{"name": "sweep-group-renamed", "grants": []string{}},
	})
	call("post_groups_by_id_members", map[string]any{
		"id": group, "body": map[string]any{"user_ids": []int64{created}},
	})
	call("post_users_by_id_groups", map[string]any{
		"id": created, "body": map[string]any{"group_ids": []int64{group}},
	})

	// A plan is created without an id (that is what makes it a create) and a second
	// one exists only so the migrate tool has somewhere to move users to.
	made := newID("post_billing_plans", call("post_billing_plans", map[string]any{
		"body": map[string]any{
			"name": "Sweep plan", "price_rub": 200, "period_days": 30, "enabled": true,
		},
	}))
	target := newID("post_billing_plans", call("post_billing_plans", map[string]any{
		"body": map[string]any{
			"name": "Sweep target", "price_rub": 300, "period_days": 30, "enabled": true,
		},
	}))
	call("post_users_by_id_plan", map[string]any{
		"id": created, "body": map[string]any{"plan_id": made, "extend_from_current": false},
	})
	call("post_users_by_id_plan_cancel", map[string]any{"id": created})
	call("post_billing_plans_by_id_migrate", map[string]any{
		"id": made, "body": map[string]any{"to_plan_id": target},
	})
	call("post_billing_settings", map[string]any{
		"body": map[string]any{
			"enabled": true, "free_plan_id": 0, "trial_plan_id": 0,
			"payment_note": "sweep",
		},
	})
	call("post_payments", map[string]any{
		"body": map[string]any{
			"key": "cryptobot", "enabled": false, "config": map[string]any{"token": "1:aa"},
		},
	})

	// A manual order is REUSED while it is still pending (see manager.manualOrder), so
	// asking twice hands back the same id. Cancel that one first and let the second call
	// mint a fresh order to confirm — each tool then acts on an order in the state it
	// actually accepts, instead of cancelling one that was already paid.
	cancel := newID("post_billing_orders", call("post_billing_orders", map[string]any{
		"body": map[string]any{"user_id": created, "plan_id": made},
	}))
	call("post_billing_orders_by_id_cancel", map[string]any{"id": cancel})
	confirm := newID("post_billing_orders", call("post_billing_orders", map[string]any{
		"body": map[string]any{"user_id": created, "plan_id": made},
	}))
	call("post_billing_orders_by_id_confirm", map[string]any{"id": confirm})

	// The event key comes from the catalog rather than a literal: a webhook that
	// subscribes to an event the panel doesn't have is rejected, and hard-coding one
	// would make this test fail the day the catalog is renamed for an unrelated reason.
	event := firstEventKey(t, call("get_webhooks_events", map[string]any{}))
	hookID := newID("post_webhooks", call("post_webhooks", map[string]any{
		"body": map[string]any{"url": hook.URL, "events": []string{event}},
	}))
	call("post_webhooks_by_id", map[string]any{
		"id": hookID,
		"body": map[string]any{
			"url": hook.URL, "events": []string{event}, "enabled": true,
		},
	})
	call("post_webhooks_by_id_test", map[string]any{"id": hookID})

	inbound := newID("post_servers_by_id_inbounds", call("post_servers_by_id_inbounds", map[string]any{
		"id": model.LocalNodeID,
		"body": map[string]any{
			"enabled": true, "name": "sweep-in", "protocol": model.InbVLESS, "port": 21001,
			"transport": model.TrWS, "security": model.SecTLS, "path": "/sweep",
		},
	}))
	call("post_inbounds_by_id", map[string]any{
		"id": inbound,
		"body": map[string]any{
			"enabled": true, "name": "sweep-in-2", "protocol": model.InbVLESS, "port": 21002,
			"transport": model.TrWS, "security": model.SecTLS, "path": "/sweep",
		},
	})

	// The configuration surface. Settings is a partial update, so one field is enough
	// to prove the shape; routing is a full replace against the master (server 0).
	call("patch_settings", map[string]any{
		"body": map[string]any{"user_autodelete_days": 7},
	})
	call("post_servers_by_id_routing", map[string]any{
		"id": model.LocalNodeID,
		"body": map[string]any{
			"routing":       map[string]any{"block_ads": true},
			"xray_dns":      "1.1.1.1",
			"warp_enabled":  false,
			"opera_enabled": false,
			"opera_country": "EU",
		},
	})
	call("post_servers_by_id_xray_restart", map[string]any{"id": model.LocalNodeID})

	snap := newID("post_config_snapshots", call("post_config_snapshots", map[string]any{
		"body": map[string]any{"label": "sweep"},
	}))
	call("post_config_snapshots_by_id_rollback", map[string]any{"id": snap})
	// Rolling back takes an auto-snapshot of its own, so this deletes a save-point that
	// still exists rather than the one just restored from.
	call("delete_config_snapshots_by_id", map[string]any{"id": snap})

	added := newID("post_nodes", call("post_nodes", map[string]any{
		"body": map[string]any{"name": "sweep-added", "host": "added.example.com"},
	}))
	call("patch_nodes_by_id", map[string]any{
		"id": added, "body": map[string]any{"name": "sweep-added-renamed"},
	})
	call("post_nodes_by_id_enabled", map[string]any{
		"id": added, "body": map[string]any{"enabled": false},
	})
	call("post_nodes_by_id_regen_join", map[string]any{"id": added})
	call("post_nodes_by_id_proxy", map[string]any{
		"id": added,
		"body": map[string]any{
			"socks_enabled": false, "socks_port": 0,
			"http_enabled": false, "http_port": 0, "accounts": []any{},
		},
	})
	call("post_nodes_by_id_update", map[string]any{"id": added})
	call("post_nodes_update_all", map[string]any{})

	call("post_registrations_by_id_approve", map[string]any{"id": regs[0].ID})
	call("post_registrations_by_id_reject", map[string]any{"id": regs[1].ID})

	// Deletions last, so everything above had something to work on.
	call("delete_webhooks_by_id", map[string]any{"id": hookID})
	call("delete_inbounds_by_id", map[string]any{"id": inbound})
	call("delete_groups_by_id", map[string]any{"id": group})
	call("delete_billing_plans_by_id", map[string]any{"id": target})
	call("delete_nodes_by_id", map[string]any{"id": added})
	call("delete_users_by_id", map[string]any{"id": created})

	// ---- the guarantee -----------------------------------------------------
	for _, tool := range tools {
		if !called[tool.Name] {
			t.Errorf("%s is offered to assistants but never called here — add it to the sweep", tool.Name)
		}
	}
}

// An id that matches nothing is the single most likely mistake an assistant makes:
// it reads an id out of one answer and uses it against the wrong resource. Every
// tool that takes one is called with an id that exists nowhere, and none of them
// may answer 500 — that status says "the panel broke", which sends a caller
// retrying a call that will never work, and it used to arrive with the storage
// layer's own words attached ("sql: no rows in result set").
func TestMCPToolsRejectMissingIDsWithoutBlamingThePanel(t *testing.T) {
	h, _, st := nodeAPITestServer(t)
	base, key := apiFixture(t, h, st)
	url := base + "/v1/mcp/" + key + "/write"

	// Bodies for the tools that need one; without it the call fails on the body and
	// never reaches the id at all.
	bodies := map[string]any{
		"patch_nodes_by_id":                map[string]any{"name": "ghost"},
		"patch_users_by_id":                map[string]any{"name": "ghost"},
		"post_billing_plans_by_id_migrate": map[string]any{"to_plan_id": 999_001},
		"post_groups_by_id":                map[string]any{"name": "ghost", "grants": []string{}},
		"post_groups_by_id_members":        map[string]any{"user_ids": []int64{999_002}},
		"post_inbounds_by_id": map[string]any{
			"name": "ghost", "protocol": model.InbVLESS, "port": 21050,
			"transport": model.TrWS, "security": model.SecTLS, "path": "/ghost",
		},
		"post_nodes_by_id_enabled": map[string]any{"enabled": false},
		"post_nodes_by_id_proxy": map[string]any{
			"socks_enabled": false, "socks_port": 0,
			"http_enabled": false, "http_port": 0, "accounts": []any{},
		},
		"post_servers_by_id_inbounds": map[string]any{
			"name": "ghost", "protocol": model.InbVLESS, "port": 21051,
			"transport": model.TrWS, "security": model.SecTLS, "path": "/ghost",
		},
		"post_users_by_id_devices_unbind": map[string]any{"all": true},
		"post_users_by_id_groups":         map[string]any{"group_ids": []int64{999_003}},
		"post_users_by_id_plan":           map[string]any{"plan_id": 999_004},
		"post_users_by_id_reset_period":   map[string]any{"period": "monthly"},
		"post_webhooks_by_id": map[string]any{
			"url": "https://example.com/hook", "events": []string{"user.created"},
		},
	}
	for _, tool := range mcp.BuildTools(OpenAPISpec(base), true) {
		if !tool.HasParam("id") {
			continue
		}
		args := map[string]any{"id": 999_999}
		if body, ok := bodies[tool.Name]; ok {
			args["body"] = body
		}
		params, err := json.Marshal(map[string]any{"name": tool.Name, "arguments": args})
		if err != nil {
			t.Fatalf("%s: encode arguments: %v", tool.Name, err)
		}
		rec := rpc(t, h, url, fmt.Sprintf(
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":%s}`, params))
		// Answering successfully is allowed — an empty list for a server that holds
		// nothing is a true answer. Blaming the panel is not.
		if body := rec.Body.String(); strings.Contains(body, "panel answered 5") {
			t.Errorf("%s with a missing id: %s", tool.Name, body)
		}
	}
}

// The panel used to accept a body it did not understand and report success, which
// is the worst of the three possible outcomes: the caller believes the change
// landed. Reported against POST /v1/users/{id}/groups, fixed for every route at
// once in apiDecode.
func TestAPIRejectsUnknownBodyFields(t *testing.T) {
	h, mgr, st := nodeAPITestServer(t)
	user, err := mgr.CreateUser(t.Context(), "strict", 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	group, err := st.CreateGroup("strict-group", nil)
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	base, key := apiFixture(t, h, st)

	// "groups" instead of "group_ids": the exact typo that answered {"ok":true}.
	rec := postJSON(t, h, base+"/v1/users/"+uid(user.ID)+"/groups", key,
		map[string]any{"groups": []int64{group.ID}})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown field: status %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "groups") {
		t.Errorf("the error doesn't name the field: %s", rec.Body.String())
	}
	if groups, err := st.GroupsForUser(user.ID); err != nil || len(groups) != 0 {
		t.Errorf("a rejected call changed membership: %v (%d groups)", err, len(groups))
	}

	// The correct field still works, so the check didn't simply break the route.
	if rec := postJSON(t, h, base+"/v1/users/"+uid(user.ID)+"/groups", key,
		map[string]any{"group_ids": []int64{group.ID}}); rec.Code != http.StatusOK {
		t.Fatalf("group_ids: status %d, body %s", rec.Code, rec.Body.String())
	}
	groups, err := st.GroupsForUser(user.ID)
	if err != nil || len(groups) != 1 {
		t.Errorf("membership not applied: %v (%d groups)", err, len(groups))
	}
}

// firstEventKey picks any real event key out of the catalog answer.
func firstEventKey(t *testing.T, text string) string {
	t.Helper()
	var out struct {
		Data []struct {
			Key string `json:"key"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil || len(out.Data) == 0 {
		t.Fatalf("webhook event catalog is unusable: %v (%s)", err, text)
	}
	return out.Data[0].Key
}

// A rejected field should say which mistake it was.
//
// "Read the object, change one thing, send it back" is a natural way to drive a REST
// API, and it trips over every server-owned key the response carries. That is not
// the same mistake as a typo, and answering both with "unknown field" leaves the
// caller auditing their own spelling for a body that was almost right.
//
// Both remain a 400. Accepting the read-only keys quietly would mean accepting a
// `member_ids` that changes no membership — the very failure the strictness is for.
func TestAPINamesTheKindOfBadField(t *testing.T) {
	h, _, st := nodeAPITestServer(t)
	group, err := st.CreateGroup("round-trip", nil)
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	base, key := apiFixture(t, h, st)
	target := base + "/v1/groups/" + uid(group.ID)

	// A field the group RESPONSE carries — what a round trip picks up.
	rec := postJSON(t, h, target, key, map[string]any{
		"id": group.ID, "name": "renamed", "grants": []string{},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("echoed id: status %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "read-only") {
		t.Errorf("a round-tripped field should be named as read-only, got: %s", body)
	}

	// A field that is nobody's: still rejected, but as the wrong name it is.
	rec = postJSON(t, h, target, key, map[string]any{
		"title": "renamed", "grants": []string{},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("typo: status %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "unknown field") || strings.Contains(body, "read-only") {
		t.Errorf("a name the API has nowhere should be named as unknown, got: %s", body)
	}

	// And the body that was actually asked for still works.
	if rec := postJSON(t, h, target, key, map[string]any{
		"name": "renamed", "grants": []string{},
	}); rec.Code != http.StatusOK {
		t.Errorf("clean body: status %d, body %s", rec.Code, rec.Body.String())
	}
}

// A write that answers 200 must have changed something.
//
// The sweep proves every tool is callable and the filter test proves query
// parameters are applied; this is the third face of the same question and the one
// that costs money when it's wrong — a body field the schema advertises, the panel
// accepts, and the handler forgets. `speed_limit` on create is one plan-application
// reorder away from being dropped silently, and the caller would see a 201 with a
// user on the wrong terms.
//
// Every field the two user-shaped bodies declare is sent and then read back out of
// the store. The list is checked against the generated schema, so a field added to
// the request struct without being wired into the handler fails here.
func TestMCPUserWritesReachTheStore(t *testing.T) {
	h, _, st := nodeAPITestServer(t)
	base, key := apiFixture(t, h, st)
	url := base + "/v1/mcp/" + key + "/write"

	call := func(tool string, args map[string]any) {
		t.Helper()
		params, err := json.Marshal(map[string]any{"name": tool, "arguments": args})
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		rec := rpc(t, h, url, fmt.Sprintf(
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":%s}`, params))
		if body := rec.Body.String(); rec.Code != http.StatusOK || strings.Contains(body, `"isError":true`) {
			t.Fatalf("%s: %d %s", tool, rec.Code, body)
		}
	}
	// value is what to send; read pulls the same thing back off the stored user.
	// want, when set, is what read should return for value — for a field the server
	// normalises on the way in (tags), so the two are not equal by design. Left nil,
	// value itself is expected back.
	type field struct {
		value any
		read  func(model.User) any
		want  any
	}
	joined := func(u model.User) any { return strings.Join(u.Tags, ",") }
	fields := map[string]field{
		"name":         {value: "written", read: func(u model.User) any { return u.Name }},
		"enabled":      {value: false, read: func(u model.User) any { return u.Enabled }},
		"data_limit":   {value: float64(4096), read: func(u model.User) any { return float64(u.DataLimit) }},
		"expire_at":    {value: float64(2_000_000_000), read: func(u model.User) any { return float64(u.ExpireAt) }},
		"device_limit": {value: float64(4), read: func(u model.User) any { return float64(u.DeviceLimit) }},
		"speed_limit":  {value: float64(3072), read: func(u model.User) any { return float64(u.SpeedLimit) }},
		"note":         {value: "from the sweep", read: func(u model.User) any { return u.Note }},
		// Mixed case and a stray space: the stored form is what the model normalises to.
		"tags": {value: []any{"VIP", "beta "}, read: joined, want: "beta,vip"},
	}
	expected := func(f field) any {
		if f.want != nil {
			return f.want
		}
		return f.value
	}

	// Create with everything the body accepts, then read the account back.
	body := map[string]any{}
	for name, f := range fields {
		body[name] = f.value
	}
	// enabled isn't a create field, and a plan or groups would overwrite the limits
	// under test — those are the sweep's business.
	delete(body, "enabled")
	call("post_users", body)
	users, err := st.ListUsers()
	if err != nil || len(users) != 1 {
		t.Fatalf("read back: %v (%d users)", err, len(users))
	}
	created := users[0]
	for name, f := range fields {
		if name == "enabled" {
			continue
		}
		if got, want := f.read(created), expected(f); got != want {
			t.Errorf("post_users: %s was sent as %v and stored as %v (want %v)", name, f.value, got, want)
		}
	}

	// Now change every one of them through the patch tool, to different values.
	patch := map[string]field{
		"name": {value: "patched"}, "enabled": {value: false},
		"data_limit": {value: float64(8192)}, "expire_at": {value: float64(2_100_000_000)},
		"device_limit": {value: float64(7)}, "speed_limit": {value: float64(1536)},
		"note": {value: "patched note"},
		"tags": {value: []any{"gold"}, want: "gold"},
	}
	patchBody := map[string]any{}
	for name, f := range patch {
		patchBody[name] = f.value
	}
	call("patch_users_by_id", map[string]any{"id": created.ID, "body": patchBody})
	after, err := st.GetUser(created.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	for name, f := range patch {
		if got, want := fields[name].read(*after), expected(f); got != want {
			t.Errorf("patch_users_by_id: %s was sent as %v and stored as %v (want %v)", name, f.value, got, want)
		}
	}
	// And that an empty list clears the tags, since "omit" and "empty" differ here.
	call("patch_users_by_id", map[string]any{"id": created.ID, "body": map[string]any{"tags": []any{}, "note": ""}})
	cleared, err := st.GetUser(created.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if len(cleared.Tags) != 0 || cleared.Note != "" {
		t.Errorf("patch with empty tags/note should clear them, got tags=%v note=%q", cleared.Tags, cleared.Note)
	}

	// The guarantee: every field the schemas advertise is exercised above.
	for _, tool := range mcp.BuildTools(OpenAPISpec(base), true) {
		if tool.Name != "post_users" && tool.Name != "patch_users_by_id" {
			continue
		}
		props, _ := tool.InputSchema["properties"].(map[string]any)
		schema, _ := props["body"].(map[string]any)
		declared, _ := schema["properties"].(map[string]any)
		for name := range declared {
			switch name {
			case "plan_id", "group_ids": // applied by their own tools, checked in the sweep
				continue
			}
			if _, ok := fields[name]; !ok {
				t.Errorf("%s accepts %q and nothing here proves it lands", tool.Name, name)
			}
		}
	}
}
