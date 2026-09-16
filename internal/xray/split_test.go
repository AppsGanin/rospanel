package xray

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
)

// splitFixture is a server with every built-in lane, every custom protocol, one
// Shadowsocks inbound nobody may use, and system proxies that carry no panel users.
func splitFixture() (*model.Settings, []model.Inbound) {
	set := baseSettings()
	set.RealityEnabled, set.RealityPrivateKey, set.RealityDest, set.RealityShortID, set.RealityPath =
		true, "priv", "www.apple.com", "aa", "/s"
	set.ProxySocksEnabled, set.ProxySocksPort = true, 1080
	set.ProxyAccounts = []model.SystemProxyAccount{{User: "u9", Pass: "not-a-panel-user"}}
	var custom []model.Inbound
	for i, protocol := range model.InboundProtocols {
		in := model.Inbound{ID: int64(10 + i), Enabled: true, Name: protocol, Protocol: protocol, Port: 9000 + i}
		switch protocol {
		case model.InbVLESS:
			in.Opts = model.InboundOpts{Transport: model.TrTCP, Security: model.SecTLS}
		case model.InbTrojan:
			in.Opts = model.InboundOpts{Transport: model.TrWS, Security: model.SecTLS, Path: "/p"}
		case model.InbShadowsocks:
			in.Opts = model.InboundOpts{Method: model.SS2022AES128, ShadowKey: "AAAAAAAAAAAAAAAAAAAAAA=="}
		}
		in.Normalize()
		custom = append(custom, in)
	}
	locked := model.Inbound{ID: 30, Enabled: true, Name: "locked", Protocol: model.InbShadowsocks, Port: 9100,
		Opts: model.InboundOpts{Method: model.SS2022AES128, ShadowKey: "BBBBBBBBBBBBBBBBBBBBBB=="}}
	locked.Normalize()
	return set, append(custom, locked)
}

// splitUsers makes n users and an access map in which about half are narrowed to a
// random selection of what the fixture offers — never inbound 30.
func splitUsers(n int, seed uint64) ([]model.User, map[int64]model.Access) {
	r := rand.New(rand.NewPCG(seed, 1))
	tokens := []string{
		model.BuiltinToken(model.LocalNodeID, model.LaneVLESS),
		model.BuiltinToken(model.LocalNodeID, model.LaneReality),
		model.BuiltinToken(model.LocalNodeID, model.LaneHysteria),
		model.InboundToken(10), model.InboundToken(11), model.InboundToken(12), model.InboundToken(13),
	}
	users := make([]model.User, 0, n)
	access := map[int64]model.Access{}
	for i := range n {
		id := int64(i*3 + 1) // gaps, as real ids have
		users = append(users, model.User{ID: id, UUID: fmt.Sprintf("00000000-0000-4000-8000-%012d", id), Password: fmt.Sprintf("pw-%d", id)})
		if r.IntN(2) == 0 {
			a := model.Access{Tokens: map[string]bool{}}
			for _, t := range tokens {
				if r.IntN(3) == 0 {
					a.Tokens[t] = true
				}
			}
			access[id] = a
		}
	}
	// Nobody is given inbound 30, so it keeps its locked entry: the users left
	// unrestricted above get every token but that one.
	for _, u := range users {
		if _, narrowed := access[u.ID]; !narrowed {
			a := model.Access{Tokens: map[string]bool{}}
			for _, t := range tokens {
				a.Tokens[t] = true
			}
			access[u.ID] = a
		}
	}
	return users, access
}

// rowsOf builds the rows a split config places, ascending by id.
func rowsOf(sc *SplitConfig, users []model.User) []nodeapi.UserRow {
	var rows []nodeapi.UserRow
	for _, u := range users {
		slots, ok := sc.Placed[u.ID]
		if !ok {
			continue
		}
		row := nodeapi.UserRow{ID: u.ID, Slots: slots}
		for _, si := range slots {
			uuid, pw := SlotNeeds(sc.Slots[si])
			if uuid {
				row.UUID = u.UUID
			}
			if pw {
				row.Password = u.Password
			}
		}
		rows = append(rows, row)
	}
	slices.SortFunc(rows, func(a, b nodeapi.UserRow) int { return int(a.ID - b.ID) })
	return rows
}

// decoded reads JSON the way both sides compare it: numbers kept exact, key order ignored.
func decoded(t *testing.T, b []byte) any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// What a node renders from the skeleton and its rows is the config the generator built
// — for every lane, every custom protocol, access groups placing users unevenly, an
// inbound nobody may use, and no users at all.
func TestRenderedUsersAreTheGeneratedConfig(t *testing.T) {
	set, custom := splitFixture()
	for _, c := range []struct {
		n            int
		unrestricted bool // nobody in a group: everyone on every inbound, the locked one too
	}{{0, false}, {1, false}, {7, false}, {300, false}, {5, true}} {
		n := c.n
		users, access := splitUsers(n, uint64(n))
		if c.unrestricted {
			access = nil
		}
		cfg, err := Generate(set, users, Options{PanelDest: "127.0.0.1:8080", ServerID: model.LocalNodeID, Custom: custom, Access: access}, nil)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := json.Marshal(cfg)
		sc, ok := SplitUsers(cfg, custom, users)
		if !ok {
			t.Fatalf("%d users: a generated config could not be split", n)
		}
		skel, _ := json.Marshal(sc.Skeleton)
		got, err := RenderUsers(skel, sc.Slots, rowsOf(sc, users))
		if err != nil {
			t.Fatalf("%d users: render: %v", n, err)
		}
		if !reflect.DeepEqual(decoded(t, got), decoded(t, want)) {
			t.Errorf("%d users: the rendered config differs from the generated one\n got %.2000s\nwant %.2000s", n, got, want)
		}
		if len(sc.Slots) != 7+1 {
			t.Errorf("%d users: %d slots, want 3 lanes + 5 custom", n, len(sc.Slots))
		}
		// The split is not vacuous: the skeleton carries no user's credential.
		for _, u := range users {
			if bytes.Contains(skel, []byte(u.Password)) || bytes.Contains(skel, []byte(u.UUID)) {
				t.Fatalf("%d users: the skeleton still holds user %d", n, u.ID)
			}
		}
	}
}

// The skeleton is the same whoever is in the config. A change is sent as the rows of
// the users it touched, built from a config generated for those users alone, and that
// is only right if their placement does not depend on anyone else.
func TestSkeletonDoesNotDependOnTheUsers(t *testing.T) {
	set, custom := splitFixture()
	users, access := splitUsers(200, 42)
	opts := Options{PanelDest: "127.0.0.1:8080", ServerID: model.LocalNodeID, Custom: custom, Access: access}
	full, _ := Generate(set, users, opts, nil)
	fullSplit, ok := SplitUsers(full, custom, users)
	if !ok {
		t.Fatal("full config not split")
	}
	fullSkel, _ := json.Marshal(fullSplit.Skeleton)
	for _, subset := range [][]model.User{nil, users[:1], users[50:51], users[10:90], {users[3], users[150]}} {
		cfg, _ := Generate(set, subset, opts, nil)
		sc, ok := SplitUsers(cfg, custom, subset)
		if !ok {
			t.Fatal("subset config not split")
		}
		skel, _ := json.Marshal(sc.Skeleton)
		if !bytes.Equal(skel, fullSkel) || !reflect.DeepEqual(sc.Slots, fullSplit.Slots) {
			t.Fatalf("a config for %d users has a different skeleton", len(subset))
		}
		for _, u := range subset {
			if !slices.Equal(sc.Placed[u.ID], fullSplit.Placed[u.ID]) {
				t.Errorf("user %d is placed in %v alone and in %v among everyone", u.ID, sc.Placed[u.ID], fullSplit.Placed[u.ID])
			}
		}
	}
}

// A user list SplitUsers cannot account for is not split: the config goes whole.
func TestSplitRefusesWhatItCannotRender(t *testing.T) {
	set, custom := splitFixture()
	users, access := splitUsers(5, 7)
	opts := Options{PanelDest: "127.0.0.1:8080", ServerID: model.LocalNodeID, Custom: custom, Access: access}
	fresh := func() *Config {
		cfg, err := Generate(set, users, opts, nil)
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	vless := func(cfg *Config) *VLESSInboundSettings {
		for i := range cfg.Inbounds {
			if cfg.Inbounds[i].Tag == TagVLESS {
				s := cfg.Inbounds[i].Settings.(VLESSInboundSettings)
				if len(s.Clients) == 0 {
					t.Fatal("the fixture has nobody on the VLESS lane")
				}
				s.Clients = slices.Clone(s.Clients)
				cfg.Inbounds[i].Settings = s
				return &s
			}
		}
		t.Fatal("no VLESS lane")
		return nil
	}
	put := func(cfg *Config, s *VLESSInboundSettings) {
		for i := range cfg.Inbounds {
			if cfg.Inbounds[i].Tag == TagVLESS {
				cfg.Inbounds[i].Settings = *s
			}
		}
	}
	if _, ok := SplitUsers(fresh(), custom, users); !ok {
		t.Fatal("the untouched config was refused")
	}
	for name, spoil := range map[string]func(*Config){
		"an entry with another flow": func(cfg *Config) {
			s := vless(cfg)
			s.Clients[0].Flow = ""
			put(cfg, s)
		},
		"an entry for a user not given": func(cfg *Config) {
			s := vless(cfg)
			s.Clients = append(s.Clients, VLESSClient{ID: "x", Flow: VisionFlow, Email: "u99999"})
			put(cfg, s)
		},
		"an entry that is not a user": func(cfg *Config) {
			s := vless(cfg)
			s.Clients = append(s.Clients, VLESSClient{ID: "x", Flow: VisionFlow, Email: "admin"})
			put(cfg, s)
		},
		"a user listed twice": func(cfg *Config) {
			s := vless(cfg)
			s.Clients = append(s.Clients, s.Clients[0])
			put(cfg, s)
		},
		"a custom inbound it was not told about": func(cfg *Config) {
			cfg.Inbounds = append(cfg.Inbounds, Inbound{Tag: "custom-77", Protocol: "trojan", Settings: TrojanInboundSettings{}})
		},
		"settings of a type it does not know": func(cfg *Config) {
			s := vless(cfg)
			for i := range cfg.Inbounds {
				if cfg.Inbounds[i].Tag == TagVLESS {
					cfg.Inbounds[i].Settings = s
				}
			}
		},
	} {
		cfg := fresh()
		spoil(cfg)
		if _, ok := SplitUsers(cfg, custom, users); ok {
			t.Errorf("%s was split", name)
		}
	}
}

// Rows RenderUsers cannot place in order are an error, not a quietly different config.
func TestRenderRefusesMalformedRows(t *testing.T) {
	set, custom := splitFixture()
	users, access := splitUsers(3, 3)
	cfg, _ := Generate(set, users, Options{PanelDest: "127.0.0.1:8080", ServerID: model.LocalNodeID, Custom: custom, Access: access}, nil)
	sc, _ := SplitUsers(cfg, custom, users)
	skel, _ := json.Marshal(sc.Skeleton)
	rows := rowsOf(sc, users)
	if len(rows) < 2 {
		t.Fatal("the fixture places fewer than two users")
	}
	swapped := slices.Clone(rows)
	swapped[0], swapped[1] = swapped[1], swapped[0]
	if _, err := RenderUsers(skel, sc.Slots, swapped); err == nil {
		t.Error("rows out of order were rendered")
	}
	bad := slices.Clone(rows)
	bad[0].Slots = []int{len(sc.Slots)}
	if _, err := RenderUsers(skel, sc.Slots, bad); err == nil {
		t.Error("a row naming a slot that does not exist was rendered")
	}
	wrongKind := slices.Clone(sc.Slots)
	wrongKind[0].Kind = "trojan"
	if _, err := RenderUsers(skel, wrongKind, rows); err == nil {
		t.Error("a slot on an inbound of another protocol was rendered")
	}
	noLock := slices.Clone(sc.Slots)
	for i := range noLock {
		noLock[i].Locked = nil
	}
	if _, err := RenderUsers(skel, noLock, nil); err == nil {
		t.Error("an empty Shadowsocks list with no locked entry was rendered")
	}
}
