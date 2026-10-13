package mieru

import (
	"net"
	"testing"
	"time"
)

// A rekey is remembered for as long as a connection older than it is open, whatever
// happens to the user in between, and forgotten once none is.
func TestRawConnsRemembersARekeyWhileAnOldConnectionLives(t *testing.T) {
	r := newRawConns()
	a, b := net.Pipe()
	defer b.Close()
	old := &rawConn{Conn: a, reg: r, key: "1.2.3.4:5", at: time.Now().Add(-time.Minute), users: map[string]struct{}{}}
	r.m[old.key] = old
	r.rekey("u5") // the connection carried no session yet: not closed here
	r.forget()
	if !r.mu.TryLock() {
		t.Fatal("locked")
	}
	_, kept := r.rekeyed["u5"]
	r.mu.Unlock()
	if !kept {
		t.Fatal("the rekey was forgotten while an older connection is open")
	}
	if r.admit(old.key, "u5") {
		t.Fatal("a connection from before the rekey opened a session")
	}
	r.forget() // admit closed it
	if _, kept := r.rekeyed["u5"]; kept {
		t.Error("the rekey outlived every older connection")
	}
}
