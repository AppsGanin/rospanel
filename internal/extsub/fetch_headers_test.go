package extsub

import (
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// A subscription nobody configured still has to answer the device question, because a
// panel with device binding on refuses a caller that does not — ours does, with a 403
// the parser cannot tell apart from an empty subscription.
func TestHeadersFillEveryDefault(t *testing.T) {
	h := Headers("https://panel.example.com/sub/abc", model.ExtIdentity{})
	for _, k := range []string{
		model.HeaderHWID, model.HeaderDeviceOS, model.HeaderOSVersion,
		model.HeaderDeviceModel, "User-Agent",
	} {
		if h[k] == "" {
			t.Errorf("header %s has no default", k)
		}
	}
	// Never a client's user agent by default: the format this package parses is the
	// share-link list, which is what a panel serves when it does not recognise the
	// caller. Claiming to be Clash would get us a document it cannot read.
	ua := strings.ToLower(h["User-Agent"])
	for _, bad := range []string{"clash", "mihomo", "sing-box", "stash", "happ", "v2rayn", "streisand"} {
		if strings.Contains(ua, bad) {
			t.Errorf("the default user agent %q would change the format the upstream serves", ua)
		}
	}
}

// Every field is an override, and a set one is sent verbatim — the point of having
// them is that the operator knows something we do not about what the other side takes.
func TestHeadersOverrideEveryField(t *testing.T) {
	id := model.ExtIdentity{
		HWID: "given-by-them", DeviceOS: "android", OSVersion: "14",
		DeviceModel: "Pixel 8", UserAgent: "Happ/1.2.3",
	}
	h := Headers("https://panel.example.com/sub/abc", id)
	for k, want := range map[string]string{
		model.HeaderHWID:        id.HWID,
		model.HeaderDeviceOS:    id.DeviceOS,
		model.HeaderOSVersion:   id.OSVersion,
		model.HeaderDeviceModel: id.DeviceModel,
		"User-Agent":            id.UserAgent,
	} {
		if h[k] != want {
			t.Errorf("%s = %q, want the operator's %q", k, h[k], want)
		}
	}
	// A partial override keeps the defaults for the rest rather than blanking them.
	partial := Headers("https://panel.example.com/sub/abc", model.ExtIdentity{DeviceModel: "Pixel 8"})
	if partial[model.HeaderDeviceModel] != "Pixel 8" {
		t.Error("the one set field was not used")
	}
	if partial[model.HeaderDeviceOS] != DefaultDeviceOS {
		t.Errorf("an unset field became %q instead of its default", partial[model.HeaderDeviceOS])
	}
	if partial[model.HeaderHWID] != DerivedHWID("https://panel.example.com/sub/abc") {
		t.Error("an unset hwid did not fall back to the derived one")
	}
}

// The derived id has to be the SAME on every sync, or each refresh burns another
// device slot upstream until the panel is locked out of a subscription it pays for.
func TestDerivedHWIDIsStablePerSource(t *testing.T) {
	const a = "https://panel.example.com/sub/abc"
	const b = "https://panel.example.com/sub/xyz"
	first := DerivedHWID(a)
	if first == "" {
		t.Fatal("no id produced")
	}
	if again := DerivedHWID(a); again != first {
		t.Errorf("the same source produced two ids: %q then %q", first, again)
	}
	// Trimming is part of the identity, not a second device.
	if padded := DerivedHWID("  " + a + "  "); padded != first {
		t.Errorf("whitespace changed the id: %q vs %q", padded, first)
	}
	if other := DerivedHWID(b); other == first {
		t.Error("two different subscriptions share one device slot")
	}
	// The upstream's own token is what makes the source unique; it must not be echoed
	// back in a second place.
	if strings.Contains(first, "abc") {
		t.Errorf("the source token leaked into the id: %q", first)
	}
}
