package logbuf

import (
	"strings"
	"sync"
	"sync/atomic"
)

// Server is the master's server log: Xray, the helpers that run beside it
// (opera-proxy, WARP, AmneziaWG…) and what the panel does to that host. The panel's
// own log (Default) keeps only the panel's business.
var Server = New()

// split turns the panel/server split on. Only the master runs it: a node process is
// nothing but a server, so everything it logs is the server's and stays in Default,
// where the node's log tail reads it from.
var split atomic.Bool

// SplitServer turns the split on for this process.
func SplitServer() { split.Store(true) }

// Scope is the log a panel line belongs to.
type Scope int

const (
	ScopePanel  Scope = iota // the panel itself
	ScopeServer              // the master's host and its Xray
	ScopeNode                // one remote node, told apart by its id
)

// serverPrefixes are the components that run on, or change, the host itself.
var serverPrefixes = []string{
	"xray:", "xray watchdog:", "auto-restart failed:", "reconcile:", "reconcile ",
	"initial reconcile failed", "user sync", "routing:", "reality:", "REALITY:",
	"tls:", "tls reload:", "TLS not ready yet:", "tlsfront:", "http80:", "decoy:",
	"opera:", "warp:", "awg:", "awg poll:", "wireguard:", "turn ", "turn relay:",
	"mieru:", "mtproto:", "anytls:", "cascade:", "proxypool:", "geo:", "iplist:",
	"hop:", "port-hopping setup failed", "connguard", "conntrack:", "hostfw:",
	"ipblock:", "brute-guard:", "shaper:", "bbr:", "memory:", "systemd:",
}

// Route picks the log for one line. server is the line's "node" or "server"
// attribute and hasServer whether it had one: a line naming a server belongs to
// that server's log — the master's (0) or one node's. A line naming none goes by
// its component prefix.
func Route(msg string, server int64, hasServer bool) Scope {
	if !split.Load() {
		return ScopePanel
	}
	if hasServer {
		if server > 0 {
			return ScopeNode
		}
		return ScopeServer
	}
	msg = strings.TrimPrefix(msg, "rospanel: ") // the standard logger's prefix
	for _, tag := range []string{"[INFO] ", "[WARN] ", "[ERROR] "} {
		msg = strings.TrimPrefix(msg, tag)
	}
	for _, p := range serverPrefixes {
		if strings.HasPrefix(msg, p) {
			return ScopeServer
		}
	}
	return ScopePanel
}

var (
	nodeMu   sync.Mutex
	nodeLogs = map[int64]*Hub{}
)

// Node is the panel's own log about one remote node: what it pushed there, when the
// node went silent. It is shown under that node's log next to what the node sent.
func Node(id int64) *Hub {
	nodeMu.Lock()
	defer nodeMu.Unlock()
	h := nodeLogs[id]
	if h == nil {
		h = New()
		nodeLogs[id] = h
	}
	return h
}

// ForgetNode drops a deleted node's log.
func ForgetNode(id int64) {
	nodeMu.Lock()
	defer nodeMu.Unlock()
	delete(nodeLogs, id)
}
