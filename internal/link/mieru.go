package link

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
)

// mieruMTU is the MTU a mieru client uses over UDP; the server's own (internal/mieru).
const mieruMTU = 1400

// MieruOn reports whether a server hands out mieru: the lane is on and has a port.
func MieruOn(set *model.Settings) bool { return set.MieruEnabled && set.MieruPort > 0 }

// Mieru is a user's mieru link, mieru's own "simple" form:
// mierus://u<id>:<uuid>@<host>:<port>?mtu=…&multiplexing=…&port=<port>&profile=<label>&protocol=TCP|UDP#<label>
// — port and protocol paired, profile required (enfein/mieru docs/client-install.md).
// The port is in the authority too: mieru's parser and those modelled on it read only
// the host there, and husi reads the port nowhere else.
func Mieru(u model.User, set *model.Settings) string {
	label := LabelFor(model.ProtoMieru, u, set)
	q := url.Values{}
	q.Set("profile", label)
	q.Set("mtu", strconv.Itoa(mieruMTU))
	q.Set("multiplexing", "MULTIPLEXING_LOW")
	q.Set("port", strconv.Itoa(set.MieruPort))
	q.Set("protocol", model.MieruTransportOr(set.MieruTransport))
	// Spaces as %20, not "+": Shadowrocket names the entry from profile and takes "+"
	// literally ("NL · MIERU" came out as "+NL+·+MIERU"); mieru's parser reads either.
	query := strings.ReplaceAll(q.Encode(), "+", "%20")
	return fmt.Sprintf("mierus://%s@%s?%s#%s",
		url.UserPassword(model.UserEmail(u.ID), u.UUID).String(),
		net.JoinHostPort(set.Host, strconv.Itoa(set.MieruPort)), query, url.PathEscape(label))
}
