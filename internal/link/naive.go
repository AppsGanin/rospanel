package link

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"strconv"

	"github.com/AppsGanin/rospanel/internal/model"
)

// NaiveOn reports whether a server hands out NaiveProxy: the lane is on and its
// certificate is one a client trusts — the NaiveProxy client cannot skip verification
// or pin a certificate, so a self-signed server has nothing it could connect to.
func NaiveOn(set *model.Settings) bool { return set.NaiveEnabled && !set.TLSInsecure }

// Naive is a user's NaiveProxy link: naive+https://u<id>:<uuid>@<host>:<port>#<label>,
// the form NekoBox and v2rayN import. It points at the VLESS-TCP-TLS port, where the
// decoy behind it answers the tunnel (internal/naive). The host is the certificate's
// name: the client sends it as SNI and checks the certificate against it.
func Naive(u model.User, set *model.Settings) string {
	return fmt.Sprintf("naive+https://%s@%s#%s",
		url.UserPassword(model.UserEmail(u.ID), u.UUID).String(),
		net.JoinHostPort(NaiveServerName(set), strconv.Itoa(set.VLESSPort)),
		url.PathEscape(LabelFor(model.ProtoNaive, u, set)))
}

// NaiveUoT is Naive with uot=1, for a client that reads it (Throne): its UDP then rides
// the tunnel as sing-box UDP over TCP, which the lane serves.
func NaiveUoT(u model.User, set *model.Settings) string {
	return fmt.Sprintf("naive+https://%s@%s?uot=1#%s",
		url.UserPassword(model.UserEmail(u.ID), u.UUID).String(),
		net.JoinHostPort(NaiveServerName(set), strconv.Itoa(set.VLESSPort)),
		url.PathEscape(LabelFor(model.ProtoNaive, u, set)))
}

// NaiveShadowrocket is the same credential in the form Shadowrocket imports for its
// NaiveProxy type (HTTP2): http2://base64(u<id>:<uuid>@host:port)?padding=1&peer=<name>
// #label — the userinfo base64'd unescaped, the certificate's name in peer.
func NaiveShadowrocket(u model.User, set *model.Settings) string {
	auth := fmt.Sprintf("%s:%s@%s", model.UserEmail(u.ID), u.UUID,
		net.JoinHostPort(set.Host, strconv.Itoa(set.VLESSPort)))
	q := url.Values{}
	q.Set("padding", "1")
	q.Set("peer", NaiveServerName(set))
	return fmt.Sprintf("http2://%s?%s#%s", base64.StdEncoding.EncodeToString([]byte(auth)),
		q.Encode(), url.PathEscape(LabelFor(model.ProtoNaive, u, set)))
}

// NaiveServerName is the name the client dials and verifies: the SNI when it is a name,
// the host otherwise.
func NaiveServerName(set *model.Settings) string {
	if set.SNI != "" && net.ParseIP(set.SNI) == nil {
		return set.SNI
	}
	return set.Host
}
