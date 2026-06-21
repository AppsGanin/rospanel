package link

import (
	"net/url"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Reality builds a vless:// share link for VLESS + gRPC + REALITY.
//
//	vless://<uuid>@<host>:<port>?encryption=none&security=reality&type=grpc
//	      &serviceName=<svc>&mode=gun&pbk=<pub>&sid=<sid>&sni=<dest>&fp=<fp>#<label>
func Reality(u model.User, set *model.Settings) string {
	q := url.Values{}
	q.Set("encryption", "none")
	q.Set("security", "reality")
	q.Set("type", "grpc")
	q.Set("serviceName", set.RealityServiceName)
	q.Set("mode", "gun")
	q.Set("pbk", set.RealityPublicKey)
	q.Set("sid", set.RealitySID())
	q.Set("sni", set.RealitySNI())
	q.Set("fp", set.RealityFP())
	q.Set("spx", "/") // spiderX: client crawls the donor after the handshake
	return assemble("vless", u.UUID, set.RealityPort, q, model.ProtoReality, u, set)
}
