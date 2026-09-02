package core

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/awg"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/store"
)

// The AmneziaWG lane. The master runs its own tunnel from here (internal/awg);
// a node runs the same thing from the state the panel hands it (nodeapi.AWGState).
// Users become peers through the same working-set and access rules as every
// other lane, and their counters land in the same traffic and sighting paths.

// awgParams converts the stored parameter block to the engine's.
func awgParams(p model.AWGParams) awg.Params {
	return awg.Params{Jc: p.Jc, Jmin: p.Jmin, Jmax: p.Jmax, S1: p.S1, S2: p.S2, H1: p.H1, H2: p.H2, H3: p.H3, H4: p.H4}
}

func modelAWGParams(p awg.Params) model.AWGParams {
	return model.AWGParams{Jc: p.Jc, Jmin: p.Jmin, Jmax: p.Jmax, S1: p.S1, S2: p.S2, H1: p.H1, H2: p.H2, H3: p.H3, H4: p.H4}
}

// awgOnlineWindow is how recent a peer's last handshake must be to count as a
// sighting: WireGuard rekeys every two minutes while traffic flows, so three is
// "connected now" with a margin.
const awgOnlineWindow = 180

// userWGKey returns a user's tunnel private key, minting and storing one the first
// time it is asked for. The key is the user's identity on every server, so it is
// made once and never rotated on its own.
func (m *Manager) userWGKey(u *model.User) (string, error) {
	if u.WGPrivateKey != "" {
		return u.WGPrivateKey, nil
	}
	priv, _, err := awg.GenerateKey()
	if err != nil {
		return "", err
	}
	if err := m.store.SetUserWGKey(u.ID, priv); err != nil {
		return "", err
	}
	u.WGPrivateKey = priv
	return priv, nil
}

// awgPeers turns the working users allowed on a server's AWG lane into peers.
// Users beyond the subnet (ids past 65,000) or without a derivable key are left
// out and logged rather than failing the whole tunnel.
func (m *Manager) awgPeers(serverID int64, users []model.User, access map[int64]model.Access) []awg.Peer {
	peers := make([]awg.Peer, 0, len(users))
	for i := range users {
		u := &users[i]
		if !model.AccessOf(access, u.ID).AllowsBuiltin(serverID, model.LaneAWG) {
			continue
		}
		addr, ok := awg.ClientAddr(u.ID)
		if !ok {
			logWarn("awg: user id beyond the tunnel subnet, skipped", "user", u.ID)
			continue
		}
		priv, err := m.userWGKey(u)
		if err != nil {
			logErr("awg: user key", "user", u.ID, "err", err)
			continue
		}
		pub, err := awg.PublicKey(priv)
		if err != nil {
			logErr("awg: user key unusable", "user", u.ID, "err", err)
			continue
		}
		peers = append(peers, awg.Peer{PublicKey: pub, Addr: addr, Email: model.UserEmail(u.ID)})
	}
	return peers
}

// syncAWGLocked brings the master's tunnel in line with the settings and the
// working set. Called under applyMu at the end of every reconcile and live user
// sync, so the peer list follows the Xray user set exactly. Never fails the
// caller: a tunnel that cannot start is reported (ConnectionsStatus) and retried
// on the next sync, while Xray keeps serving.
func (m *Manager) syncAWGLocked(set *model.Settings, users []model.User) {
	if m.awg == nil {
		return
	}
	if !set.AWGEnabled || set.AWGPrivateKey == "" || set.AWGPort == 0 {
		if m.awg.Running() {
			m.awg.Close()
		}
		return
	}
	access, err := m.store.AccessMap()
	if err != nil {
		logErr("awg: access map", "err", err)
		return
	}
	cfg := awg.Config{
		PrivateKey: set.AWGPrivateKey,
		ListenPort: set.AWGPort,
		Params:     awgParams(set.AWGParams),
		Peers:      m.awgPeers(model.LocalNodeID, users, access),
	}
	if err := m.awg.Apply(cfg); err != nil {
		// A build without TUN support (a developer's laptop) says so once at
		// startup, not on every user change.
		if err == awg.ErrUnsupported {
			return
		}
		logErr("awg: apply failed", "err", err)
	}
}

// PollAWG reads the master tunnel's counters and feeds them where Xray's go:
// traffic deltas per user, and a sighting for every peer that shook hands
// recently, from the address it did so — which is how the device limit and the
// online status see tunnel users at all.
func (m *Manager) PollAWG() error {
	if m.awg == nil || !m.awg.Running() {
		return nil
	}
	stats, err := m.awg.Stats()
	if err != nil {
		return err
	}
	if len(stats) == 0 {
		return nil
	}
	users, err := m.store.ListUsers()
	if err != nil {
		return err
	}
	byPub := make(map[string]*model.User, len(users))
	for i := range users {
		u := &users[i]
		if u.WGPrivateKey == "" {
			continue
		}
		if pub, err := awg.PublicKey(u.WGPrivateKey); err == nil {
			byPub[pub] = u
		}
	}
	now := time.Now().Unix()
	today := time.Now().In(m.loc()).Format("2006-01-02")
	var deltas []store.TrafficDelta
	m.awgMu.Lock()
	if m.awgLast == nil {
		m.awgLast = map[string]awg.PeerStat{}
	}
	for pub, st := range stats {
		u, ok := byPub[pub]
		if !ok {
			continue
		}
		prev := m.awgLast[pub]
		// rx on the server is the user's upload, tx their download — the same
		// orientation Xray's counters have.
		addUp, addDown := st.RxBytes-prev.RxBytes, st.TxBytes-prev.TxBytes
		if st.RxBytes < prev.RxBytes { // device restarted → counters from zero
			addUp = st.RxBytes
		}
		if st.TxBytes < prev.TxBytes {
			addDown = st.TxBytes
		}
		m.awgLast[pub] = st
		if addUp > 0 || addDown > 0 {
			deltas = append(deltas, store.TrafficDelta{
				UserID: u.ID, NodeID: model.LocalNodeID, Day: today,
				AddUp: nonNeg(addUp), AddDown: nonNeg(addDown), SeenAt: now,
			})
		}
		if st.LastHandshake > 0 && now-st.LastHandshake <= awgOnlineWindow {
			if ip := awg.EndpointIP(st.Endpoint); ip != "" {
				m.RecordAccessOn(model.LocalNodeID, model.UserEmail(u.ID), ip, "")
			}
		}
	}
	m.awgMu.Unlock()
	if len(deltas) == 0 {
		return nil
	}
	if err := m.store.ApplyTrafficDeltas(deltas); err != nil {
		return err
	}
	return m.enforceAfterTraffic(users)
}

// AWGStatus is what the Connections panel shows about the master's tunnel.
func (m *Manager) AWGStatus() (running bool, lastErr string) {
	if m.awg == nil {
		return false, ""
	}
	return m.awg.Running(), m.awg.LastError()
}

// StopAWG tears the master's tunnel down (shutdown).
func (m *Manager) StopAWG() {
	if m.awg != nil {
		m.awg.Close()
	}
}

// ensureMasterAWGIdentity mints the master's keypair, parameters and port the
// first time the lane is switched on, or when asked to regenerate. A regenerated
// identity invalidates every client config handed out so far — which is the
// point of asking for it.
func (m *Manager) ensureMasterAWGIdentity(set *model.Settings, regen bool) error {
	if set.AWGPrivateKey != "" && !set.AWGParams.IsZero() && !regen {
		return nil
	}
	priv, pub, err := awg.GenerateKey()
	if err != nil {
		return err
	}
	params := awg.RandomParams()
	if err := m.store.SaveAWGKeys(priv, pub, modelAWGParams(params)); err != nil {
		return err
	}
	set.AWGPrivateKey, set.AWGPublicKey, set.AWGParams = priv, pub, modelAWGParams(params)
	return nil
}

// ensureNodeAWGIdentity is the node-side twin.
func (m *Manager) ensureNodeAWGIdentity(n *model.Node, regen bool) error {
	if n.AWGPrivateKey != "" && !n.AWGParams.IsZero() && !regen {
		return nil
	}
	priv, pub, err := awg.GenerateKey()
	if err != nil {
		return err
	}
	params := awg.RandomParams()
	if err := m.store.SaveNodeAWGKeys(n.ID, priv, pub, modelAWGParams(params)); err != nil {
		return err
	}
	n.AWGPrivateKey, n.AWGPublicKey, n.AWGParams = priv, pub, modelAWGParams(params)
	return nil
}

// pickAWGPort chooses a UDP port for a fresh tunnel: high and random rather than
// WireGuard's well-known 51820, which a DPI box needs no handshake to notice.
func pickAWGPort() int {
	for range 20 {
		p := 30000 + int(time.Now().UnixNano()%30000)
		if portFree("udp", p) {
			return p
		}
		time.Sleep(time.Millisecond)
	}
	return 30000 + int(time.Now().UnixNano()%30000)
}

// validateAWGUpdate checks the port and DNS an operator entered for a server's
// tunnel and returns them normalised: 0 for the port means "pick one".
func validateAWGUpdate(port int, dns string) (int, string, error) {
	if port < 0 || port > 65535 {
		return 0, "", invalidCode("err.awgPortRange", "порт AmneziaWG вне диапазона 1–65535")
	}
	dns = strings.TrimSpace(dns)
	if dns != "" {
		for _, d := range strings.Split(dns, ",") {
			if net.ParseIP(strings.TrimSpace(d)) == nil {
				return 0, "", invalidCode("err.awgDNS", "DNS для AmneziaWG: IP-адреса через запятую")
			}
		}
	}
	return port, dns, nil
}

// AWGClientConfig renders one user's config for one server's tunnel — the file
// the Amnezia apps import. s is that server's materialised settings (the master's,
// or a node's from nodeSettings). Fails when the lane is off there or the user
// cannot be a peer; a config for a tunnel that will not accept the user is worse
// than none.
func (m *Manager) AWGClientConfig(u *model.User, s *model.Settings) (string, error) {
	if !s.AWGEnabled || s.AWGPublicKey == "" || s.AWGPort == 0 {
		return "", fmt.Errorf("awg: lane is off on server %d", s.ServerID)
	}
	addr, ok := awg.ClientAddr(u.ID)
	if !ok {
		return "", fmt.Errorf("awg: user %d is beyond the tunnel subnet", u.ID)
	}
	priv, err := m.userWGKey(u)
	if err != nil {
		return "", err
	}
	return awg.ClientConfig{
		PrivateKey:      priv,
		Address:         addr,
		DNS:             awgDNSOr(s),
		Params:          awgParams(s.AWGParams),
		ServerPublicKey: s.AWGPublicKey,
		Endpoint:        net.JoinHostPort(s.Host, strconv.Itoa(s.AWGPort)),
	}.Render(), nil
}

// awgDNSOr is what the client resolves through inside the tunnel: the operator's
// own AmneziaWG DNS when they set one, otherwise the plain resolvers from this
// server's DNS settings — the same ones Xray uses, so both lanes answer alike.
// A DoH/DoT URL from those settings is skipped: a WireGuard DNS line takes plain
// addresses, and a client handed a URL would silently fail to resolve anything.
// Nothing usable there leaves it to awg.DefaultDNS.
func awgDNSOr(s *model.Settings) string {
	if v := strings.TrimSpace(s.AWGDNS); v != "" {
		return v
	}
	var plain []string
	for _, f := range strings.FieldsFunc(s.XrayDNS, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ',' || r == ' '
	}) {
		if ip := net.ParseIP(strings.TrimSpace(f)); ip != nil {
			plain = append(plain, ip.String())
		}
	}
	if len(plain) == 0 {
		return awg.DefaultDNS
	}
	// Two is what a client needs; more only lengthens the config.
	if len(plain) > 2 {
		plain = plain[:2]
	}
	return strings.Join(plain, ", ")
}

// nodeAWGState is what a node needs to run its tunnel: its own identity and the
// peers allowed on it. nil when the lane is off on that node.
func (m *Manager) nodeAWGState(n *model.Node, ns *model.Settings, users []model.User, access map[int64]model.Access) *nodeapi.AWGState {
	if !ns.AWGEnabled || n.AWGPrivateKey == "" || ns.AWGPort == 0 {
		return nil
	}
	peers := m.awgPeers(n.ID, users, access)
	out := &nodeapi.AWGState{Port: ns.AWGPort, PrivateKey: n.AWGPrivateKey, Params: awgParams(n.AWGParams)}
	for _, p := range peers {
		out.Peers = append(out.Peers, nodeapi.AWGPeer{PublicKey: p.PublicKey, Addr: p.Addr.String(), Email: p.Email})
	}
	return out
}
