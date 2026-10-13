package extsub

import "testing"

// A VLESS Encryption value Xray would refuse keeps the link out instead of breaking
// the config it lands in.
func TestXrayOutboundVLESSEncryption(t *testing.T) {
	t.Parallel()
	base := "vless://11111111-2222-3333-4444-555555555555@h.example:443?type=tcp&security=tls"
	for enc, want := range map[string]bool{
		"":                                       true,
		"none":                                   true,
		"mlkem768x25519plus.native.0rtt.AbC-d_e": true,
		"mlkem768x25519plus.xorpub.1rtt.100-111-1111.75-0-111.KEY": true,
		"auto":                                false,
		"mlkem768x25519plus.native.600s.priv": false,
		"mlkem768x25519plus.bogus.0rtt.key":   false,
	} {
		l := base
		if enc != "" {
			l += "&encryption=" + enc
		}
		if _, ok := XrayOutbound(l, "proxy"); ok != want {
			t.Errorf("encryption=%q: taken %v, want %v", enc, ok, want)
		}
	}
}
