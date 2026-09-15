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

// awgParams converts the stored parameter block to the engine's. A block that
// will not parse is a corrupt row, not a reason to run an empty configuration —
// the caller gets the zero Params and the tunnel refuses to come up saying why,
// rather than coming up with the obfuscation silently switched off.
func awgParams(p model.AWGParams) awg.Params {
	out, err := awg.FromModel(p)
	if err != nil {
		logErr("awg: stored parameters are unreadable", "err", err)
		return awg.Params{}
	}
	return out
}

// awgOnlineWindow is how recent a peer's last handshake must be to count as a
// sighting: WireGuard rekeys every two minutes while traffic flows, so three is
// "connected now" with a margin.
const awgOnlineWindow = 180

// claimAWG gives the users who lack one their tunnel identity — a private key and a
// slot on the subnet — and records it on the users handed in. The key is the user's
// identity on every server and the slot their address on every server, so both are
// made once and never changed on their own. A user left without a slot after a
// successful claim is one the subnet has no room for.
//
// One store transaction for the lot: the first tunnel a big panel brings up claims for
// every user it has.
func (m *Manager) claimAWG(users []*model.User) error {
	var claims []store.AWGClaim
	for _, u := range users {
		if u.WGPrivateKey != "" && u.AWGSlot != 0 {
			continue
		}
		key := u.WGPrivateKey
		if key == "" {
			priv, _, err := awg.GenerateKey()
			if err != nil {
				return err
			}
			key = priv
		}
		claims = append(claims, store.AWGClaim{UserID: u.ID, Key: key})
	}
	if len(claims) == 0 {
		return nil
	}
	// Another server may have claimed first; what the store kept is what counts.
	got, err := m.store.ClaimUsersAWG(claims, awg.FirstSlot, awg.LastSlot)
	if err != nil {
		return err
	}
	for _, u := range users {
		if id, ok := got[u.ID]; ok {
			u.WGPrivateKey, u.AWGSlot = id.Key, id.Slot
		}
	}
	// The users every node's state is built from were read before these existed.
	m.dropNodeInputs()
	return nil
}

// awgPeers turns the working users allowed on a server's AWG lane into peers. A user
// without an address (the subnet is full) or without a usable key is left out and
// logged rather than failing the whole tunnel.
func (m *Manager) awgPeers(serverID int64, users []model.User, access map[int64]model.Access) []awg.Peer {
	peers, _ := m.awgPeersClaimed(serverID, users, access)
	return peers
}

// awgPeersClaimed is awgPeers that also says whether every allowed user's identity was
// claimed; false leaves the users whose claim failed out of the peers.
func (m *Manager) awgPeersClaimed(serverID int64, users []model.User, access map[int64]model.Access) ([]awg.Peer, bool) {
	allowed := make([]*model.User, 0, len(users))
	for i := range users {
		if model.AccessOf(access, users[i].ID).AllowsBuiltin(serverID, model.LaneAWG) {
			allowed = append(allowed, &users[i])
		}
	}
	claimed := true
	if err := m.claimAWG(allowed); err != nil {
		logErr("awg: user tunnel identities", "server", serverID, "err", err)
		claimed = false
	}
	peers := make([]awg.Peer, 0, len(allowed))
	unplaced := 0
	for _, u := range allowed {
		addr, ok := awg.ClientAddr(u.AWGSlot)
		if !ok {
			if claimed {
				unplaced++
			}
			continue
		}
		if u.WGPrivateKey == "" {
			logErr("awg: user key unreadable (wrong or missing secrets.key?)", "user", u.ID)
			continue
		}
		pub, err := awg.PublicKey(u.WGPrivateKey)
		if err != nil {
			logErr("awg: user key unusable", "user", u.ID, "err", err)
			continue
		}
		peers = append(peers, awg.Peer{PublicKey: pub, Addr: addr, Email: model.UserEmail(u.ID)})
	}
	if unplaced > 0 && claimed {
		logWarn("awg: no address left on the tunnel subnet, users left out",
			"server", serverID, "users", unplaced, "capacity", awg.LastSlot-awg.FirstSlot+1)
	}
	return peers, claimed
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
	if err := m.store.SaveAWGKeys(priv, pub, awg.ToModel(params)); err != nil {
		return err
	}
	set.AWGPrivateKey, set.AWGPublicKey, set.AWGParams = priv, pub, awg.ToModel(params)
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
	if err := m.store.SaveNodeAWGKeys(n.ID, priv, pub, awg.ToModel(params)); err != nil {
		return err
	}
	n.AWGPrivateKey, n.AWGPublicKey, n.AWGParams = priv, pub, awg.ToModel(params)
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
	if err := m.claimAWG([]*model.User{u}); err != nil {
		return "", err
	}
	addr, ok := awg.ClientAddr(u.AWGSlot)
	if !ok {
		return "", fmt.Errorf("awg: no address left on the tunnel subnet for user %d", u.ID)
	}
	if u.WGPrivateKey == "" {
		return "", fmt.Errorf("awg: stored key for user %d is unreadable", u.ID)
	}
	return awg.ClientConfig{
		PrivateKey:      u.WGPrivateKey,
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
	st, _ := m.nodeAWGStateClaimed(n, ns, users, access)
	return st
}

// nodeAWGStateClaimed is nodeAWGState that also says whether every peer's identity was
// claimed (true when there is no tunnel to claim for).
func (m *Manager) nodeAWGStateClaimed(n *model.Node, ns *model.Settings, users []model.User, access map[int64]model.Access) (*nodeapi.AWGState, bool) {
	if !ns.AWGEnabled || n.AWGPrivateKey == "" || ns.AWGPort == 0 {
		return nil, true
	}
	params := awgParams(n.AWGParams)
	// An agent that predates AmneziaWG 3.1 reads h1–h4 as numbers, and a range
	// arriving as a string fails its decode of the WHOLE sync response — the node
	// would stop syncing altogether, not merely lose its tunnel. So a node too old
	// to read these parameters is not sent them: it keeps the tunnel it is already
	// running until it updates, and says so in its diagnostics.
	if params.NeedsAgent31() && !nodeSpeaks31(n.NodeVersion) {
		logWarn("awg: node too old for the 3.1 parameters, tunnel state withheld",
			"node", n.ID, "node_version", n.NodeVersion)
		return nil, true
	}
	// awgPeers records a key it mints on the user it was handed, and these users are
	// the snapshot every node shares (nodeInputs): it works on its own copy.
	peers, claimed := m.awgPeersClaimed(n.ID, append([]model.User(nil), users...), access)
	out := &nodeapi.AWGState{Port: ns.AWGPort, PrivateKey: n.AWGPrivateKey, Params: params}
	for _, p := range peers {
		out.Peers = append(out.Peers, nodeapi.AWGPeer{PublicKey: p.PublicKey, Addr: p.Addr.String(), Email: p.Email})
	}
	return out, claimed
}

// awgAgent31 is the first panel release whose node agent reads AmneziaWG 3.1
// parameters. A node reporting anything older cannot be handed them.
const awgAgent31 = 3

// nodeSpeaks31 reports whether a node's agent can read a 3.1 parameter block. An
// empty version is a node that has not reported yet, and the safe reading of "not
// reported" is "not new enough".
func nodeSpeaks31(nodeVersion string) bool {
	major, _, ok := strings.Cut(strings.TrimPrefix(strings.TrimSpace(nodeVersion), "v"), ".")
	if !ok {
		return false
	}
	n, err := strconv.Atoi(major)
	return err == nil && n >= awgAgent31
}
