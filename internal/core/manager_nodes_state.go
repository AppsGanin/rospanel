package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// nodeSettings materializes a node's effective settings: the global settings row
// with the node's own identity (address, TLS, REALITY) and protocol overrides
// applied. Everything else — ports, hop range, fingerprints, sub delivery —
// inherits from global, so xray.Generate, the link builders and tlsmgr all work
// for a remote node without changes.
//
// Egress (proxy lanes, WARP, Opera) is the node's OWN and independent of the master:
// each server has its own proxy pool, its own WARP registration and its own Opera
// helper. All egress is off by default, so a node with no config egresses direct.
func nodeSettings(set *model.Settings, n *model.Node) *model.Settings {
	ns := *set // shallow copy; we only overwrite value fields below
	ns.ServerID = n.ID
	ns.ServerPlacement = n.Placement
	ns.Host = n.Host
	ns.SNI = n.Host
	ns.RealityPrivateKey = n.RealityPrivateKey
	ns.RealityPublicKey = n.RealityPublicKey
	ns.RealityShortID = n.RealityShortID
	ns.RealityPath = n.RealityPath
	// AmneziaWG: the node's own identity, never the master's; the port, name and
	// DNS ride in the connections blob below (off ⇒ zero port ⇒ no config).
	ns.AWGEnabled = derefBool(n.AWGEnabled)
	ns.AWGPrivateKey = n.AWGPrivateKey
	ns.AWGPublicKey = n.AWGPublicKey
	ns.AWGParams = n.AWGParams
	ns.AWGPort, ns.AWGName, ns.AWGDNS = 0, "", ""
	// REALITY donor: the node's own if set, otherwise inherit the panel's (a node
	// needs some donor for REALITY to work).
	if n.RealityDest != "" {
		ns.RealityDest = n.RealityDest
	}

	// A node's protocols are its OWN — no inheritance from the master. Unset ⇒ off.
	ns.VLESSEnabled = derefBool(n.VLESSEnabled)
	ns.HysteriaEnabled = derefBool(n.HysteriaEnabled)
	ns.RealityEnabled = derefBool(n.RealityEnabled)

	// TLS hints for this node's share links come from what the node reported about
	// its live cert — the panel can't read the remote node's disk.
	ns.TLSInsecure = n.CertSelfSigned
	ns.TLSPinSHA256 = ""
	if n.CertSelfSigned {
		ns.TLSPinSHA256 = n.CertSHA256
	}

	// Routing + egress are the node's OWN (each server is independent — a node does
	// not borrow the master's lanes/WARP/Opera, which point at the master's backends).
	// Nil routing ⇒ empty (direct). All egress is off by default, so a node with no
	// config produces the same "direct" output as before.
	if n.Routing != nil {
		ns.Routing = *n.Routing
	} else {
		ns.Routing = model.RoutingConfig{}
	}
	ns.WarpEnabled = n.WarpEnabled
	ns.WarpPrivateKey = n.WarpPrivateKey
	ns.WarpPublicKey = n.WarpPublicKey
	ns.WarpEndpoint = n.WarpEndpoint
	ns.WarpAddressV4 = n.WarpAddressV4
	ns.WarpAddressV6 = n.WarpAddressV6
	ns.WarpReserved = n.WarpReserved
	ns.OperaEnabled = n.OperaEnabled
	ns.OperaCountry = n.OperaCountry

	// System proxies are the node's OWN, never the master's: inheriting would open a
	// listener on every node the moment the master enabled one, and would write the
	// master's proxy password onto every node's disk.
	ns.ProxySocksEnabled = n.Proxy.SocksEnabled
	ns.ProxySocksPort = n.Proxy.SocksPort
	ns.ProxyHTTPEnabled = n.Proxy.HTTPEnabled
	ns.ProxyHTTPPort = n.Proxy.HTTPPort
	ns.ProxyAccounts = n.Proxy.Accounts

	// DNS: the node's OWN (no inheritance). Unset ⇒ Xray's default resolver.
	if n.XrayDNS != nil {
		ns.XrayDNS = *n.XrayDNS
	} else {
		ns.XrayDNS = ""
	}

	// Connection transport: the node's own if configured, otherwise inherit the
	// master's (ns already carries the master's values from the shallow copy).
	if c := n.Connections; c != nil {
		ns.HysteriaPort = c.HysteriaPort
		ns.HopStart = c.HopStart
		ns.HopEnd = c.HopEnd
		ns.HopInterval = c.HopInterval
		ns.HysteriaObfs = c.HysteriaObfs
		ns.RealityPort = c.RealityPort
		ns.RealityMaxTimeDiff = c.RealityMaxTimeDiff
		ns.TLSFragment = c.TLSFragment
		ns.TLSMin13 = c.TLSMin13
		ns.BlockQUIC = c.BlockQUIC
		ns.VLESSFp = c.VLESSFp
		ns.RealityFp = c.RealityFp
		ns.VLESSName = c.VLESSName
		ns.RealityName = c.RealityName
		ns.HysteriaName = c.HysteriaName
		ns.AWGPort = c.AWGPort
		ns.AWGName = c.AWGName
		ns.AWGDNS = c.AWGDNS
	}
	return &ns
}

// derefBool resolves an optional per-node bool to its value, treating unset as false
// (a node's toggles are its own — nothing is inherited from the master).
func derefBool(b *bool) bool { return b != nil && *b }

// NodeDesiredState builds the full desired state for a node: its Xray config
// (generated panel-side from nodeSettings + the working user set), the host-level
// meta the agent needs, and a hash over both so the sync handler can skip no-ops.
//
// It always builds. A sync that only needs to know whether the node is current asks
// NodeStateChange, which can answer without building.
func (m *Manager) NodeDesiredState(n *model.Node) (*nodeapi.NodeState, error) {
	x, err := m.readNodeStateInputs(n)
	if err != nil {
		return nil, err
	}
	if err := m.stateGate.acquire(context.Background()); err != nil {
		return nil, err
	}
	defer m.stateGate.release()
	state, _, err := m.buildNodeState(n, x)
	return state, err
}

// buildNodeState builds a node's state from inputs already read. complete is false
// when a part was left out on a soft failure — the custom inbounds unreadable, the
// speed caps or blocks unreadable, a user's tunnel identity not claimed — so the state
// serves this sync but is not remembered.
//
// The caller holds the state gate (see stateGate).
func (m *Manager) buildNodeState(n *model.Node, x *nodeStateInputs) (state *nodeapi.NodeState, complete bool, err error) {
	b, err := m.generateNodeState(n, x, x.in.users)
	if err != nil {
		return nil, false, err
	}
	// Who this state lets in is who the node's reports may speak for from now on.
	m.noteNodeServed(n.ID, b.cfg, b.meta.AWG, x.servedSeq)
	raw, err := json.Marshal(b.cfg)
	if err != nil {
		return nil, false, err
	}
	metaRaw, err := json.Marshal(b.meta)
	if err != nil {
		return nil, false, err
	}
	// Hashed as a stream, not over a joined copy: appending metaRaw to raw copied the
	// whole config — another ten megabytes at 50,000 users — to hash bytes it already
	// had. The digest is the same one.
	h := sha256.New()
	_, _ = h.Write(raw)
	_, _ = h.Write(metaRaw)
	return &nodeapi.NodeState{
		Hash:       hex.EncodeToString(h.Sum(nil)),
		XrayConfig: raw,
		Meta:       b.meta,
	}, b.complete, nil
}

// nodeBuild is a node's generated Xray config and host meta, not yet encoded.
type nodeBuild struct {
	cfg  *xray.Config
	meta nodeapi.NodeMeta
	// custom are the custom inbounds the config was generated with: none when they
	// could not be read, which also makes the build incomplete.
	custom   []model.Inbound
	complete bool
}

// generateNodeState generates a node's config and meta for the given users — all the
// working users for a whole state, or some of them for their part of one (see
// manager_node_split.go). complete is as buildNodeState describes.
func (m *Manager) generateNodeState(n *model.Node, x *nodeStateInputs, users []model.User) (*nodeBuild, error) {
	set, in := x.set, x.in
	complete := in.version != 0
	ns := nodeSettings(set, n)
	// Cert paths are sentinels the agent rewrites to its own absolute paths (the
	// panel doesn't know the node's data dir); keeping them symbolic makes the hash
	// independent of where the node stores its certs.
	ns.CertPath = nodeapi.CertPathSentinel
	ns.KeyPath = nodeapi.KeyPathSentinel
	// The node's own fallback points at its local decoy/panel loopback, same as the
	// panel's own layout. Egress lanes resolve against the node's OWN proxy pool.
	opts := x.opts
	opts.ServerID = n.ID
	opts.Access = in.access
	if x.inbErr != nil {
		// Soft, as in genOptsFor: the built-in lanes still keep the server reachable.
		logErr("inbounds: load failed", "server", n.ID, "err", x.inbErr)
		complete = false
	} else {
		opts.Custom = x.inbounds
	}
	// Users this node's WireGuard inbounds hold need a tunnel identity. A claim made here
	// reaches the shared inputs on their next read (claimAWG drops them), so a state
	// built on a fresh or failed claim is not remembered.
	genUsers, wgClaimed, wgOK := m.claimWireGuard(users, opts.Custom, opts.Access)
	complete = complete && wgOK && !wgClaimed
	cfg, err := xray.Generate(ns, genUsers, opts, x.proxies)
	if err != nil {
		return nil, err
	}
	connGuardPorts := []int{ns.VLESSPort}
	if ns.RealityEnabled {
		connGuardPorts = append(connGuardPorts, ns.RealityPort)
	}
	// Custom inbounds get the same per-IP flood guard as the built-in lanes — they are
	// public listeners on the same box, and leaving them out would make "add a custom
	// inbound" quietly the way to bypass the guard. Only the TCP ones: the guard's
	// rules count connections, which UDP/QUIC has none of.
	for _, in := range opts.Custom {
		if model.ProtoOf(in.Protocol) == "tcp" {
			connGuardPorts = append(connGuardPorts, in.Port)
		}
	}
	// ACME: the node's own provider/email/EAB when set, otherwise the panel's.
	acmeEmail := set.ACMEEmail
	if n.ACMEEmail != "" {
		acmeEmail = n.ACMEEmail
	}
	acmeProvider, eabKID, eabHMAC := set.ACMEProvider, set.ZeroSSLEABKID, set.ZeroSSLEABHMAC
	if n.ACMEProvider != "" {
		acmeProvider, eabKID, eabHMAC = n.ACMEProvider, n.ZeroSSLEABKID, n.ZeroSSLEABHMAC
	}
	meta := nodeapi.NodeMeta{
		Host:              n.Host,
		SNI:               n.Host,
		ACMEEmail:         acmeEmail,
		ACMEProvider:      acmeProvider,
		ZeroSSLEABKID:     eabKID,
		ZeroSSLEABHMAC:    eabHMAC,
		HysteriaEnabled:   ns.HysteriaEnabled,
		HysteriaPort:      ns.HysteriaPort,
		HopStart:          ns.HopStart,
		HopEnd:            ns.HopEnd,
		HopRanges:         nodeHopMeta(ns, opts.Custom),
		ConnGuardPorts:    connGuardPorts,
		TurnRelays:        nodeTurnRelays(opts.Custom),
		LoopbackDest:      m.opts.PanelDest,
		DecoyTemplate:     n.DecoyTemplate,
		GeoRefreshHours:   n.GeoRefreshHours, // the node's OWN geo cadence
		XrayPinnedVersion: xray.PinnedVersion,
		SpeedLimits:       in.speed,
	}
	var claimed bool
	meta.AWG, claimed = m.nodeAWGStateClaimed(n, ns, users, in.access)
	complete = complete && claimed
	// What the source policy has refused, for this node's own firewall. Read here
	// rather than pushed on each block so a node that was offline catches up on its
	// next sync, and so the hash covers it (a lifted block reaches the node too).
	if len(in.blocked) > 0 {
		meta.BlockedIPs = in.blocked
		meta.BlockTTLHours = int(policyTTL(set.ConnPolicy) / time.Hour)
	}
	// The addresses banned by hand, the same way.
	if len(in.banned) > 0 {
		meta.BannedIPs = in.banned
	}
	if ns.OperaEnabled {
		meta.OperaEnabled = true
		meta.OperaCountry = ns.OperaCountryOr()
		meta.OperaPort = ns.OperaPortOr()
	}
	return &nodeBuild{cfg: cfg, meta: meta, custom: opts.Custom, complete: complete}, nil
}

// nodeInputs are the parts of every node's desired state that no node owns: the working
// users' credentials, the access map, the speed caps and the blocked addresses.
//
// Each node's sync used to read all four for itself, twice a poll and again on every
// wake — and a wake reaches every node at once. With 20,000 users that was ~28ms of
// the ~36ms a node's state took, repeated per node. Read once, they are shared by
// every node until something changes.
//
// "Something changes" is a wake: every change nodes must see already wakes them (a
// user sync, a node or policy edit), and the registry counts wakes, so a snapshot
// taken before the latest one is never used. What changes with no wake at all — a
// blocked address running out, say — is picked up when the snapshot ages out, which
// nodeInputsTTL keeps well inside a poll.
//
// version numbers what the inputs say, not when they were read: a read that finds what
// the last one found keeps its version, which is what lets a node's remembered state
// outlive a wake that changed nothing (see NodeStateChange). 0 is a read that failed
// softly and is not to be remembered.
type nodeInputs struct {
	gen     uint64
	version uint64
	at      time.Time
	users   []model.User // read-only: copy before changing a user (see nodeAWGState)
	ids     []int64      // the users' ids, in their order: what the next read compares
	access  map[int64]model.Access
	speed   map[string]int
	blocked []string
	banned  []string
}

// nodeInputs returns the shared inputs, reading them afresh when the cached ones
// predate the latest wake or have aged out. Callers must not modify what they get.
func (m *Manager) nodeInputs() (*nodeInputs, error) {
	m.nodeInputsMu.Lock()
	defer m.nodeInputsMu.Unlock()
	gen := m.nodes.generation()
	if c := m.nodeInputsCache; c != nil && c.gen == gen && time.Since(c.at) < nodeInputsTTL {
		return c, nil
	}
	ws, err := m.readWorkingSet()
	if err != nil {
		return nil, err
	}
	return m.readNodeInputsLocked(ws)
}

// workingSet is one read of who belongs in the config and what their speed caps are
// (store.WorkingSet), stamped with the wake generation and the time taken before it.
type workingSet struct {
	gen  uint64
	at   time.Time
	ids  []int64
	caps map[int64]int
}

// readWorkingSet reads the working set. The generation is taken before reading: a
// wake that lands mid-read leaves what is built from it one generation behind, so the
// next caller reads again rather than trusting it.
func (m *Manager) readWorkingSet() (*workingSet, error) {
	ws := &workingSet{gen: m.nodes.generation(), at: time.Now()}
	var err error
	if ws.ids, ws.caps, err = m.store.WorkingSet(ws.at.Unix()); err != nil {
		return nil, err
	}
	return ws, nil
}

// readNodeInputsLocked builds the shared inputs on the working set given and, unless
// the rest of the read came back incomplete, makes them the snapshot every node
// shares. The caller holds nodeInputsMu.
func (m *Manager) readNodeInputsLocked(ws *workingSet) (*nodeInputs, error) {
	prev := m.nodeInputsCache
	in := &nodeInputs{gen: ws.gen, at: ws.at}
	ids, capped := ws.ids, ws.caps
	var err error
	// The credentials cost several times the rest of this read put together, in time
	// and in garbage both, and nothing can change one for a user already in the set:
	// a uuid and a password are written when the account is made and never again, and
	// a tunnel identity is claimed once — by a path that forgets this snapshot (see
	// claimAWG). So while the set is the same, last read's credentials are the same.
	if prev != nil && slices.Equal(prev.ids, ids) {
		in.users, in.ids = prev.users, prev.ids
	} else {
		if in.users, err = m.store.WorkingCredentials(in.at.Unix()); err != nil {
			return nil, err
		}
		// Taken from the users in hand rather than from the ids read a moment before:
		// a set that moved in between must not pass for the one these are the
		// credentials of, or the next read would reuse them for it.
		in.ids = make([]int64, len(in.users))
		for i := range in.users {
			in.ids[i] = in.users[i].ID
		}
	}
	// A hard failure, as in genOptsFor: without the access map every restricted
	// user's credential would be written into every lane.
	if in.access, err = m.store.AccessMap(); err != nil {
		return nil, fmt.Errorf("load access map: %w", err)
	}
	if len(capped) > 0 {
		in.speed = make(map[string]int, len(capped))
		for id, kbps := range capped {
			in.speed[model.UserEmail(id)] = kbps
		}
	}
	complete := true
	if in.blocked, err = m.store.BlockedIPList(); err != nil {
		logErr("node state: cannot read blocked addresses", "err", err)
		complete = false
	}
	if in.banned, err = m.store.BannedIPList(); err != nil {
		logErr("node state: cannot read banned addresses", "err", err)
		complete = false
	}
	// A read that failed softly serves this build but is not kept: sharing it would
	// drop the blocks from every node's state for the whole TTL.
	if complete {
		// Compared with the last complete read rather than the cache, which a change no
		// wake announces drops: the same inputs keep their version either way, and a
		// new version is told apart from the one before it (see inputsJournal).
		last := m.nodeInputsLast
		if last != nil && sameNodeInputs(last, in) {
			in.version = last.version
		} else {
			m.nodeInputsVersion++
			in.version = m.nodeInputsVersion
			m.nodeJournal.record(last, in)
		}
		m.nodeInputsCache = in
		m.nodeInputsLast = in
	}
	return in, nil
}

// fleetChanged builds the shared inputs afresh on the working set given and reports
// whether what the nodes are served has moved since the last read — which is the whole
// question behind waking them. The read that answers it is the read they would each
// have made, so the snapshot it leaves behind is the one they get: asking costs the
// fleet nothing.
//
// Anything it cannot answer counts as changed. A wake that was not needed is a little
// work; one that was needed and skipped would hold a config back until the node's own
// poll.
func (m *Manager) fleetChanged(ws *workingSet) bool {
	m.nodeInputsMu.Lock()
	defer m.nodeInputsMu.Unlock()
	prev := m.nodeInputsCache
	in, err := m.readNodeInputsLocked(ws)
	if err != nil {
		logErr("node state: cannot tell whether the nodes' inputs changed", "err", err)
		return true
	}
	return prev == nil || in.version == 0 || in.version != prev.version
}

// sameNodeInputs reports whether two reads found the same inputs. Users are compared on
// what WorkingCredentials reads — the fields every config builder uses (see
// TestGenerateReadsOnlyCredentials, and TestWorkingUserIDsMatchWorkingUsers for the
// read itself).
func sameNodeInputs(a, b *nodeInputs) bool {
	if len(a.users) != len(b.users) || !maps.Equal(a.speed, b.speed) || !slices.Equal(a.blocked, b.blocked) ||
		!slices.Equal(a.banned, b.banned) ||
		!reflect.DeepEqual(a.access, b.access) {
		return false
	}
	// The same users, reused rather than read again: there is nothing to walk.
	if len(a.users) == 0 || &a.users[0] == &b.users[0] {
		return true
	}
	for i := range a.users {
		x, y := &a.users[i], &b.users[i]
		if x.ID != y.ID || x.UUID != y.UUID || x.Password != y.Password ||
			x.WGPrivateKey != y.WGPrivateKey || x.AWGSlot != y.AWGSlot {
			return false
		}
	}
	return true
}

// dropNodeInputs forgets the shared inputs, so the next node state reads them afresh —
// for a change made while building one, which no wake announces.
func (m *Manager) dropNodeInputs() {
	m.nodeInputsMu.Lock()
	m.nodeInputsCache = nil
	m.nodeInputsMu.Unlock()
}

// NodeXrayConfig returns one server's Xray config for the read-only viewer: the
// master's live on-disk config.json for node 0, and for a remote node the config
// the panel generates and pushes (the same bytes the node applies).
//
// The node's copy differs in one respect: the cert/key paths are still the panel's
// sentinels here, because only the agent knows its own data dir — it substitutes
// them before handing the config to Xray. The viewer says so.
func (m *Manager) NodeXrayConfig(id int64) ([]byte, error) {
	if id == model.LocalNodeID {
		return m.XrayConfig()
	}
	n, err := m.store.GetNode(id)
	if err != nil {
		return nil, err
	}
	if n == nil {
		return nil, invalidCode("err.nodeNotFound", "нода не найдена")
	}
	state, err := m.NodeDesiredState(n)
	if err != nil {
		return nil, err
	}
	return state.XrayConfig, nil
}
