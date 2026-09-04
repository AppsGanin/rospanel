package model

import (
	"strings"
	"testing"
	"time"
)

func tmplUser() *User {
	return &User{
		Name: "petya", DataLimit: 100 << 30, UsedUp: 20 << 30, UsedDown: 5 << 30,
		ExpireAt: time.Date(2030, 3, 4, 12, 0, 0, 0, time.UTC).Unix(),
	}
}

// A name with no variables is the name. Every install that existed before this
// feature is in this case, and its output has to be byte-identical.
func TestRenderNameLeavesPlainNamesAlone(t *testing.T) {
	for _, name := range []string{"VLESS-TCP-TLS", "🇳🇱 Amsterdam", "Node (backup)", ""} {
		if got := RenderName(name, NameVars{Server: "NL", Country: "nl", User: tmplUser()}); got != name {
			t.Errorf("RenderName(%q) = %q, want it untouched", name, got)
		}
	}
}

func TestRenderNameExpandsEveryVariable(t *testing.T) {
	v := NameVars{Server: "Amsterdam", Country: "nl", User: tmplUser(), Loc: time.UTC}
	cases := map[string]string{
		NameVarFlag:    "🇳🇱",
		NameVarCountry: "NL",
		NameVarServer:  "Amsterdam",
		NameVarUser:    "petya",
		NameVarUsed:    "25 GB",
		NameVarLeft:    "75 GB",
		NameVarTotal:   "100 GB",
		NameVarExpire:  "2030-03-04",
	}
	for tmpl, want := range cases {
		if got := RenderName(tmpl, v); got != want {
			t.Errorf("RenderName(%q) = %q, want %q", tmpl, got, want)
		}
	}
	// {days} moves with the clock, so it is checked for shape rather than value.
	if got := RenderName(NameVarDays, v); got == "" || got == NameUnknown || strings.ContainsAny(got, "{}") {
		t.Errorf("RenderName({days}) = %q, want a number", got)
	}
}

// An account with no quota and no expiry has no numbers to show. It must read as
// unlimited rather than as zero — "0" next to a lane name says "you are cut off".
func TestRenderNameUnlimitedReadsAsInfinity(t *testing.T) {
	v := NameVars{User: &User{Name: "u"}, Loc: time.UTC}
	for _, tmpl := range []string{NameVarLeft, NameVarTotal, NameVarExpire, NameVarDays} {
		if got := RenderName(tmpl, v); got != NameInfinite {
			t.Errorf("RenderName(%q) on an unlimited account = %q, want %s", tmpl, got, NameInfinite)
		}
	}
}

// A user over their quota has nothing left, not a negative amount; an expired
// account has zero days, not a negative count.
func TestRenderNameClampsPastTheLimit(t *testing.T) {
	u := &User{DataLimit: 10 << 30, UsedUp: 12 << 30, ExpireAt: 1}
	v := NameVars{User: u, Loc: time.UTC}
	if got := RenderName(NameVarLeft, v); got != "0" {
		t.Errorf("{left} over quota = %q, want 0", got)
	}
	if got := RenderName(NameVarDays, v); got != "0" {
		t.Errorf("{days} after expiry = %q, want 0", got)
	}
}

// The catalogues that list lanes as things to tick have no user. Those variables
// have to say so rather than render as empty, or the operator sees a name they did
// not write and cannot match to the one they did.
func TestRenderNameWithoutAUser(t *testing.T) {
	got := RenderName("{flag} VLESS ({left})", NameVars{Country: "de"})
	if got != "🇩🇪 VLESS (—)" {
		t.Errorf("no-user render = %q", got)
	}
}

// An unknown variable is the operator's own text and stays exactly as typed: every
// surface escapes it, so it renders as a curiosity rather than as a break.
func TestRenderNameKeepsUnknownVariables(t *testing.T) {
	got := RenderName("{flag} {nope}", NameVars{Country: "fr"})
	if got != "🇫🇷 {nope}" {
		t.Errorf("unknown variable render = %q", got)
	}
}

// An unset country leaves both its variables unknown, and the collapse keeps the
// result from carrying the gap where the flag would have been.
func TestRenderNameUnknownCountry(t *testing.T) {
	if got := RenderName("{flag} Node", NameVars{}); got != "— Node" {
		t.Errorf("unknown country render = %q", got)
	}
}

// The server prefix is the settings' job, and a name that places {server} itself
// must not be prefixed on top of its own answer.
func TestProtoLabelServerPrefix(t *testing.T) {
	base := func(name string) *Settings {
		return &Settings{VLESSName: name, NodeLabel: "Amsterdam", ServerPlacement: Placement{Country: "NL"}}
	}
	if got := base("VLESS").ProtoLabelFor(ProtoVLESS, nil); got != "Amsterdam · VLESS" {
		t.Errorf("plain name = %q, want the automatic prefix", got)
	}
	if got := base("{flag} {server}").ProtoLabelFor(ProtoVLESS, nil); got != "🇳🇱 Amsterdam" {
		t.Errorf("{server} name = %q, want no second prefix", got)
	}
	// A name with other variables but no {server} still gets the prefix.
	if got := base("VLESS {country}").ProtoLabelFor(ProtoVLESS, nil); got != "Amsterdam · VLESS NL" {
		t.Errorf("prefixed templated name = %q", got)
	}
}

// The default (no custom name) is untouched by any of this.
func TestProtoLabelDefaultUnchanged(t *testing.T) {
	s := &Settings{}
	if got := s.ProtoLabelFor(ProtoHysteria, tmplUser()); got != ProtoHysteria {
		t.Errorf("default label = %q, want %q", got, ProtoHysteria)
	}
}
