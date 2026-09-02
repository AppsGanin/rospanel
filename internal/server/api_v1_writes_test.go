package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/mcp"
	"github.com/AppsGanin/rospanel/internal/model"
)

// The other half of "a write that answers 200 must have changed something".
//
// TestMCPUserWritesReachTheStore covers the two user-shaped bodies. These are the
// rest: nodes, their system proxy, inbounds, webhooks and billing. The failure this
// guards against is a body field that is advertised in the schema, accepted by the
// decoder and then dropped on the floor — the caller gets a success and the panel
// keeps the old value, which is the same silence that made `groups` a bug.
//
// Each tool sends every field it declares and reads the result back out of the
// store, and each is checked against the generated schema so a newly added field
// has to be accounted for here.

// writeProbe is one tool's landing test.
type writeProbe struct {
	// body is what to send; check reads the stored object and returns the fields it
	// found, keyed by the JSON name they were sent under.
	body  map[string]any
	check func(t *testing.T) map[string]any
	// derived are fields the panel legitimately owns: the server computes or
	// normalises them, so what comes back is not what was sent.
	derived []string
}

func TestMCPFleetWritesReachTheStore(t *testing.T) {
	h, mgr, st := nodeAPITestServer(t)
	base, key := apiFixture(t, h, st)
	url := base + "/v1/mcp/" + key + "/write"

	call := func(tool string, args map[string]any) string {
		t.Helper()
		params, err := json.Marshal(map[string]any{"name": tool, "arguments": args})
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		rec := rpc(t, h, url, fmt.Sprintf(
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":%s}`, params))
		body := rec.Body.String()
		if rec.Code != http.StatusOK || strings.Contains(body, `"isError":true`) {
			t.Fatalf("%s: %d %s", tool, rec.Code, body)
		}
		return body
	}

	node, err := mgr.CreateNode("landing", "landing.example.com")
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer hook.Close()

	getNode := func(t *testing.T) *model.Node {
		t.Helper()
		n, err := st.GetNode(node.ID)
		if err != nil {
			t.Fatalf("get node: %v", err)
		}
		return n
	}
	deref := func(b *bool) any {
		if b == nil {
			return nil
		}
		return *b
	}

	probes := map[string]writeProbe{
		"patch_nodes_by_id": {
			body: map[string]any{
				"name": "renamed", "host": "renamed.example.com",
				"decoy_template": "nginx", "vless_enabled": false,
				"hysteria_enabled": false, "reality_enabled": true,
				"xray_dns": "1.1.1.1", "warp_enabled": true,
				"opera_enabled": true, "opera_country": "EU",
				"traffic_coefficient": float64(2),
				"routing":             map[string]any{},
				// Placement: the country is normalised to upper case on the way in.
				"country": "nl", "sort_weight": float64(7), "capacity": float64(120),
				"hide_when_full": true,
			},
			check: func(t *testing.T) map[string]any {
				n := getNode(t)
				dns := ""
				if n.XrayDNS != nil {
					dns = *n.XrayDNS
				}
				return map[string]any{
					"name": n.Name, "host": n.Host, "decoy_template": n.DecoyTemplate,
					"vless_enabled": deref(n.VLESSEnabled), "hysteria_enabled": deref(n.HysteriaEnabled),
					"reality_enabled": deref(n.RealityEnabled), "xray_dns": dns,
					"warp_enabled": n.WarpEnabled, "opera_enabled": n.OperaEnabled,
					"opera_country": n.OperaCountry, "traffic_coefficient": n.TrafficCoefficient,
					"country": strings.ToLower(n.Country), "sort_weight": float64(n.Weight),
					"capacity": float64(n.Capacity), "hide_when_full": n.HideWhenFull,
					// Routing round-trips as a struct; that it arrived non-nil is the
					// landing test — its contents are model.RoutingConfig's own business.
					"routing": n.Routing != nil,
				}
			},
		},
		"post_nodes_by_id_proxy": {
			body: map[string]any{
				"socks_enabled": true, "socks_port": float64(11080),
				"http_enabled": true, "http_port": float64(13128),
				"accounts": []any{map[string]any{"user": "u", "pass": "p"}},
			},
			check: func(t *testing.T) map[string]any {
				p := getNode(t).Proxy
				return map[string]any{
					"socks_enabled": p.SocksEnabled, "socks_port": float64(p.SocksPort),
					"http_enabled": p.HTTPEnabled, "http_port": float64(p.HTTPPort),
					"accounts": len(p.Accounts) == 1,
				}
			},
		},
		"post_webhooks": {
			body: map[string]any{
				"url": hook.URL, "events": []any{"user.created"}, "enabled": false,
			},
			check: func(t *testing.T) map[string]any {
				hooks, err := st.ListWebhooks()
				if err != nil || len(hooks) == 0 {
					t.Fatalf("list webhooks: %v (%d)", err, len(hooks))
				}
				w := hooks[len(hooks)-1]
				return map[string]any{
					"url": w.URL, "enabled": w.Enabled,
					"events": len(w.Events) == 1 && w.Events[0] == "user.created",
				}
			},
		},
		"post_billing_settings": {
			body: map[string]any{
				"enabled": true, "free_plan_id": float64(0),
				"trial_plan_id": float64(0), "payment_note": "landed",
			},
			check: func(t *testing.T) map[string]any {
				set, err := st.GetSettings()
				if err != nil {
					t.Fatalf("settings: %v", err)
				}
				return map[string]any{
					"enabled": set.BillingEnabled, "free_plan_id": float64(set.BillingFreePlanID),
					"trial_plan_id": float64(set.BillingTrialPlanID), "payment_note": set.BillingPaymentNote,
				}
			},
		},
		"post_billing_plans": {
			body: map[string]any{
				"name": "Landed", "slug": "landed", "price_rub": float64(499),
				"period_days": float64(31), "data_limit": float64(1 << 30),
				"device_limit": float64(5), "speed_limit": float64(2048),
				"sort_order": float64(3), "enabled": true, "group_ids": []any{},
			},
			// id picks create from update; a create is told to leave it out.
			derived: []string{"id"},
			check: func(t *testing.T) map[string]any {
				plans, err := st.ListTariffPlans(true)
				if err != nil || len(plans) == 0 {
					t.Fatalf("list plans: %v (%d)", err, len(plans))
				}
				p := plans[len(plans)-1]
				return map[string]any{
					"name": p.Name, "slug": p.Slug, "price_rub": float64(p.PriceRub),
					"period_days": float64(p.PeriodDays), "data_limit": float64(p.DataLimit),
					"device_limit": float64(p.DeviceLimit), "speed_limit": float64(p.SpeedLimit),
					"sort_order": float64(p.SortOrder), "enabled": p.Enabled,
					"group_ids": len(p.GroupIDs) == 0,
				}
			},
		},
	}

	for tool, probe := range probes {
		t.Run(tool, func(t *testing.T) {
			args := map[string]any{"body": probe.body}
			if strings.Contains(tool, "_by_id") {
				args["id"] = node.ID
			}
			call(tool, args)
			got := probe.check(t)
			for name, want := range probe.body {
				if _, ok := got[name]; !ok {
					continue // asserted structurally (see the bool entries above)
				}
				switch want.(type) {
				case []any, map[string]any:
					if got[name] != true {
						t.Errorf("%s: %s did not land (%v)", tool, name, got[name])
					}
				default:
					if got[name] != want {
						t.Errorf("%s: %s was sent as %v and stored as %v", tool, name, want, got[name])
					}
				}
			}
		})
	}

	// Completeness: every field these tools declare is either sent above or named as
	// one the panel owns.
	for _, tool := range mcp.BuildTools(OpenAPISpec(base), true) {
		probe, ok := probes[tool.Name]
		if !ok {
			continue
		}
		props, _ := tool.InputSchema["properties"].(map[string]any)
		schema, _ := props["body"].(map[string]any)
		declared, _ := schema["properties"].(map[string]any)
		for name := range declared {
			if _, sent := probe.body[name]; sent {
				continue
			}
			if !contains(probe.derived, name) {
				t.Errorf("%s accepts %q and nothing here proves it lands", tool.Name, name)
			}
		}
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// The inbound body is the widest on the API — twenty-five fields, most of them
// meaningful only for one protocol or transport. One probe cannot exercise it: a
// Hysteria2 inbound has no transport to set and a WebSocket one has no hop range,
// and Normalize() deliberately clears what doesn't apply. So the surface is covered
// by one inbound per field group, and the union has to account for every field the
// schema advertises.
func TestMCPInboundWritesReachTheStore(t *testing.T) {
	h, _, st := nodeAPITestServer(t)
	base, key := apiFixture(t, h, st)
	url := base + "/v1/mcp/" + key + "/write"

	call := func(body map[string]any) {
		t.Helper()
		params, err := json.Marshal(map[string]any{
			"name": "post_servers_by_id_inbounds",
			"arguments": map[string]any{
				"id": model.LocalNodeID, "body": body,
			},
		})
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		rec := rpc(t, h, url, fmt.Sprintf(
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":%s}`, params))
		if out := rec.Body.String(); rec.Code != http.StatusOK || strings.Contains(out, `"isError":true`) {
			t.Fatalf("create %v: %d %s", body["name"], rec.Code, out)
		}
	}
	// find returns the stored inbound by name.
	find := func(t *testing.T, name string) model.Inbound {
		t.Helper()
		list, err := st.Inbounds(model.LocalNodeID)
		if err != nil {
			t.Fatalf("list inbounds: %v", err)
		}
		for _, in := range list {
			if in.Name == name {
				return in
			}
		}
		t.Fatalf("inbound %q was created and cannot be found", name)
		return model.Inbound{}
	}

	type group struct {
		body   map[string]any
		assert func(t *testing.T, in model.Inbound)
	}
	groups := []group{
		{ // the common fields, plus what a TLS WebSocket lane carries
			body: map[string]any{
				"enabled": true, "name": "land-ws", "protocol": model.InbVLESS, "port": 21101,
				"transport": model.TrWS, "security": model.SecTLS,
				"sni": "sni.example.com", "fp": "chrome", "path": "/land", "host": "host.example.com",
			},
			assert: func(t *testing.T, in model.Inbound) {
				eq(t, "enabled", true, in.Enabled)
				eq(t, "protocol", model.InbVLESS, in.Protocol)
				eq(t, "port", 21101, in.Port)
				eq(t, "transport", model.TrWS, in.Opts.Transport)
				eq(t, "security", model.SecTLS, in.Opts.Security)
				eq(t, "sni", "sni.example.com", in.Opts.SNI)
				eq(t, "fp", "chrome", in.Opts.FP)
				eq(t, "path", "/land", in.Opts.Path)
				eq(t, "host", "host.example.com", in.Opts.Host)
			},
		},
		{ // REALITY, and the TCP masquerade headers
			body: map[string]any{
				"enabled": true, "name": "land-reality", "protocol": model.InbVLESS, "port": 21102,
				"transport": model.TrTCP, "security": model.SecReality,
				"reality_dest": "www.example.com", "reality_anti_replay": true,
				"header_type": "http", "header_hosts": []any{"a.example"}, "header_paths": []any{"/x"},
			},
			assert: func(t *testing.T, in model.Inbound) {
				eq(t, "security", model.SecReality, in.Opts.Security)
				eq(t, "reality_dest", "www.example.com", in.Opts.RealityDest)
				if in.Opts.RealityMaxTimeDiff == 0 {
					t.Error("reality_anti_replay did not land: maxTimeDiff is still 0")
				}
				eq(t, "header_type", "http", in.Opts.HeaderType)
				eq(t, "header_hosts", 1, len(in.Opts.HeaderHosts))
				eq(t, "header_paths", 1, len(in.Opts.HeaderPaths))
			},
		},
		{ // gRPC's own three
			body: map[string]any{
				"enabled": true, "name": "land-grpc", "protocol": model.InbVLESS, "port": 21103,
				"transport": model.TrGRPC, "security": model.SecTLS,
				"service_name": "svc", "authority": "auth.example", "multi_mode": true,
			},
			assert: func(t *testing.T, in model.Inbound) {
				eq(t, "service_name", "svc", in.Opts.ServiceName)
				eq(t, "authority", "auth.example", in.Opts.Authority)
				eq(t, "multi_mode", true, in.Opts.MultiMode)
			},
		},
		{ // XHTTP's mode and its raw extra
			body: map[string]any{
				"enabled": true, "name": "land-xhttp", "protocol": model.InbVLESS, "port": 21104,
				"transport": model.TrXHTTP, "security": model.SecTLS,
				"mode": "auto", "path": "/x",
				"xhttp_extra": map[string]any{"raw": `{"scMaxEachPostBytes":1000000}`},
			},
			assert: func(t *testing.T, in model.Inbound) {
				eq(t, "mode", "auto", in.Opts.Mode)
				if len(in.Opts.XHTTPExtra) == 0 {
					t.Error("xhttp_extra did not land: the assembled blob is empty")
				}
			},
		},
		{ // Hysteria2's hop range, and the two remaining raw boxes
			body: map[string]any{
				"enabled": true, "name": "land-hy2", "protocol": model.InbHysteria, "port": 21105,
				"hop_start": 40000, "hop_end": 40010, "hop_interval": "5-10",
				"sockopt":   map[string]any{"raw": `{"tcpFastOpen":true}`},
				"tls_extra": map[string]any{"raw": `{"rejectUnknownSni":true}`},
			},
			assert: func(t *testing.T, in model.Inbound) {
				eq(t, "hop_start", 40000, in.Opts.HopStart)
				eq(t, "hop_end", 40010, in.Opts.HopEnd)
				eq(t, "hop_interval", "5-10", in.Opts.HopInterval)
				if len(in.Opts.Sockopt) == 0 {
					t.Error("sockopt did not land: the assembled blob is empty")
				}
				if len(in.Opts.TLSExtra) == 0 {
					t.Error("tls_extra did not land: the assembled blob is empty")
				}
			},
		},
		{ // Shadowsocks-2022's chosen method — the field whose loss on this exact path
			// (inboundReq had no `method`) made the whole protocol unreachable.
			body: map[string]any{
				"enabled": true, "name": "land-ss", "protocol": model.InbShadowsocks, "port": 21106,
				"method": model.SS2022AES256,
			},
			assert: func(t *testing.T, in model.Inbound) {
				eq(t, "method", model.SS2022AES256, in.Opts.Method)
				// The panel-generated server key must have landed too, or the inbound
				// can't authenticate anyone.
				if in.Opts.ShadowKey == "" {
					t.Error("shadow_key did not land: the server key is empty")
				}
			},
		},
	}

	covered := map[string]bool{}
	for _, g := range groups {
		for name := range g.body {
			covered[name] = true
		}
		call(g.body)
		g.assert(t, find(t, g.body["name"].(string)))
	}

	for _, tool := range mcp.BuildTools(OpenAPISpec(base), true) {
		if tool.Name != "post_servers_by_id_inbounds" {
			continue
		}
		props, _ := tool.InputSchema["properties"].(map[string]any)
		schema, _ := props["body"].(map[string]any)
		declared, _ := schema["properties"].(map[string]any)
		for name := range declared {
			if !covered[name] {
				t.Errorf("the inbound body accepts %q and nothing here proves it lands", name)
			}
		}
	}
}

// eq reports a field that came back as something other than what was sent.
func eq(t *testing.T, field string, want, got any) {
	t.Helper()
	if want != got {
		t.Errorf("%s was sent as %v and stored as %v", field, want, got)
	}
}
