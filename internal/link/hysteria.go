package link

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The fm parameter is Xray's finalmask block, carried in the link so an Xray-core
// client reproduces the server's packet shaping. The panel emits two of its parts:
// quicParams (Hysteria2 port-hopping) and one UDP mask (Salamander obfuscation).
//
// The JSON is kept COMPACT (no spaces/newlines) on purpose: fm is double-URL-encoded
// because Xray decodes query params twice before consuming the value, and Go's
// url.QueryEscape turns a space into "+", which after the second encode becomes
// "%2B" and decodes back to a literal "+" on the client — corrupting the JSON. No
// whitespace ⇒ no ambiguity. Marshalling rather than formatting is what keeps that
// promise once a user-supplied value (the obfuscation key) is inside the document.
type fmDoc struct {
	UDP        []fmMask `json:"udp,omitempty"`
	QuicParams *fmQuic  `json:"quicParams,omitempty"`
}

type fmMask struct {
	Type     string `json:"type"`
	Settings any    `json:"settings,omitempty"`
}

type fmSalamander struct {
	Password string `json:"password"`
}

type fmQuic struct {
	UDPHop     *fmUDPHop `json:"udpHop,omitempty"`
	Congestion string    `json:"congestion,omitempty"`
}

type fmUDPHop struct {
	Ports    string `json:"ports"`
	Interval string `json:"interval"`
}

// fmParam renders the fm query value for a lane, or "" when the lane needs none.
// Whether the lane hops is the caller's decision — it is the same question the rest
// of the panel asks (Settings.HopEnd > the lane port, Inbound.UsesHopping), and
// re-deriving it from the ADVERTISED base here would quietly answer it differently.
// An empty obfs key means no obfuscation.
func fmParam(hop bool, base, hopEnd int, interval, obfs string) string {
	var doc fmDoc
	if obfs != "" {
		doc.UDP = []fmMask{{Type: "salamander", Settings: fmSalamander{Password: obfs}}}
	}
	if hop {
		if interval == "" {
			interval = "5-10"
		}
		doc.QuicParams = &fmQuic{
			UDPHop:     &fmUDPHop{Ports: fmt.Sprintf("%d-%d", base, hopEnd), Interval: interval},
			Congestion: "bbr",
		}
	}
	if doc.UDP == nil && doc.QuicParams == nil {
		return ""
	}
	// The document is built entirely from typed fields, so marshalling cannot fail.
	b, err := json.Marshal(doc)
	if err != nil {
		return ""
	}
	return string(b)
}

// setObfs adds the Hysteria2 obfuscation parameters a non-Xray client reads. Xray
// clients take the same key from fm; sing-box, mihomo and the reference hysteria
// client read obfs/obfs-password instead, and the two implementations are wire
// compatible, so emitting both is what makes one link work everywhere.
func setObfs(q url.Values, obfs string) {
	if obfs == "" {
		return
	}
	q.Set("obfs", "salamander")
	q.Set("obfs-password", obfs)
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
	setObfs(q, set.HysteriaObfs)
	if fm := fmParam(set.HopEnd > set.HysteriaPort, model.HopAdvertised(set.HysteriaPort, set.HopStart), set.HopEnd, set.HopInterval, set.HysteriaObfs); fm != "" {
		// Encode() escapes once more → double-encoded, which is what the client wants.
		q.Set("fm", url.QueryEscape(fm))
	}
	return assemble("hysteria2", url.QueryEscape(u.Password), set.HysteriaPort, q, model.ProtoHysteria, u, set)
}
