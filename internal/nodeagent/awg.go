package nodeagent

import (
	"log/slog"
	"net/netip"
	"time"

	"github.com/AppsGanin/rospanel/internal/awg"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
)

// The node's AmneziaWG tunnel: brought in line with what the panel sent on every
// state apply, read for counters and sightings on every stats sample. Both feed
// the same report fields Xray's users do, so the panel never learns which lane a
// byte came through — and does not need to.

// awgOnlineWindow mirrors core.awgOnlineWindow: a handshake this recent means
// the peer is connected now.
const awgOnlineWindow = 180

// syncAWG applies (or tears down) the tunnel for the state the panel sent.
func (a *Agent) syncAWG(st *nodeapi.AWGState) {
	if a.awg == nil {
		return
	}
	if st == nil {
		if a.awg.Running() {
			a.awg.Close()
		}
		a.awgMu.Lock()
		a.awgEmails = nil
		a.awgMu.Unlock()
		return
	}
	cfg := awg.Config{PrivateKey: st.PrivateKey, ListenPort: st.Port, Params: st.Params}
	emails := make(map[string]string, len(st.Peers))
	for _, p := range st.Peers {
		addr, err := netip.ParseAddr(p.Addr)
		if err != nil {
			continue
		}
		cfg.Peers = append(cfg.Peers, awg.Peer{PublicKey: p.PublicKey, Addr: addr, Email: p.Email})
		emails[p.PublicKey] = p.Email
	}
	a.awgMu.Lock()
	a.awgEmails = emails
	a.awgMu.Unlock()
	if err := a.awg.Apply(cfg); err != nil && err != awg.ErrUnsupported {
		slog.Warn("node: amneziawg apply failed", "err", err)
	}
}

// sampleAWG folds the tunnel's counters into the pending traffic deltas and its
// recent handshakes into the connection samples, exactly as sampleStats and the
// access-log tap do for Xray.
func (a *Agent) sampleAWG() {
	if a.awg == nil || !a.awg.Running() {
		return
	}
	stats, err := a.awg.Stats()
	if err != nil || len(stats) == 0 {
		return
	}
	a.awgMu.Lock()
	emails := a.awgEmails
	if a.awgLast == nil {
		a.awgLast = map[string]awg.PeerStat{}
	}
	now := time.Now().Unix()
	type sighting struct{ email, ip string }
	var seen []sighting
	a.statsMu.Lock()
	for pub, st := range stats {
		email, ok := emails[pub]
		if !ok {
			continue
		}
		uid, ok := userIDFromEmail(email)
		if !ok {
			continue
		}
		prev := a.awgLast[pub]
		addUp, addDown := st.RxBytes-prev.RxBytes, st.TxBytes-prev.TxBytes
		if st.RxBytes < prev.RxBytes {
			addUp = st.RxBytes
		}
		if st.TxBytes < prev.TxBytes {
			addDown = st.TxBytes
		}
		a.awgLast[pub] = st
		if addUp > 0 || addDown > 0 {
			d := a.pending[uid]
			if d == nil {
				d = &nodeapi.TrafficDelta{UserID: uid}
				a.pending[uid] = d
			}
			if addUp > 0 {
				d.Up += addUp
			}
			if addDown > 0 {
				d.Down += addDown
			}
		}
		if st.LastHandshake > 0 && now-st.LastHandshake <= awgOnlineWindow {
			if ip := awg.EndpointIP(st.Endpoint); ip != "" {
				seen = append(seen, sighting{email, ip})
			}
		}
	}
	a.statsMu.Unlock()
	a.awgMu.Unlock()
	for _, s := range seen {
		a.recordConn(s.email, s.ip, "")
	}
}
