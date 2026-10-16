package mieru

import (
	"errors"
	"net"
	"syscall"
	"testing"
)

// scriptedListener answers Accept from a script: an error, or a connection.
type scriptedListener struct {
	net.Listener
	script []error // nil: a connection
	calls  int
}

func (l *scriptedListener) Accept() (net.Conn, error) {
	i := l.calls
	l.calls++
	if i >= len(l.script) {
		return nil, net.ErrClosed
	}
	if err := l.script[i]; err != nil {
		return nil, err
	}
	c, _ := net.Pipe()
	return c, nil
}

// mieru stops accepting for good on the first error Accept returns; the listener it
// is given must hand it only the close.
func TestAcceptWaitsOutFailures(t *testing.T) {
	t.Parallel()
	emfile := &net.OpError{Op: "accept", Net: "tcp", Err: syscall.EMFILE}
	inner := &scriptedListener{script: []error{emfile, emfile, nil}}
	l := &trackedListener{Listener: inner, reg: &rawConns{m: map[string]*rawConn{}}}
	c, err := l.Accept()
	if err != nil || c == nil {
		t.Fatalf("Accept after two failures: %v %v", c, err)
	}
	_ = c.Close()
	if inner.calls != 3 {
		t.Errorf("accepted on call %d, want 3", inner.calls)
	}
	if _, err := l.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Errorf("a closed listener: %v, want net.ErrClosed", err)
	}
}
