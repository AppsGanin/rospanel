package nodeagent

import (
	"fmt"

	"github.com/AppsGanin/rospanel/internal/mtproto"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// syncMTProto puts the node's Telegram proxy where the panel wants it, with
// mtproto-in's clients from the config just applied; that ends the open connections of
// anyone it no longer holds. Nil-safe for an Agent built without one (tests).
func (a *Agent) syncMTProto(cfg []byte, st *nodeapi.MTProtoState) {
	if a.mtproto == nil {
		return
	}
	clients, addr := xray.MTProtoClientsRaw(cfg)
	users := make([]mtproto.User, 0, len(clients))
	for _, c := range clients {
		users = append(users, mtproto.User{Email: c.Email, UUID: c.ID})
	}
	var l mtproto.Listen
	if st != nil && st.Port > 0 {
		l = mtproto.Listen{Port: st.Port, Domain: st.Domain, Front: st.Front}
	}
	// Off Xray's ports and the front's, whatever the panel sent (see syncMieru).
	var err error
	if tag, taken := xray.TCPListenerPortsRaw(cfg)[l.Port]; l.Port > 0 && taken {
		l, err = mtproto.Listen{}, fmt.Errorf("mtproto: port %d is Xray's (%s); pick another", st.Port, tag)
	} else if l.Port > 0 && int64(l.Port) == a.frontPort.Load() {
		l, err = mtproto.Listen{}, fmt.Errorf("mtproto: port %d is the TLS front's; pick another", st.Port)
	}
	a.mtprotoMu.Lock()
	defer a.mtprotoMu.Unlock()
	if applyErr := a.mtproto.Apply(l, addr, users); err == nil {
		err = applyErr
	}
	msg := ""
	if err != nil {
		if msg = err.Error(); len(msg) > mieruErrMax {
			msg = msg[:mieruErrMax]
		}
	}
	a.mtprotoErr.Store(&msg)
}

// mtprotoError is the listener's last error, for the next sync.
func (a *Agent) mtprotoError() string {
	if p := a.mtprotoErr.Load(); p != nil {
		return *p
	}
	return ""
}

// retryMTProto starts the listener when the last sync could not (see retryMieru).
func (a *Agent) retryMTProto() {
	if a.mtproto == nil || a.mtprotoError() == "" {
		return
	}
	a.mtprotoMu.Lock()
	defer a.mtprotoMu.Unlock()
	msg := ""
	if err := a.mtproto.Retry(); err != nil {
		if msg = err.Error(); len(msg) > mieruErrMax {
			msg = msg[:mieruErrMax]
		}
	} else if a.mtproto.Listening().Port == 0 {
		return
	}
	a.mtprotoErr.Store(&msg)
}
