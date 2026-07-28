package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/AppsGanin/rospanel/internal/auth"
	"github.com/AppsGanin/rospanel/internal/connguard"
	"github.com/AppsGanin/rospanel/internal/hop"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// InboundView is one custom inbound as the UI sees it: the stored record plus the
// facts the editor needs but shouldn't have to derive — which subscription formats
// can't carry this combination, and the REALITY public material a client needs.
type InboundView struct {
	model.Inbound
	// Unsupported names the subscription formats that will silently skip this
	// inbound (see model.Inbound.UnsupportedFormats). Shown as a warning, not an
	// error: those clients simply won't see this lane, everything else still works.
	Unsupported []string `json:"unsupported"`
	// RealityPublicKey / RealityShortID are the public halves clients need. The
	// private key never leaves the panel and is not part of this view.
	RealityPublicKey string `json:"reality_public_key,omitempty"`
	RealityShortID   string `json:"reality_short_id,omitempty"`

	// The advanced blobs, taken apart into the same typed forms the editor submits, so
	// the UI binds fields directly instead of parsing JSON. The raw json.RawMessage
	// versions on the embedded Inbound.Opts are cleared below so the view carries one
	// representation, not two.
	XHTTPExtraForm model.XHTTPExtraForm `json:"xhttp_extra_form"`
	SockoptForm    model.SockoptForm    `json:"sockopt_form"`
	TLSExtraForm   model.TLSExtraForm   `json:"tls_extra_form"`
}

// inboundView builds the UI view: strips the private key and the raw blobs, and
// exposes the advanced settings as forms.
func inboundView(in model.Inbound) InboundView {
	v := InboundView{
		Inbound:          in,
		Unsupported:      in.UnsupportedFormats(),
		RealityPublicKey: in.Opts.RealityPublicKey,
		RealityShortID:   in.Opts.RealityShortID,
		XHTTPExtraForm:   model.DisassembleXHTTPExtra(in.Opts.XHTTPExtra),
		SockoptForm:      model.DisassembleSockopt(in.Opts.Sockopt),
		TLSExtraForm:     model.DisassembleTLSExtra(in.Opts.TLSExtra),
	}
	v.Opts.RealityPrivateKey = ""
	// One representation of the advanced settings, not two: the forms above are the
	// view's; the raw blobs would only be a second copy the client would have to
	// reconcile.
	v.Opts.XHTTPExtra, v.Opts.Sockopt, v.Opts.TLSExtra = nil, nil, nil
	return v
}

// Inbounds returns one server's custom inbounds for the UI (LocalNodeID = master).
func (m *Manager) Inbounds(serverID int64) ([]InboundView, error) {
	if err := m.checkServerExists(serverID); err != nil {
		return nil, err
	}
	list, err := m.store.Inbounds(serverID)
	if err != nil {
		return nil, err
	}
	out := make([]InboundView, 0, len(list))
	for _, in := range list {
		out = append(out, inboundView(in))
	}
	return out, nil
}

// checkServerExists rejects a server id that isn't the master or a live node, so an
// inbound can never be parked against a deleted node.
func (m *Manager) checkServerExists(serverID int64) error {
	if serverID == model.LocalNodeID {
		return nil
	}
	n, err := m.store.GetNode(serverID)
	if err != nil {
		return err
	}
	if n == nil {
		return invalidCode("err.serverNotFound", "сервер не найден")
	}
	return nil
}

// effectiveSettings materializes the settings of one server: the panel's own for the
// master, the node's resolved view for a node. It is what the reserved-port set and
// the config generator are derived from.
func (m *Manager) effectiveSettings(serverID int64) (*model.Settings, error) {
	set, err := m.store.GetSettings()
	if err != nil {
		return nil, err
	}
	if serverID == model.LocalNodeID {
		set.ServerID = model.LocalNodeID
		return set, nil
	}
	n, err := m.store.GetNode(serverID)
	if err != nil {
		return nil, err
	}
	if n == nil {
		return nil, invalidCode("err.serverNotFound", "сервер не найден")
	}
	return nodeSettings(set, n), nil
}

// reservedPorts is what a custom inbound on this server may NOT bind: the built-in
// lanes' ports and the panel's own loopback machinery. Named, because the operator
// gets told which one they collided with.
//
// The built-in lanes are listed whether or not they are currently enabled: their
// listener comes back the moment the lane is switched on, and discovering the
// collision then — as an Xray that won't start — is exactly the failure this set
// exists to prevent.
func reservedPorts(set *model.Settings) model.ReservedPorts {
	r := model.ReservedPorts{}
	// Named through a helper rather than a map literal: two built-in lanes routinely
	// share a number (Vision on TCP/443 and Hysteria2 on UDP/443 is the default), and
	// a literal would silently keep whichever key came last — telling the operator
	// their port collides with the wrong thing.
	hold := func(port int, who string) {
		if port <= 0 {
			return
		}
		if prev, ok := r[port]; ok && prev != who {
			r[port] = prev + " / " + who
			return
		}
		r[port] = who
	}
	hold(set.VLESSPort, "VLESS-Vision")
	hold(set.RealityPort, "VLESS-XHTTP-REALITY")
	hold(set.HysteriaPort, "HYSTERIA-UDP")
	hold(xray.APIPort, "Xray internal API")
	if set.HopEnd > set.HysteriaPort {
		// The built-in hop range is a funnel onto the Hysteria port: anything inside
		// it would have its traffic silently stolen by the nftables redirect.
		for p := set.HysteriaPort + 1; p <= set.HopEnd; p++ {
			hold(p, "HYSTERIA-UDP hop range")
		}
	}
	if set.ProxyModeEnabled {
		hold(set.ProxyModePort, "proxy mode")
	}
	if set.OperaEnabled {
		hold(set.OperaPortOr(), "Opera VPN")
	}
	return r
}

// CreateInbound validates and stores a new custom inbound, generating REALITY key
// material when the combination needs it.
func (m *Manager) CreateInbound(ctx context.Context, in model.Inbound) (*InboundView, error) {
	if err := m.checkServerExists(in.ServerID); err != nil {
		return nil, err
	}
	in.Normalize()
	if err := m.prepareInbound(&in); err != nil {
		return nil, err
	}
	if err := m.validateAgainstSet(ctx, in, 0); err != nil {
		return nil, err
	}
	saved, err := m.store.CreateInbound(in)
	if err != nil {
		return nil, inboundConflict(err)
	}
	m.applyInboundChange(in.ServerID)
	v := inboundView(*saved)
	return &v, nil
}

// UpdateInbound validates and stores an edit. The server it belongs to is taken
// from the stored row, not the request: moving an inbound between servers would
// carry a port that is free on one box onto another where it may not be.
func (m *Manager) UpdateInbound(ctx context.Context, in model.Inbound) (*InboundView, error) {
	cur, err := m.store.GetInbound(in.ID)
	if err != nil {
		return nil, err
	}
	if cur == nil {
		return nil, invalidCode("err.inboundNotFound", "подключение не найдено")
	}
	in.ServerID = cur.ServerID
	in.CreatedAt = cur.CreatedAt
	in.Sort = cur.Sort
	in.Normalize()
	// Carry the stored REALITY material forward — the UI never sees the private key,
	// so it can't send it back. A donor change keeps the same identity on purpose:
	// re-keying is a separate, explicit act (regen), not a side effect of editing.
	if in.Opts.Security == model.SecReality {
		in.Opts.RealityPrivateKey = cur.Opts.RealityPrivateKey
		in.Opts.RealityPublicKey = cur.Opts.RealityPublicKey
		in.Opts.RealityShortID = cur.Opts.RealityShortID
	}
	if err := m.prepareInbound(&in); err != nil {
		return nil, err
	}
	if err := m.validateAgainstSet(ctx, in, in.ID); err != nil {
		return nil, err
	}
	if err := m.store.UpdateInbound(in); err != nil {
		return nil, inboundConflict(err)
	}
	m.applyInboundChange(in.ServerID)
	saved, err := m.store.GetInbound(in.ID)
	if err != nil {
		return nil, err
	}
	v := inboundView(*saved)
	return &v, nil
}

// RegenInboundReality mints fresh REALITY material for one inbound. Existing clients
// must re-import their links afterwards, so it is its own action rather than
// something an edit does silently.
func (m *Manager) RegenInboundReality(id int64) (*InboundView, error) {
	in, err := m.store.GetInbound(id)
	if err != nil {
		return nil, err
	}
	if in == nil {
		return nil, invalidCode("err.inboundNotFound", "подключение не найдено")
	}
	if in.Opts.Security != model.SecReality {
		return nil, invalidCode("err.inboundHasNoReality", "у этого подключения нет REALITY")
	}
	in.Opts.RealityPrivateKey = ""
	if err := m.prepareInbound(in); err != nil {
		return nil, err
	}
	if err := m.store.UpdateInbound(*in); err != nil {
		return nil, err
	}
	m.applyInboundChange(in.ServerID)
	v := inboundView(*in)
	return &v, nil
}

// DeleteInbound removes an inbound; its users lose that lane on the next apply.
func (m *Manager) DeleteInbound(id int64) error {
	in, err := m.store.GetInbound(id)
	if err != nil {
		return err
	}
	if in == nil {
		return nil // already gone; deleting twice is not an error
	}
	if err := m.store.DeleteInbound(id); err != nil {
		return err
	}
	// Sweep any group grant that pointed at this inbound, so a group doesn't keep a
	// token for something that no longer exists.
	if err := m.store.DeleteInboundGrants(id); err != nil {
		logErr("groups: grant cleanup after inbound delete failed", "inbound", id, "err", err)
	}
	m.applyInboundChange(in.ServerID)
	return nil
}

// prepareInbound fills in what the panel owns rather than the operator: REALITY key
// material for a combination that needs it.
func (m *Manager) prepareInbound(in *model.Inbound) error {
	if !in.NeedsRealityKeys() {
		return nil
	}
	priv, pub, err := auth.GenerateRealityKeys()
	if err != nil {
		return err
	}
	shortIDs, err := auth.RandomShortIDs()
	if err != nil {
		return err
	}
	in.Opts.RealityPrivateKey = priv
	in.Opts.RealityPublicKey = pub
	in.Opts.RealityShortID = shortIDs
	return nil
}

// validateAgainstSet is the whole pre-save gate: the per-inbound rules, the
// cross-inbound ones (ports, names, hop overlaps) against the set this write would
// produce, a live REALITY donor probe, and finally an actual bind test on the target
// machine. Everything here runs BEFORE anything is written, so a bad configuration
// never reaches the server it would break.
//
// excludeID is the row being replaced (0 on create), so an edit doesn't collide with
// its own stored copy.
func (m *Manager) validateAgainstSet(ctx context.Context, in model.Inbound, excludeID int64) error {
	set, err := m.effectiveSettings(in.ServerID)
	if err != nil {
		return err
	}
	existing, err := m.store.Inbounds(in.ServerID)
	if err != nil {
		return err
	}
	next := make([]model.Inbound, 0, len(existing)+1)
	var prev *model.Inbound
	for i := range existing {
		if existing[i].ID == excludeID {
			prev = &existing[i]
			continue
		}
		next = append(next, existing[i])
	}
	next = append(next, in)
	reserved := reservedPorts(set)
	// The panel's own loopback listener. Every server runs one — the master's admin
	// API, a node's decoy — on the same address, so binding it would take the port the
	// VLESS fallback delivers non-VPN traffic to. The bind probe would catch it, but
	// only as an anonymous "port busy"; naming it says what actually holds it.
	if _, port, err := net.SplitHostPort(m.opts.PanelDest); err == nil {
		if p, err := strconv.Atoi(port); err == nil {
			if _, taken := reserved[p]; !taken {
				reserved[p] = "panel internal port"
			}
		}
	}
	if err := model.ValidateInboundSet(next, reserved, set.BuiltinLaneLabels()); err != nil {
		return fromFieldErr(err)
	}

	// A REALITY donor has to actually serve TLS 1.3 + HTTP/2 with a small enough
	// certificate, or the lane comes up and no client can complete a handshake, with
	// nothing in the logs to explain why. Only probed when the donor changed, so an
	// unrelated edit doesn't pay for a network round trip.
	if in.Opts.Security == model.SecReality && in.Enabled {
		if prev == nil || prev.Opts.RealityDest != in.Opts.RealityDest {
			for _, d := range in.Opts.RealityServerNames() {
				if err := validateRealityDestLive(d); err != nil {
					return err
				}
			}
		}
	}

	// The bind test: is the port actually free on the machine this inbound will run
	// on? Only for an enabled inbound whose port is new — re-saving an inbound that
	// already holds its port would otherwise fail against itself.
	if in.Enabled && (prev == nil || !prev.Enabled || prev.Port != in.Port) {
		if err := m.probePort(ctx, in.ServerID, portNetwork(in), in.Port); err != nil {
			return err
		}
	}

	// Finally, the only check that can judge the ADVANCED settings: hand the whole
	// candidate config to Xray and see whether it parses. The key whitelists catch a
	// misspelled name, but not a value Xray dislikes — its parser is the only
	// authority on that, and asking it here is what keeps a bad blob out of the
	// database instead of finding out from a crashed process and a rollback.
	if in.Enabled {
		if err := m.validateCandidate(ctx, in.ServerID, next); err != nil {
			return err
		}
	}
	return nil
}

// validateCandidate generates the config this inbound set would produce and asks Xray
// to parse it — locally for the master, over the node's long-poll otherwise.
//
// A server that cannot be asked (no local binary, node offline, agent too old) is
// logged and allowed through: the alternative is refusing every save whenever a node
// is briefly unreachable, and the apply path still validates and rolls back.
func (m *Manager) validateCandidate(ctx context.Context, serverID int64, set []model.Inbound) error {
	enabled := make([]model.Inbound, 0, len(set))
	for _, in := range set {
		if in.Enabled {
			enabled = append(enabled, in)
		}
	}
	cfg, err := m.candidateConfig(serverID, enabled)
	if err != nil {
		// Generation failing is a panel bug, not operator input; don't block the save
		// on it — the apply path will surface it properly.
		logErr("inbound: candidate config generation failed", "server", serverID, "err", err)
		return nil
	}

	if serverID == model.LocalNodeID {
		if err := m.sup.ValidateConfig(cfg); err != nil {
			return invalidCode("err.xrayRejectedConfig", "Xray отклонил конфигурацию: {{err}}", map[string]any{"err": err.Error()})
		}
		return nil
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil
	}
	switch err := m.checkNodeConfig(ctx, serverID, raw); {
	case err == nil:
		return nil
	case errors.Is(err, errProbeUnavailable):
		logWarn("inbound: node config check skipped", "server", serverID)
		return nil
	default:
		return err
	}
}

// candidateConfig builds the Xray config a server would run with the given inbound
// set — the same generator the real apply uses, so what is validated is what would
// be applied.
func (m *Manager) candidateConfig(serverID int64, custom []model.Inbound) (*xray.Config, error) {
	users, err := m.store.WorkingUsers(time.Now().Unix())
	if err != nil {
		return nil, err
	}
	opts := m.genOpts()
	opts.Custom = custom

	if serverID == model.LocalNodeID {
		set, err := m.store.GetSettings()
		if err != nil {
			return nil, err
		}
		return xray.Generate(set, users, opts, m.getProxies())
	}
	set, err := m.effectiveSettings(serverID)
	if err != nil {
		return nil, err
	}
	// The node substitutes its own cert paths on apply; the sentinels keep this
	// candidate identical to what it will actually be asked to run.
	set.CertPath = nodeapi.CertPathSentinel
	set.KeyPath = nodeapi.KeyPathSentinel
	return xray.Generate(set, users, opts, m.getNodeProxies(serverID))
}

// inboundConflict turns a unique-index violation into the same user-facing message
// the pre-write validation would have produced. It only fires when two saves race:
// the checks in validateAgainstSet are a read followed by a write, so the database
// is what actually decides.
func inboundConflict(err error) error {
	switch {
	case errors.Is(err, store.ErrInboundPortTaken):
		return invalidCode("err.portTakenHere", "порт уже занят другим подключением на этом сервере — выбери другой")
	case errors.Is(err, store.ErrInboundNameTaken):
		return invalidCode("err.inboundNameTakenHere", "название уже занято другим подключением на этом сервере")
	}
	return err
}

// hasHysteria reports whether any custom inbound is Hysteria2. Such an inbound
// cannot have its users live-updated (QUIC authenticators are fixed at start), so
// its presence is what makes a user change need a reconcile.
func hasHysteria(list []model.Inbound) bool {
	for _, in := range list {
		if in.Protocol == model.InbHysteria {
			return true
		}
	}
	return false
}

// portNetwork is the transport-layer network an inbound listens on. Hysteria2 is
// QUIC, so it binds UDP; everything else binds TCP. Testing the wrong one would pass
// while the real bind fails.
func portNetwork(in model.Inbound) string {
	if in.Protocol == model.InbHysteria {
		return "udp"
	}
	return "tcp"
}

// probePort asks the machine that will run this inbound whether the port is free.
//
// On the master that is a local bind. On a node the panel cannot bind anything
// itself, so it asks the node over the sync channel (see ProbeNodePort). A node that
// can't answer — offline, or an agent too old to know the request — is NOT treated
// as a failure: refusing to configure a temporarily unreachable server would be
// worse than the thing being guarded against, and the node still has its own
// validate-and-rollback if the config turns out not to start.
func (m *Manager) probePort(ctx context.Context, serverID int64, network string, port int) error {
	if serverID == model.LocalNodeID {
		if !portFree(network, port) {
			return invalidCode("err.portTakenOnServer", "порт {{port}} ({{network}}) уже занят на этом сервере — выберите другой", map[string]any{"port": port, "network": network})
		}
		return nil
	}
	free, err := m.ProbeNodePort(ctx, serverID, network, port)
	if err != nil {
		logWarn("inbound: node port probe skipped", "node", serverID, "port", port, "err", err)
		return nil
	}
	if !free {
		return invalidCode("err.portTakenOnServer", "порт {{port}} ({{network}}) уже занят на этом сервере — выберите другой", map[string]any{"port": port, "network": network})
	}
	return nil
}

// applyInboundChange pushes the new inbound set to the server it belongs to: a
// reconcile (and a refreshed nftables funnel set) for the master, a wake for a node
// so its held poll returns with the fresh config.
func (m *Manager) applyInboundChange(serverID int64) {
	if serverID != model.LocalNodeID {
		m.nodes.wakeOne(serverID)
		return
	}
	if err := EnsureHostHops(m.store); err != nil {
		logErr("hop: re-apply failed", "err", err)
	}
	m.ensureLocalConnGuard()
	m.TriggerReconcile()
}

// EnsureHostHops re-installs this host's nftables funnels: the built-in Hysteria2
// lane's range plus every custom Hysteria2 inbound that asks for hopping.
//
// It must be the ONLY thing that writes those rules. The table is dropped and
// recreated on every apply, so anything installing a subset erases the rest — which
// is exactly what boot used to do to the custom funnels, leaving them missing until
// an unrelated edit happened to re-apply them.
//
// Package-level and store-driven rather than a Manager method, because boot needs it
// before the Manager exists.
func EnsureHostHops(st *store.Store) error {
	set, err := st.GetSettings()
	if err != nil {
		return err
	}
	ranges := []hop.Range{{Start: set.HopStart, End: set.HopEnd, Target: set.HysteriaPort}}
	list, err := st.EnabledInbounds(model.LocalNodeID)
	if err != nil {
		return err
	}
	for _, in := range list {
		if in.UsesHopping() {
			ranges = append(ranges, hop.Range{Start: in.Opts.HopStart, End: in.Opts.HopEnd, Target: in.Port})
		}
	}
	return hop.EnsureAll(ranges)
}

// HostConnGuardPorts is the set of public TCP ports the per-IP flood guard should
// protect on this host: the built-in lanes plus every enabled custom inbound that
// listens on TCP.
//
// Custom inbounds belong here for the same reason they belong in the node's list —
// they are public listeners on the same box, and leaving them out would quietly make
// "add a custom inbound" the way to bypass the guard. Hysteria2 is excluded: the
// guard counts connections, which QUIC has none of.
func HostConnGuardPorts(st *store.Store) ([]int, error) {
	set, err := st.GetSettings()
	if err != nil {
		return nil, err
	}
	ports := []int{set.VLESSPort}
	if set.RealityEnabled {
		ports = append(ports, set.RealityPort)
	}
	list, err := st.EnabledInbounds(model.LocalNodeID)
	if err != nil {
		return nil, err
	}
	for _, in := range list {
		if in.Protocol != model.InbHysteria {
			ports = append(ports, in.Port)
		}
	}
	return ports, nil
}

// SetConnGuard records the operator's per-IP guard preference and the limits to use,
// so the Manager can re-apply the rules itself when the port set changes. Without
// this the guard would only ever match the ports that existed at boot.
func (m *Manager) SetConnGuard(wanted bool, lim connguard.Limits) {
	m.connGuardWanted.Store(wanted)
	m.connGuardMu.Lock()
	m.connGuardLimits = lim
	m.connGuardMu.Unlock()
}

// ensureLocalConnGuard re-applies the per-IP guard for the current port set.
// Best-effort: connguard degrades to a no-op without nft or root.
func (m *Manager) ensureLocalConnGuard() {
	if !m.connGuardWanted.Load() {
		return
	}
	ports, err := HostConnGuardPorts(m.store)
	if err != nil {
		logErr("connguard: port list failed", "err", err)
		return
	}
	m.connGuardMu.Lock()
	lim := m.connGuardLimits
	m.connGuardMu.Unlock()
	if err := connguard.Ensure(ports, lim); err != nil {
		logErr("connguard: re-apply failed", "err", err)
	}
}

// inboundNames is the display names already taken by a server's custom inbounds, so
// renaming a built-in lane can't land on one of them.
func (m *Manager) inboundNames(serverID int64) []string {
	list, err := m.store.Inbounds(serverID)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, in := range list {
		out = append(out, in.Name)
	}
	return out
}

// nodeHopMeta is the funnel set for one node's Hysteria2 lanes, shipped in its
// desired state so the agent installs exactly the rules the panel computed instead
// of deriving a different set from a subset of the facts.
//
// Nil when the node has no Hysteria2 at all, so an agent that receives nothing tears
// its table down rather than keeping a stale funnel alive.
func nodeHopMeta(set *model.Settings, custom []model.Inbound) []nodeapi.HopRange {
	var out []nodeapi.HopRange
	if set.HysteriaEnabled && set.HopEnd > set.HysteriaPort {
		out = append(out, nodeapi.HopRange{Start: set.HopStart, End: set.HopEnd, Target: set.HysteriaPort})
	}
	for _, in := range custom {
		if in.UsesHopping() {
			out = append(out, nodeapi.HopRange{Start: in.Opts.HopStart, End: in.Opts.HopEnd, Target: in.Port})
		}
	}
	return out
}

// nodeCheckTimeout is how long a node has to validate a candidate config. Longer
// than the port probe: `xray run -test` parses a whole document and starts (then
// tears down) every handler, which on a small box is a second or two.
const nodeCheckTimeout = 15 * time.Second

// nowUnix is the current unix time, wrapped so the node-liveness checks in this
// package read the same way.
func nowUnix() int64 { return time.Now().Unix() }

// inboundProbeTimeout is how long a node has to answer a port probe before the panel
// gives up and lets the save through. Generous next to a node's round trip (its held
// poll returns as soon as the panel wakes it), short enough that an unreachable node
// doesn't leave an operator staring at a spinner.
const inboundProbeTimeout = 8 * time.Second

// errProbeUnavailable means the node never answered — the caller treats it as
// "couldn't check", not as "port is busy".
var errProbeUnavailable = fmt.Errorf("node did not answer the port probe")
