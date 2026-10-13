package nodeagent

import (
	"fmt"

	"github.com/AppsGanin/rospanel/internal/mieru"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// mieruErrMax bounds the listener error reported to the panel.
const mieruErrMax = 300

// syncMieru puts the node's mieru lane where the panel wants it, with mieru-in's
// clients from the config just applied; that ends the open sessions of anyone it no
// longer holds. Nil-safe for an Agent built without one (tests).
func (a *Agent) syncMieru(cfg []byte, st *nodeapi.MieruState) {
	if a.mieru == nil {
		return
	}
	clients, addr := xray.MieruClientsRaw(cfg)
	users := make([]mieru.User, 0, len(clients))
	for _, c := range clients {
		users = append(users, mieru.User{Email: c.Email, UUID: c.ID})
	}
	var l mieru.Listen
	if st != nil && st.Port > 0 {
		l = mieru.Listen{Port: st.Port, Transport: model.MieruTransportOr(st.Transport)}
	}
	// Off Xray's ports, whatever the panel sent: a listener retried in the gap of an Xray
	// restart would otherwise take Xray down (see core.mieruClearOfXray). The front's
	// public port is Xray's lane too.
	var err error
	if tag, taken := xray.TCPListenerPortsRaw(cfg)[l.Port]; l.Port > 0 && taken {
		l, err = mieru.Listen{}, fmt.Errorf("mieru: port %d is Xray's (%s); pick another", st.Port, tag)
	} else if l.Port > 0 && int64(l.Port) == a.frontPort.Load() {
		l, err = mieru.Listen{}, fmt.Errorf("mieru: port %d is the TLS front's; pick another", st.Port)
	}
	a.mieruMu.Lock()
	defer a.mieruMu.Unlock()
	if applyErr := a.mieru.Apply(l, addr, users); err == nil {
		err = applyErr
	}
	msg := ""
	if err != nil {
		if msg = err.Error(); len(msg) > mieruErrMax {
			msg = msg[:mieruErrMax]
		}
	}
	a.mieruErr.Store(&msg)
}

// mieruError is the listener's last error, for the next sync.
func (a *Agent) mieruError() string {
	if p := a.mieruErr.Load(); p != nil {
		return *p
	}
	return ""
}

// retryMieru starts the listener when the last sync could not (its port taken, say), so
// the node's report says how it stands now rather than when the state last changed.
func (a *Agent) retryMieru() {
	if a.mieru == nil || a.mieruError() == "" {
		return
	}
	a.mieruMu.Lock()
	defer a.mieruMu.Unlock()
	msg := ""
	if err := a.mieru.Retry(); err != nil {
		if msg = err.Error(); len(msg) > mieruErrMax {
			msg = msg[:mieruErrMax]
		}
	} else if a.mieru.Listening().Port == 0 {
		return
	}
	a.mieruErr.Store(&msg)
}
