package link

import (
	"net/url"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Trojan builds a trojan:// share link for Trojan-over-WebSocket on :443.
//
//	trojan://<password>@<host>:443?security=tls&sni=<sni>&type=ws
//	      &path=<wspath>&host=<sni>&fp=chrome#<label>
func Trojan(u model.User, set *model.Settings) string {
	q := url.Values{}
	q.Set("security", "tls")
	q.Set("sni", set.SNI)
	q.Set("type", "ws")
	q.Set("alpn", "http/1.1")
	q.Set("path", set.WSPath)
	q.Set("host", set.SNI)
	q.Set("fp", set.TrojanFP())
	pinSelfSigned(q, set)
	return assemble("trojan", url.QueryEscape(u.Password), set.VLESSPort, q, model.ProtoTrojan, u, set)
}
