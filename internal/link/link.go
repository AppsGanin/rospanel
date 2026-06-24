// Package link builds client share-links from a user + settings.
package link

import (
	"fmt"
	"net/url"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Label is the node name appended after '#' in share links / used as the
// sing-box/Clash node tag (protocol display name only). The user name, when
// requested, goes into the subscription title instead (see sub.SubTitle).
func Label(proto string) string { return proto }

// assemble joins the share-link shape shared by every protocol:
//
//	<scheme>://<cred>@<host>:<port>?<query>#<label>
//
// cred must already be escaped where the protocol needs it (password links pass
// url.QueryEscape(pw); UUID links pass the raw uuid). host is always set.Host.
func assemble(scheme, cred string, port int, q url.Values, proto string, u model.User, set *model.Settings) string {
	return fmt.Sprintf("%s://%s@%s:%d?%s#%s",
		scheme, cred, set.Host, port, q.Encode(), url.PathEscape(Label(proto)))
}

// pinSelfSigned adds the cert-pin query param (pcs) when the active cert isn't
// CA-trusted. Recent Xray-core removed allowInsecure, so for an untrusted
// (self-signed/IP) cert the client trusts this exact cert via its SHA-256 pin.
// On a real CA cert TLSPinSHA256 is empty and normal verification applies.
func pinSelfSigned(q url.Values, set *model.Settings) {
	if set.TLSPinSHA256 != "" {
		q.Set("pcs", set.TLSPinSHA256)
	}
}
