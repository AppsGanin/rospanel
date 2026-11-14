package nodeagent

import (
	"github.com/AppsGanin/rospanel/internal/xhttpfront"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// syncXHTTP points the VLESS-XHTTP-TLS lane's path at the inbound of the config just
// applied, or takes the route down when the lane is off. public is the TCP-TLS lane's
// port, where its clients' sockets are (0 when this agent runs no front).
func (a *Agent) syncXHTTP(cfg []byte, public int) {
	path, addr, ok := xray.XHTTPRouteRaw(cfg)
	if !ok {
		a.xhttp.SetRoutes(nil)
		a.sup.SetPublicPort(xray.TagXHTTP, 0)
		return
	}
	a.xhttp.SetRoutes([]xhttpfront.Route{{Path: path, Addr: addr}})
	a.sup.SetPublicPort(xray.TagXHTTP, public)
}
