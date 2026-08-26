package link

import (
	"net/url"

	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
)

// Reality builds a vless:// share link for VLESS + XHTTP + REALITY.
//
//	vless://<uuid>@<host>:<port>?encryption=none&security=reality&type=xhttp
//	      &path=<path>&mode=auto&pbk=<pub>&sid=<sid>&sni=<dest>&fp=<fp>#<label>
//
// mode stays "auto" on purpose: with a REALITY config present Xray's client resolves
// auto to stream-one (one HTTP request per connection), which is both the smallest
// surface and what the server side expects. Pinning a different mode here would only
// make the two disagree.
func Reality(u model.User, set *model.Settings) string {
	q := url.Values{}
	q.Set("encryption", "none")
	q.Set("security", "reality")
	q.Set("type", "xhttp")
	q.Set("path", set.RealityPathOr())
	q.Set("mode", "auto")
	q.Set("pbk", set.RealityPublicKey)
	q.Set("sid", set.RealitySID())
	q.Set("sni", set.RealitySNI())
	q.Set("fp", set.RealityFP())
	q.Set("spx", "/") // spiderX: client crawls the donor after the handshake
	return assemble("vless", u.UUID, set.RealityPort, q, model.ProtoReality, u, set)
}
