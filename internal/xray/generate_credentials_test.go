package xray

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// filledUser returns a user with every field set to something other than its zero
// value — reflection rather than a literal, so a field added to model.User later is
// filled too without anyone remembering this test.
func filledUser(id int64) model.User {
	var u model.User
	v := reflect.ValueOf(&u).Elem()
	for i := range v.NumField() {
		f := v.Field(i)
		switch f.Kind() {
		case reflect.String:
			f.SetString("x-" + v.Type().Field(i).Name)
		case reflect.Int, reflect.Int64, reflect.Int32:
			f.SetInt(7)
		case reflect.Bool:
			f.SetBool(true)
		case reflect.Slice:
			if f.Type().Elem().Kind() == reflect.String {
				f.Set(reflect.ValueOf([]string{"x"}))
			}
		case reflect.Struct:
			if f.Type() == reflect.TypeOf(time.Time{}) {
				f.Set(reflect.ValueOf(time.Unix(1_700_000_000, 0)))
			}
		}
	}
	u.ID = id
	u.UUID = fmt.Sprintf("uuid-%d", id)
	u.Password = fmt.Sprintf("pw-%d", id)
	return u
}

// TestGenerateReadsOnlyCredentials is what store.WorkingCredentials rests on: the
// config builders read a user's ID, UUID and Password and nothing else, so a user
// carrying only those produces the same config and the same live-add stubs as one
// with every field set. A builder that starts reading another field fails here rather
// than quietly generating a different config on every server.
func TestGenerateReadsOnlyCredentials(t *testing.T) {
	full := []model.User{filledUser(1), filledUser(2), filledUser(3)}
	creds := make([]model.User, len(full))
	for i, u := range full {
		creds[i] = model.User{ID: u.ID, UUID: u.UUID, Password: u.Password, WGPrivateKey: u.WGPrivateKey}
	}

	set := baseSettings()
	set.RealityEnabled = true
	set.RealityPrivateKey = "priv"
	set.RealityDest = "www.apple.com"
	set.RealityShortID = "aabb"
	set.RealityPath = "/secret"
	custom := []model.Inbound{
		{ID: 5, Enabled: true, Name: "WS", Protocol: model.InbVLESS, Port: 9443,
			Opts: model.InboundOpts{Transport: model.TrWS, Security: model.SecTLS, Path: "/w"}},
		{ID: 6, Enabled: true, Name: "T", Protocol: model.InbTrojan, Port: 9444,
			Opts: model.InboundOpts{Transport: model.TrTCP, Security: model.SecTLS}},
		{ID: 7, Enabled: true, Name: "H", Protocol: model.InbHysteria, Port: 7000},
		{ID: 8, Enabled: true, Name: "SS", Protocol: model.InbShadowsocks, Port: 9500,
			Opts: model.InboundOpts{Method: model.SS2022AES128, ShadowKey: base64.StdEncoding.EncodeToString(make([]byte, 16))}},
	}
	for i := range custom {
		custom[i].Normalize()
	}
	// One user limited to one lane and one inbound, so the access gate is part of
	// what is compared.
	access := map[int64]model.Access{2: {Tokens: map[string]bool{
		model.BuiltinToken(model.LocalNodeID, model.LaneVLESS): true, model.InboundToken(6): true,
	}}}
	opts := Options{PanelDest: "127.0.0.1:8080", Custom: custom, Access: access}

	render := func(users []model.User) string {
		t.Helper()
		cfg, err := Generate(set, users, opts, nil)
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		b, err := json.Marshal(struct {
			Config *Config
			Stubs  []Inbound
		}{cfg, UserInbounds(set, custom, users, model.LocalNodeID, access)})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if a, b := render(full), render(creds); a != b {
		t.Fatalf("a user with only credentials generated a different config:\nfull:  %s\ncreds: %s", a, b)
	}
}
