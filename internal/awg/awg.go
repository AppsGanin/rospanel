// Package awg runs an AmneziaWG server: WireGuard with the handshake hidden
// behind junk packets and random-looking headers, so a DPI box that recognises
// plain WireGuard sees nothing it knows. The protocol engine is amneziawg-go,
// embedded in the process (no daemon, no separate binary); this package owns the
// parameters, the keys, the peer list, the client configs and the counters.
//
// One tunnel per server: the master runs its own, every node runs its own with the
// keys and parameters the panel generated for it. A user is a peer on each tunnel
// they are allowed on, with the same address on all of them.
package awg

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

// Iface is the tunnel interface name on every server.
const Iface = "awg0"

// DefaultMTU is what AmneziaWG clients default to; the junk headers ride inside
// the UDP payload, so the tunnel MTU is WireGuard's.
const DefaultMTU = 1420

// DefaultDNS is what clients resolve through inside the tunnel unless the
// operator says otherwise.
const DefaultDNS = "1.1.1.1, 8.8.8.8"

// Keepalive is the client-side persistent keepalive in seconds — the NATs
// mobile clients sit behind drop a silent UDP mapping in well under a minute.
const Keepalive = 25

// Params are the AmneziaWG obfuscation parameters (protocol version 1.0): how
// many junk packets precede the handshake and how big they are (Jc, Jmin, Jmax),
// how much random padding the two handshake messages carry (S1, S2), and the
// four message-type headers that replace WireGuard's 1/2/3/4 (H1–H4). Both ends
// must agree on every value, which is why they live in the server's row and go
// out in every client config.
type Params struct {
	Jc   int    `json:"jc"`
	Jmin int    `json:"jmin"`
	Jmax int    `json:"jmax"`
	S1   int    `json:"s1"`
	S2   int    `json:"s2"`
	H1   uint32 `json:"h1"`
	H2   uint32 `json:"h2"`
	H3   uint32 `json:"h3"`
	H4   uint32 `json:"h4"`
}

// IsZero reports an unset parameter block (a server that has never had AWG on).
func (p Params) IsZero() bool { return p == Params{} }

// RandomParams picks a parameter set inside the ranges the AmneziaWG authors
// recommend: 3–10 junk packets of 50–1000 bytes, 15–150 bytes of padding on each
// handshake message (with S1+56 ≠ S2, which the protocol requires so the two
// messages cannot be told apart by length), and four distinct random headers
// well clear of WireGuard's own 1–4.
func RandomParams() Params {
	p := Params{
		Jc:   randInt(3, 10),
		Jmin: 50,
		Jmax: 1000,
		S1:   randInt(15, 150),
		S2:   randInt(15, 150),
	}
	for p.S1+56 == p.S2 {
		p.S2 = randInt(15, 150)
	}
	seen := map[uint32]bool{}
	pick := func() uint32 {
		for {
			v := uint32(randInt(5, 2_000_000_000))
			if !seen[v] {
				seen[v] = true
				return v
			}
		}
	}
	p.H1, p.H2, p.H3, p.H4 = pick(), pick(), pick(), pick()
	return p
}

// Validate refuses parameter sets amneziawg-go would refuse or that break the
// obfuscation: the same rules RandomParams follows, stated as bounds.
func (p Params) Validate() error {
	switch {
	case p.Jc < 0 || p.Jc > 128:
		return errors.New("awg: jc must be 0–128")
	case p.Jmin < 0 || p.Jmax < 0 || p.Jmin > p.Jmax || p.Jmax > 1280:
		return errors.New("awg: jmin ≤ jmax ≤ 1280")
	case p.S1 < 0 || p.S1 > 1132 || p.S2 < 0 || p.S2 > 1188:
		return errors.New("awg: s1 ≤ 1132, s2 ≤ 1188")
	case p.S1+56 == p.S2:
		return errors.New("awg: s1 + 56 must not equal s2")
	case p.H1 < 5 || p.H2 < 5 || p.H3 < 5 || p.H4 < 5:
		return errors.New("awg: h1–h4 must be ≥ 5")
	case p.H1 == p.H2 || p.H1 == p.H3 || p.H1 == p.H4 || p.H2 == p.H3 || p.H2 == p.H4 || p.H3 == p.H4:
		return errors.New("awg: h1–h4 must differ")
	}
	return nil
}

func randInt(lo, hi int) int {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(hi-lo+1)))
	if err != nil {
		return lo
	}
	return lo + int(n.Int64())
}

// GenerateKey mints a Curve25519 keypair, base64 as WireGuard writes keys.
func GenerateKey() (priv, pub string, err error) {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(k.Bytes()),
		base64.StdEncoding.EncodeToString(k.PublicKey().Bytes()), nil
}

// PublicKey derives the public key of a base64 private key.
func PublicKey(privB64 string) (string, error) {
	raw, err := keyBytes(privB64)
	if err != nil {
		return "", err
	}
	k, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(k.PublicKey().Bytes()), nil
}

func keyBytes(b64 string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return nil, fmt.Errorf("awg: key is not base64: %w", err)
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("awg: key is %d bytes, want 32", len(raw))
	}
	return raw, nil
}

// keyHex is the UAPI form of a key: the same 32 bytes, hex.
func keyHex(b64 string) (string, error) {
	raw, err := keyBytes(b64)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// Subnet is the tunnel network every server uses: /16 leaves room for 65,000
// users, each at the address ClientAddr derives from their id, the same on
// every server so a config for one server differs from the next only in the
// endpoint and keys.
var Subnet = netip.MustParsePrefix("10.66.0.0/16")

// ServerAddr is the server's own tunnel address, the first host of Subnet.
var ServerAddr = netip.MustParseAddr("10.66.0.1")

// ClientAddr is a user's tunnel address: host index id+1 inside Subnet, so user
// 1 is 10.66.0.2 and the two reserved hosts (.0.0, .0.1) are never handed out.
// false when the id is beyond what the subnet holds.
func ClientAddr(userID int64) (netip.Addr, bool) {
	idx := userID + 1
	if userID <= 0 || idx >= 65535 {
		return netip.Addr{}, false
	}
	base := Subnet.Addr().As4()
	return netip.AddrFrom4([4]byte{base[0], base[1], byte(idx >> 8), byte(idx)}), true
}

// Peer is one client on a server's tunnel.
type Peer struct {
	PublicKey string // base64
	Addr      netip.Addr
	Email     string // the user's Xray tag ("u12"), for counters and sightings
}

// Config is everything a server's tunnel is set up from.
type Config struct {
	PrivateKey string // base64
	ListenPort int
	Params     Params
	MTU        int
	Peers      []Peer
}

// UAPI renders the whole configuration in WireGuard's cross-platform IPC form
// with the AmneziaWG extensions — the form amneziawg-go's IpcSet reads. Peers
// replace whatever the device held, so applying the same Config twice is a no-op
// and a user removed from the list is gone from the device.
func (c Config) UAPI() (string, error) {
	priv, err := keyHex(c.PrivateKey)
	if err != nil {
		return "", err
	}
	if c.ListenPort < 1 || c.ListenPort > 65535 {
		return "", fmt.Errorf("awg: listen port %d out of range", c.ListenPort)
	}
	if err := c.Params.Validate(); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "private_key=%s\nlisten_port=%d\n", priv, c.ListenPort)
	p := c.Params
	fmt.Fprintf(&b, "jc=%d\njmin=%d\njmax=%d\ns1=%d\ns2=%d\nh1=%d\nh2=%d\nh3=%d\nh4=%d\n",
		p.Jc, p.Jmin, p.Jmax, p.S1, p.S2, p.H1, p.H2, p.H3, p.H4)
	b.WriteString("replace_peers=true\n")
	peers := append([]Peer(nil), c.Peers...)
	sort.Slice(peers, func(i, j int) bool { return peers[i].PublicKey < peers[j].PublicKey })
	for _, pe := range peers {
		pub, err := keyHex(pe.PublicKey)
		if err != nil {
			return "", fmt.Errorf("peer %s: %w", pe.Email, err)
		}
		if !pe.Addr.IsValid() {
			return "", fmt.Errorf("peer %s: no address", pe.Email)
		}
		fmt.Fprintf(&b, "public_key=%s\nreplace_allowed_ips=true\nallowed_ip=%s/32\n", pub, pe.Addr)
	}
	return b.String(), nil
}

// ClientConfig is one user's side of one server's tunnel.
type ClientConfig struct {
	PrivateKey      string // the user's, base64
	Address         netip.Addr
	DNS             string
	MTU             int
	Params          Params
	ServerPublicKey string
	Endpoint        string // host:port
}

// Render writes the config file every AmneziaWG client imports (the app, the
// official CLI, a QR code): WireGuard's INI with the obfuscation keys added to
// [Interface].
func (c ClientConfig) Render() string {
	mtu := c.MTU
	if mtu <= 0 {
		mtu = DefaultMTU
	}
	dns := strings.TrimSpace(c.DNS)
	if dns == "" {
		dns = DefaultDNS
	}
	p := c.Params
	var b strings.Builder
	fmt.Fprintf(&b, "[Interface]\nPrivateKey = %s\nAddress = %s/32\nDNS = %s\nMTU = %d\n",
		c.PrivateKey, c.Address, dns, mtu)
	fmt.Fprintf(&b, "Jc = %d\nJmin = %d\nJmax = %d\nS1 = %d\nS2 = %d\nH1 = %d\nH2 = %d\nH3 = %d\nH4 = %d\n",
		p.Jc, p.Jmin, p.Jmax, p.S1, p.S2, p.H1, p.H2, p.H3, p.H4)
	fmt.Fprintf(&b, "\n[Peer]\nPublicKey = %s\nAllowedIPs = 0.0.0.0/0, ::/0\nEndpoint = %s\nPersistentKeepalive = %d\n",
		c.ServerPublicKey, c.Endpoint, Keepalive)
	return b.String()
}

// PeerStat is what the device knows about one peer: counters since the device
// came up, the last handshake and the address the last packet came from.
type PeerStat struct {
	RxBytes       int64
	TxBytes       int64
	LastHandshake int64  // unix seconds, 0 = never
	Endpoint      string // ip:port, "" = never
}

// ParseStats reads an IpcGet dump into per-peer stats keyed by the peer's
// base64 public key.
func ParseStats(dump string) map[string]PeerStat {
	out := map[string]PeerStat{}
	var cur string
	var st PeerStat
	flush := func() {
		if cur != "" {
			out[cur] = st
		}
	}
	for _, line := range strings.Split(dump, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "public_key":
			flush()
			st = PeerStat{}
			cur = ""
			if raw, err := hex.DecodeString(v); err == nil && len(raw) == 32 {
				cur = base64.StdEncoding.EncodeToString(raw)
			}
		case "rx_bytes":
			st.RxBytes, _ = strconv.ParseInt(v, 10, 64)
		case "tx_bytes":
			st.TxBytes, _ = strconv.ParseInt(v, 10, 64)
		case "last_handshake_time_sec":
			st.LastHandshake, _ = strconv.ParseInt(v, 10, 64)
		case "endpoint":
			st.Endpoint = v
		}
	}
	flush()
	return out
}

// EndpointIP is the address half of a peer's endpoint, or "".
func EndpointIP(endpoint string) string {
	ap, err := netip.ParseAddrPort(endpoint)
	if err != nil {
		return ""
	}
	return ap.Addr().Unmap().String()
}

// Device is a running tunnel. The Linux implementation drives amneziawg-go over
// a TUN; elsewhere every method reports ErrUnsupported so the panel still builds
// and runs (and hands out configs) on a developer's machine.
type Device interface {
	// Apply brings the tunnel to cfg: starts it if needed, restarts it if the key
	// or port changed, otherwise replaces the peer list in place.
	Apply(cfg Config) error
	// Stats reads every peer's counters.
	Stats() (map[string]PeerStat, error)
	// Running reports whether the tunnel is up.
	Running() bool
	// LastError is what the last Apply failed with, "" when it succeeded.
	LastError() string
	// Close tears the tunnel down.
	Close()
}

// ErrUnsupported is what the stub device answers on a platform without TUN
// support in this build.
var ErrUnsupported = errors.New("awg: not supported on this platform")
