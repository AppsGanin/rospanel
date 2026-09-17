package xray

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
)

// WireGuard users are changed in the running Xray the way Hysteria2 users are, through
// the API, because the CLI refuses this inbound type as well. Unlike Hysteria2 nothing
// has to be cut off afterwards: removing a peer removes its keys from the device, so
// the packets of a session it already had no longer decrypt, and the replies to it have
// nowhere to go. No inbound is ever rebuilt either — a changed key is the old peer
// removed and the new one added, which touches nobody else.

// wireGuardPeerState is one peer as the running inbound holds it.
type wireGuardPeerState struct {
	key        string // hex, as the API takes it
	allowedIPs string // joined, for comparison
}

// wireGuardInbound is one WireGuard inbound as it should be.
type wireGuardInbound struct {
	tag   string
	peers []WireGuardInboundPeer
}

// readWireGuardLive reads the peers of the WireGuard inbounds a process started with.
func readWireGuardLive(cfg []byte) (map[string]map[string]wireGuardPeerState, error) {
	var doc struct {
		Inbounds []struct {
			Tag      string          `json:"tag"`
			Protocol string          `json:"protocol"`
			Settings json.RawMessage `json:"settings"`
		} `json:"inbounds"`
	}
	if len(cfg) == 0 {
		return nil, errors.New("the config the process started with is unknown")
	}
	if err := json.Unmarshal(cfg, &doc); err != nil {
		return nil, fmt.Errorf("read the running config: %w", err)
	}
	out := map[string]map[string]wireGuardPeerState{}
	for _, in := range doc.Inbounds {
		if in.Protocol != model.InbWireGuard || in.Tag == "" {
			continue
		}
		var settings WireGuardInboundSettings
		if len(in.Settings) > 0 {
			if err := json.Unmarshal(in.Settings, &settings); err != nil {
				return nil, fmt.Errorf("read the peers of %s: %w", in.Tag, err)
			}
		}
		peers, err := wireGuardPeerStates(in.Tag, settings.Peers)
		if err != nil {
			return nil, err
		}
		out[in.Tag] = peers
	}
	return out, nil
}

// wireGuardPeerStates indexes peers by email. Removal goes by email, so a peer with none
// or a shared one cannot be managed live.
func wireGuardPeerStates(tag string, peers []WireGuardInboundPeer) (map[string]wireGuardPeerState, error) {
	out := make(map[string]wireGuardPeerState, len(peers))
	for _, p := range peers {
		if _, dup := out[p.Email]; dup || p.Email == "" {
			return nil, fmt.Errorf("%s holds a peer with no email or a shared one (%q)", tag, p.Email)
		}
		key, err := wireGuardKeyHex(p.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("%s: peer %s: %w", tag, p.Email, err)
		}
		out[p.Email] = wireGuardPeerState{key: key, allowedIPs: strings.Join(p.AllowedIPs, ",")}
	}
	return out, nil
}

// tunnelUsers maps each WireGuard inbound's tag to its peers by tunnel address.
//
// Xray's WireGuard inbound writes its access-log lines without the user — every other
// inbound appends "email: <user>", this one never sets the field (proxy/wireguard/
// server.go, still so on main) — so the online status, the sites and the abuse matching,
// all of which read the log, would never see its users. What the line does carry is the
// connection's source, and on this inbound that is the peer's own tunnel address, which
// allowedIPs pins to exactly one peer. The tap looks the user up by it.
type tunnelUsers map[string]map[string]string // inbound tag → tunnel address → email

// tunnelUsersOf builds the lookup from peers as the sync tracks them. Only a peer's
// single-address prefixes are taken: those are the addresses the panel gives, and a
// wider one could not name one user.
func tunnelUsersOf(peers map[string]map[string]wireGuardPeerState) *tunnelUsers {
	out := tunnelUsers{}
	for tag, byEmail := range peers {
		addrs := map[string]string{}
		for email, p := range byEmail {
			for _, cidr := range strings.Split(p.allowedIPs, ",") {
				prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
				if err == nil && prefix.IsSingleIP() {
					addrs[prefix.Addr().String()] = email
				}
			}
		}
		if len(addrs) > 0 {
			out[tag] = addrs
		}
	}
	return &out
}

// attribute reads a WireGuard inbound's access line — "from tcp:10.66.0.2:31239 accepted
// tcp:example.com:443 [custom-18 >> direct]" — as the tap reads any other: the user, the
// address they connected from and the destination. The address is the tunnel one, the
// only one the inbound knows; the client's real address never reaches it, since the
// packets arrive through the call service's relay. "" for a line from any other inbound
// or an address no peer holds.
func (t *tunnelUsers) attribute(line string) (email, ip, dest string) {
	if t == nil || len(*t) == 0 {
		return "", "", ""
	}
	a := strings.Index(line, " accepted ")
	if a < 0 {
		return "", "", ""
	}
	// The route is the bracket after the destination: "[in >> out]", "[in -> out]" or
	// "[in ==> out]" depending on how the rule matched. Searched for past the
	// destination, whose IPv6 form has brackets of its own.
	rest := line[a+len(" accepted "):]
	sp := strings.IndexByte(rest, ' ')
	if sp < 0 {
		return "", "", ""
	}
	rest = rest[sp:]
	open, closing := strings.Index(rest, " ["), strings.IndexByte(rest, ']')
	if open < 0 || closing < open {
		return "", "", ""
	}
	inTag := rest[open+2 : closing]
	for _, sep := range []string{" >> ", " -> ", " ==> "} {
		if in, _, ok := strings.Cut(inTag, sep); ok {
			inTag = in
			break
		}
	}
	addrs := (*t)[inTag]
	if addrs == nil {
		return "", "", ""
	}
	src := accessSource(line)
	if email = addrs[src]; email == "" {
		return "", "", ""
	}
	return email, src, accessDest(line)
}

// wireGuardKeyHex turns a base64 WireGuard key into the hex the API takes.
func wireGuardKeyHex(b64 string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) != 32 {
		return "", fmt.Errorf("not a WireGuard key: %q", b64)
	}
	return hex.EncodeToString(raw), nil
}

// SyncWireGuard brings the running Xray's WireGuard inbounds to the peers the generated
// config gives them, without disturbing anyone who stays.
func (s *Supervisor) SyncWireGuard(apiAddr string, inbounds []Inbound) error {
	want := make([]wireGuardInbound, 0, len(inbounds))
	for _, in := range inbounds {
		settings, ok := in.Settings.(WireGuardInboundSettings)
		if !ok {
			return fmt.Errorf("inbound %s carries no wireguard settings", in.Tag)
		}
		want = append(want, wireGuardInbound{tag: in.Tag, peers: settings.Peers})
	}
	s.runMu.Lock()
	defer s.runMu.Unlock()
	return s.syncWireGuardLocked(apiAddr, want)
}

// syncWireGuardLocked brings each WireGuard inbound named in want to its peers and leaves
// every other inbound alone. Caller holds runMu.
//
// A failure leaves the process holding peers no config describes: it is marked so, and
// the next Apply restarts it rather than finding it already current.
func (s *Supervisor) syncWireGuardLocked(apiAddr string, want []wireGuardInbound) (err error) {
	if len(want) == 0 {
		return nil
	}
	if s.bin == "" {
		return errors.New("xray binary unavailable")
	}
	s.mu.Lock()
	p := s.cur
	s.mu.Unlock()
	if p == nil {
		return errors.New("xray is not running")
	}
	if p.wireGuardLost {
		return errors.New("an earlier change to the running wireguard inbounds failed part way")
	}
	defer func() {
		if err != nil {
			p.wireGuardLost = true
			s.mu.Lock()
			if s.cur == p {
				s.appliedCfg = nil
			}
			s.mu.Unlock()
		}
	}()
	if p.wireGuard == nil {
		if p.wireGuard, err = readWireGuardLive(p.cfg); err != nil {
			return err
		}
	}

	api := newXrayAPI(apiAddr, s.waitFn)
	defer api.close()
	// The tap's lookup follows the peers, whether the sync gets all the way or not.
	defer func() { p.tunnelUsers.Store(tunnelUsersOf(p.wireGuard)) }()
	for _, in := range want {
		have, running := p.wireGuard[in.tag]
		if !running {
			// An inbound the config gained after this process started: the restart
			// that brings it up brings its peers.
			slog.Warn("xray: a wireguard inbound to update is not in the running process", "tag", in.tag)
			continue
		}
		next, err := wireGuardPeerStates(in.tag, in.peers)
		if err != nil {
			return err
		}
		var gone, joined []string
		for email, peer := range have {
			if now, kept := next[email]; !kept || now != peer {
				gone = append(gone, email)
			}
		}
		for email, peer := range next {
			if was, had := have[email]; !had || was != peer {
				joined = append(joined, email)
			}
		}
		slices.Sort(gone)
		slices.Sort(joined)
		for _, email := range gone {
			if err := api.removeUser(in.tag, email); err != nil {
				return err
			}
			delete(have, email)
		}
		for _, email := range joined {
			peer := next[email]
			if err := api.addWireGuardUser(in.tag, email, peer.key, strings.Split(peer.allowedIPs, ",")); err != nil {
				return err
			}
			have[email] = peer
		}
	}
	return nil
}
