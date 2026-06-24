package link

import (
	"net/url"

	"github.com/msTimofeev/rospanel/internal/model"
	"github.com/msTimofeev/rospanel/internal/xray"
)

// VLESS builds a vless:// share link for raw-TCP + TLS + Vision.
//
//	vless://<uuid>@<host>:<port>?encryption=none&security=tls&sni=<sni>
//	      &fp=chrome&type=tcp&flow=xtls-rprx-vision#<label>
func VLESS(u model.User, set *model.Settings) string {
	q := url.Values{}
	q.Set("encryption", "none")
	q.Set("security", "tls")
	q.Set("sni", set.SNI)
	q.Set("fp", set.VLESSFP())
	q.Set("alpn", "h2,http/1.1") // match the server's offered ALPN (Vision is raw after the handshake)
	q.Set("type", "tcp")
	q.Set("flow", xray.VisionFlow)
	pinSelfSigned(q, set)
	return assemble("vless", u.UUID, set.VLESSPort, q, model.ProtoVLESS, u, set)
}
