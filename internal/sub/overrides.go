package sub

import (
	"slices"

	"github.com/AppsGanin/rospanel/internal/link"
	"github.com/AppsGanin/rospanel/internal/model"
)

// Overrides are a matched response rule's protocol overrides (model.SubRule.Protocols):
// "off" leaves a protocol out of the subscription, "on" puts it in for a client that is
// not known to read it — the operator knows their users' apps better than a
// User-Agent table. nil changes nothing.
type Overrides map[string]string

func (o Overrides) on(k string) bool { return o[k] == model.SubProtoOn }

// Apply returns the servers with the protocols o turns off hidden, in every format: a
// lane or the custom inbounds through Server.Hidden, VLESS Encryption by dropping the
// custom inbounds that have it. The input is left as it is.
func (o Overrides) Apply(servers []Server) []Server {
	var hidden map[string]bool
	for k, v := range o {
		if v == model.SubProtoOff && k != model.SubProtoVLESSEnc {
			if hidden == nil {
				hidden = map[string]bool{}
			}
			hidden[k] = true
		}
	}
	noEnc := o[model.SubProtoVLESSEnc] == model.SubProtoOff
	if hidden == nil && !noEnc {
		return servers
	}
	out := make([]Server, len(servers))
	for i, s := range servers {
		s.Hidden = hidden
		if noEnc {
			s.Custom = slices.DeleteFunc(slices.Clone(s.Custom), func(in model.Inbound) bool { return in.Opts.EncMode != "" })
		}
		out[i] = s
	}
	return out
}

// links adjusts the link forms a client is handed.
func (o Overrides) links(f linkForms) linkForms {
	if o.on(model.LaneNaive) && f.naive == nil {
		f.naive = link.Naive
	}
	if o.on(model.LaneAWG) && f.awg == nil {
		f.awg = AWGIncyLink
	}
	f.mieru = f.mieru || o.on(model.LaneMieru)
	f.anytls = f.anytls || o.on(model.LaneAnyTLS)
	f.vlessEnc = o.enc(f.vlessEnc)
	return f
}

// clash adjusts the lanes a Clash profile carries (it has no NaiveProxy).
func (o Overrides) clash(c clashCaps) clashCaps {
	c.awg = c.awg || o.on(model.LaneAWG)
	c.mieru = c.mieru || o.on(model.LaneMieru)
	c.anytls = c.anytls || o.on(model.LaneAnyTLS)
	c.vlessEnc = o.enc(c.vlessEnc)
	return c
}

// SingBox adjusts the lanes a sing-box profile carries (it has no mieru, AmneziaWG 3.1
// or VLESS Encryption).
func (o Overrides) SingBox(c SingBoxCaps) SingBoxCaps {
	c.Naive = c.Naive || o.on(model.LaneNaive)
	c.AnyTLS = c.AnyTLS || o.on(model.LaneAnyTLS)
	return c
}

// enc is VLESS Encryption for the client: an override either way, else what it reads.
func (o Overrides) enc(auto bool) bool {
	switch o[model.SubProtoVLESSEnc] {
	case model.SubProtoOn:
		return true
	case model.SubProtoOff:
		return false
	}
	return auto
}

// Protocols says which protocols a subscription in format carries for the client that
// sends userAgent, with a rule's overrides: the same decisions the renderers make, for
// the rule editor's preview. A format that has no form for a protocol never carries it,
// whatever a rule says. Built-in lanes and custom inbounds are subject to the user's
// access and to the server having them switched on, which this does not know.
func Protocols(format, userAgent string, o Overrides) map[string]bool {
	p := map[string]bool{
		model.LaneVLESS: true, model.LaneReality: true, model.LaneHysteria: true, model.SubProtoCustom: true,
	}
	switch format {
	case model.SubActionClash:
		c := o.clash(clashCapsFor(userAgent))
		p[model.LaneAnyTLS], p[model.LaneMieru], p[model.LaneAWG], p[model.SubProtoVLESSEnc] = c.anytls, c.mieru, c.awg, c.vlessEnc
	case model.SubActionSingbox:
		c := o.SingBox(SingBoxCapsFor(userAgent))
		p[model.LaneNaive], p[model.LaneAnyTLS] = c.Naive, c.AnyTLS
	case model.SubActionXrayJSON:
		p[model.SubProtoVLESSEnc] = o.enc(VLESSEncLinkFor(userAgent) || xrayCoreHasVLESSEnc(userAgent))
	default:
		f := o.links(linkForms{naive: NaiveLinkFor(userAgent), mieru: MieruLinkFor(userAgent), awg: awgLinkFor(userAgent),
			anytls: AnyTLSLinkFor(userAgent), vlessEnc: VLESSEncLinkFor(userAgent)})
		p[model.LaneNaive], p[model.LaneAWG] = f.naive != nil, f.awg != nil
		p[model.LaneMieru], p[model.LaneAnyTLS], p[model.SubProtoVLESSEnc] = f.mieru, f.anytls, f.vlessEnc
	}
	for k, v := range o {
		if v == model.SubProtoOff {
			p[k] = false
		}
	}
	if !p[model.SubProtoCustom] {
		p[model.SubProtoVLESSEnc] = false
	}
	out := make(map[string]bool, len(model.SubProtoKeys))
	for _, k := range model.SubProtoKeys {
		out[k] = p[k]
	}
	return out
}
