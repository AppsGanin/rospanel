package tuning

import (
	"context"
	"net"
	"time"
)

// TCPUserTimeout bounds unacknowledged data, including a peer that advertises a
// zero window. Keepalive alone does not bound this case: a pending write uses
// TCP's much longer retransmission timer instead of the keepalive timer.
const TCPUserTimeout = 3 * time.Minute

// ListenTCP listens with a bounded wait for an unresponsive peer on Linux.
// Accepted sockets inherit TCP_USER_TIMEOUT; healthy idle connections are not
// expired. Go's normal TCP keepalive settings remain in force.
func ListenTCP(addr string) (net.Listener, error) {
	lc := TCPListenConfig()
	return lc.Listen(context.Background(), "tcp", addr)
}

// TCPListenConfig returns the same socket policy for listeners that need to add
// controls of their own, such as mieru's reuse-port control.
func TCPListenConfig() net.ListenConfig {
	lc := net.ListenConfig{Control: tcpControl}
	// TCP_USER_TIMEOUT is not supported by Linux MPTCP sockets.
	lc.SetMultipathTCP(false)
	return lc
}
