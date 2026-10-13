package sub

import (
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The preview says what each format can carry: a rule gives a protocol only where the
// format has a form for it, and hides any.
func TestProtocolsPreview(t *testing.T) {
	t.Parallel()
	on, off := model.SubProtoOn, model.SubProtoOff
	cases := []struct {
		name, format, ua string
		o                Overrides
		key              string
		want             bool
	}{
		{"happ has no mieru", model.SubActionV2ray, "Happ/3.8.1", nil, model.LaneMieru, false},
		{"rule gives happ mieru", model.SubActionV2ray, "Happ/3.8.1", Overrides{model.LaneMieru: on}, model.LaneMieru, true},
		{"rule hides vless", model.SubActionV2ray, "Happ/3.8.1", Overrides{model.LaneVLESS: off}, model.LaneVLESS, false},
		{"clash has no naive", model.SubActionClash, "clash.meta/v1.19.32", Overrides{model.LaneNaive: on}, model.LaneNaive, false},
		{"sing-box has no mieru", model.SubActionSingbox, "SFA (sing-box 1.13.0; language en_US)", Overrides{model.LaneMieru: on}, model.LaneMieru, false},
		{"rule gives old sing-box naive", model.SubActionSingbox, "SFA (sing-box 1.11.0; language en_US)", Overrides{model.LaneNaive: on}, model.LaneNaive, true},
		{"enc hidden with custom", model.SubActionV2ray, "v2rayN/7.15.0", Overrides{model.SubProtoCustom: off}, model.SubProtoVLESSEnc, false},
		{"xray json enc for streisand", model.SubActionXrayJSON, "Streisand/1.6.76", nil, model.SubProtoVLESSEnc, true},
	}
	for _, c := range cases {
		if got := Protocols(c.format, c.ua, c.o)[c.key]; got != c.want {
			t.Errorf("%s: %s = %v, want %v", c.name, c.key, got, c.want)
		}
	}
}

// Apply hides lanes and the custom inbounds, and drops the encrypted ones alone.
func TestOverridesApply(t *testing.T) {
	t.Parallel()
	enc := model.Inbound{ID: 1, Opts: model.InboundOpts{EncMode: model.EncNative}}
	plain := model.Inbound{ID: 2}
	in := []Server{{Set: &model.Settings{}, Custom: []model.Inbound{enc, plain}, Access: model.UnrestrictedAccess()}}
	out := Overrides{model.SubProtoVLESSEnc: model.SubProtoOff, model.LaneHysteria: model.SubProtoOff}.Apply(in)
	if len(out[0].Custom) != 1 || out[0].Custom[0].ID != 2 {
		t.Errorf("custom after hiding VLESS Encryption: %+v", out[0].Custom)
	}
	if len(in[0].Custom) != 2 {
		t.Error("Apply changed its input")
	}
	if out[0].allowsBuiltin(model.LaneHysteria) || !out[0].allowsBuiltin(model.LaneVLESS) {
		t.Error("hiding Hysteria2 hid the wrong lanes")
	}
}
