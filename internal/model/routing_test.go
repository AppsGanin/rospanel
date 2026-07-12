package model

import (
	"encoding/json"
	"strings"
	"testing"
)

// A routing config saved by a pre-lanes build must upgrade in place: the single
// proxy pool becomes one lane whose ID is the same "proxy" its saved routing
// order already points at, so precedence, proxies and rules all survive.
func TestMigrateLanesFromLegacyPool(t *testing.T) {
	saved := `{
		"routing_order": ["proxy", "warp", "direct"],
		"proxy_urls": ["https://example.com/list.txt"],
		"proxy_manual": ["socks5://1.2.3.4:1080"],
		"proxy_domains": ["geosite:google"],
		"proxy_ips": ["geoip:us"]
	}`
	var rc RoutingConfig
	if err := json.Unmarshal([]byte(saved), &rc); err != nil {
		t.Fatal(err)
	}
	rc.MigrateLanes()

	if len(rc.Lanes) != 1 {
		t.Fatalf("got %d lanes, want 1", len(rc.Lanes))
	}
	l := rc.Lanes[0]
	if l.ID != LegacyProxyLaneID {
		t.Errorf("lane ID = %q, want %q (the routing order points at it)", l.ID, LegacyProxyLaneID)
	}
	if !l.Enabled {
		t.Error("migrated lane is disabled; the pool it came from was live")
	}
	if len(l.URLs) != 1 || len(l.Manual) != 1 || len(l.Domains) != 1 || len(l.IPs) != 1 {
		t.Errorf("migrated lane lost data: %+v", l)
	}
	if !ValidLaneID(l.ID) {
		t.Errorf("migrated lane ID %q is not a valid lane ID", l.ID)
	}

	// The deprecated fields must never be written back.
	out, err := json.Marshal(rc)
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"proxy_urls", "proxy_manual", "proxy_domains", "proxy_ips"} {
		if strings.Contains(string(out), gone) {
			t.Errorf("re-marshalled config still carries deprecated %q: %s", gone, out)
		}
	}
}

// An empty (never-configured) pool must not conjure a lane.
func TestMigrateLanesNoLegacyData(t *testing.T) {
	rc := RoutingConfig{BlockAds: true}
	rc.MigrateLanes()
	if len(rc.Lanes) != 0 {
		t.Errorf("got %d lanes from an empty config, want 0", len(rc.Lanes))
	}
}

// Lanes already present win: the deprecated fields are dropped, not merged.
func TestMigrateLanesKeepsExistingLanes(t *testing.T) {
	rc := RoutingConfig{
		Lanes:        []EgressLane{{ID: "ru", Name: "RU", Enabled: true}},
		ProxyManual:  []string{"socks5://1.2.3.4:1080"},
		ProxyDomains: []string{"geosite:google"},
	}
	rc.MigrateLanes()
	if len(rc.Lanes) != 1 || rc.Lanes[0].ID != "ru" {
		t.Fatalf("lanes changed: %+v", rc.Lanes)
	}
	if rc.ProxyManual != nil || rc.ProxyDomains != nil {
		t.Error("deprecated fields survived the migration")
	}
}

func TestValidLaneID(t *testing.T) {
	ok := []string{"ru", "en2", "l1", "proxy", "abcdefghijklmnop"}
	for _, id := range ok {
		if !ValidLaneID(id) {
			t.Errorf("ValidLaneID(%q) = false, want true", id)
		}
	}
	bad := map[string]string{
		"":                  "empty",
		"RU":                "uppercase",
		"ru-2":              "dash would make the balancer selector ambiguous",
		"ru_2":              "underscore",
		"ru zone":           "space",
		"warp":              "reserved built-in lane",
		"opera":             "reserved built-in lane",
		"direct":            "reserved built-in lane",
		"abcdefghijklmnopq": "17 chars, over the limit",
	}
	for id, why := range bad {
		if ValidLaneID(id) {
			t.Errorf("ValidLaneID(%q) = true, want false (%s)", id, why)
		}
	}
}

func TestValidateLanes(t *testing.T) {
	tests := []struct {
		name    string
		lanes   []EgressLane
		wantErr string // substring; empty ⇒ must pass
	}{
		{
			name:  "valid",
			lanes: []EgressLane{{ID: "ru", Name: "RU"}, {ID: "en", Name: "EN"}},
		},
		{
			name:    "duplicate ID",
			lanes:   []EgressLane{{ID: "ru", Name: "RU"}, {ID: "ru", Name: "RU again"}},
			wantErr: "дублирующийся",
		},
		{
			name:    "bad ID",
			lanes:   []EgressLane{{ID: "ru-2", Name: "RU"}},
			wantErr: "недопустимый идентификатор",
		},
		{
			name:    "blank name",
			lanes:   []EgressLane{{ID: "ru", Name: "  "}},
			wantErr: "не задано название",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := RoutingConfig{Lanes: tt.lanes}
			err := rc.ValidateLanes()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("got error %v, want none", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got error %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateLanesTooMany(t *testing.T) {
	rc := RoutingConfig{}
	for i := 0; i <= MaxEgressLanes; i++ {
		rc.Lanes = append(rc.Lanes, EgressLane{ID: "l" + string(rune('a'+i)), Name: "lane"})
	}
	if err := rc.ValidateLanes(); err == nil {
		t.Fatalf("accepted %d lanes, want a limit of %d", len(rc.Lanes), MaxEgressLanes)
	}
}
