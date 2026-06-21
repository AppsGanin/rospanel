package link

import (
	"fmt"
	"net/url"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Hysteria2 builds a hysteria2:// share link.
//
// Format matches what x-ui/3x-ui and similar Xray-based panels emit and that
// clients such as v2rayNG / NekoBox (Xray core) accept:
//
//	hysteria2://<pw>@<host>:<port>?type=hysteria&security=tls&sni=<sni>
//	      &alpn=h3&fm=<quicParams>#<label>
//
// fm carries the port-hopping quicParams JSON and is double-URL-encoded because
// Xray decodes query params twice before consuming the value. The JSON is kept
// COMPACT (no spaces/newlines): Go's url.QueryEscape encodes a space as "+",
// which after the second encode becomes "%2B" and decodes back to a literal "+"
// on the client — corrupting the JSON. No whitespace ⇒ no ambiguity.
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
		fm := fmt.Sprintf(
			`{"quicParams":{"udpHop":{"ports":"%d-%d","interval":"%s"},"congestion":"bbr"}}`,
			set.HysteriaPort, set.HopEnd, interval,
		)
		q.Set("fm", url.QueryEscape(fm)) // Encode() escapes once more → double-encoded
	}
	return assemble("hysteria2", url.QueryEscape(u.Password), set.HysteriaPort, q, model.ProtoHysteria, u, set)
}
