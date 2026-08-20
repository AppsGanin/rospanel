package link

import (
	"fmt"
	"net/url"

	"github.com/AppsGanin/rospanel/internal/model"
)

// hopParams renders the Hysteria2 port-hopping quicParams JSON carried in a link's
// fm parameter.
//
// The JSON is kept COMPACT (no spaces/newlines) on purpose: fm is double-URL-encoded
// because Xray decodes query params twice before consuming the value, and Go's
// url.QueryEscape turns a space into "+", which after the second encode becomes
// "%2B" and decodes back to a literal "+" on the client — corrupting the JSON. No
// whitespace ⇒ no ambiguity.
func hopParams(base, hopEnd int, interval string) string {
	return fmt.Sprintf(
		`{"quicParams":{"udpHop":{"ports":"%d-%d","interval":"%s"},"congestion":"bbr"}}`,
		base, hopEnd, interval,
	)
}

// Hysteria2 builds a hysteria2:// share link.
//
// Format matches what x-ui/3x-ui and similar Xray-based panels emit and that
// clients such as v2rayNG / NekoBox (Xray core) accept:
//
//	hysteria2://<pw>@<host>:<port>?type=hysteria&security=tls&sni=<sni>
//	      &alpn=h3&fm=<quicParams>#<label>
func Hysteria2(u model.User, set *model.Settings) string {
	q := url.Values{}
	q.Set("type", "hysteria")
	q.Set("security", "tls")
	q.Set("sni", set.SNI)
	q.Set("alpn", "h3")
	pinSelfSigned(q, set)
	if set.HopEnd > set.HysteriaPort {
		interval := set.HopInterval
		if interval == "" {
			interval = "5-10"
		}
		// Encode() escapes once more → double-encoded, which is what the client wants.
		q.Set("fm", url.QueryEscape(hopParams(model.HopAdvertised(set.HysteriaPort, set.HopStart), set.HopEnd, interval)))
	}
	return assemble("hysteria2", url.QueryEscape(u.Password), set.HysteriaPort, q, model.ProtoHysteria, u, set)
}
