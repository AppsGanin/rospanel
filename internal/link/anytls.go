package link

import (
	"fmt"
	"net"
	"net/url"
	"strconv"

	"github.com/AppsGanin/rospanel/internal/model"
)

// AnyTLSOn reports whether a server hands out AnyTLS: the lane is on and its certificate
// is one a client trusts — an AnyTLS link has no way to pin one.
func AnyTLSOn(set *model.Settings) bool { return set.AnyTLSEnabled && !set.TLSInsecure }

// AnyTLS is a user's AnyTLS link as anytls-go's URI scheme writes it:
// anytls://<uuid>@<host>:<port>?sni=<name>#<label> — the TCP-TLS port, where the decoy
// behind Xray's fallback answers it (internal/anytls), and the server's own TLS name.
func AnyTLS(u model.User, set *model.Settings) string {
	q := url.Values{}
	q.Set("sni", set.SNI)
	return fmt.Sprintf("anytls://%s@%s?%s#%s",
		url.User(u.UUID).String(), net.JoinHostPort(set.Host, strconv.Itoa(set.VLESSPort)),
		q.Encode(), url.PathEscape(LabelFor(model.ProtoAnyTLS, u, set)))
}
