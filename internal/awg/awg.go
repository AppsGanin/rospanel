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
	"encoding/json"
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

// Range is a closed interval the protocol samples from — per packet for a header,
// per timer for a timing. The zero Range means "not set": the engine keeps its own
// default, and nothing is written to the client config.
//
// It marshals as a string ("110-130", or just "110" when the ends meet) and
// unmarshals from either that or a bare number, which is what every parameter row
// written before 3.1 holds for H1–H4.
type Range struct{ Min, Max uint32 }

func (r Range) IsZero() bool { return r == Range{} }

func (r Range) String() string {
	switch {
	case r.IsZero():
		return ""
	case r.Min == r.Max:
		return strconv.FormatUint(uint64(r.Min), 10)
	}
	return fmt.Sprintf("%d-%d", r.Min, r.Max)
}

// MarshalJSON writes a real range as a string and a single value as a bare
// number — which is the shape every parameter block written before 3.1 already
// had, in the database and on the wire to a node. Emitting the string form for
// those too would have been tidier and would have broken every node still running
// an agent that reads h1–h4 as numbers: its decode of the WHOLE sync response
// fails, so the node stops syncing at all rather than merely losing its tunnel.
func (r Range) MarshalJSON() ([]byte, error) {
	if r.Min == r.Max {
		return json.Marshal(r.Min)
	}
	return json.Marshal(r.String())
}

func (r *Range) UnmarshalJSON(b []byte) error {
	var n uint32
	if err := json.Unmarshal(b, &n); err == nil {
		*r = Range{n, n}
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("awg: range is neither a number nor a string: %w", err)
	}
	return r.parse(s)
}

func (r *Range) parse(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		*r = Range{}
		return nil
	}
	lo, hi, split := strings.Cut(s, "-")
	min, err := strconv.ParseUint(strings.TrimSpace(lo), 10, 32)
	if err != nil {
		return fmt.Errorf("awg: range %q: %w", s, err)
	}
	max := min
	if split {
		max, err = strconv.ParseUint(strings.TrimSpace(hi), 10, 32)
		if err != nil {
			return fmt.Errorf("awg: range %q: %w", s, err)
		}
	}
	if max < min {
		return fmt.Errorf("awg: range %q runs backwards", s)
	}
	*r = Range{uint32(min), uint32(max)}
	return nil
}

// Overlaps reports whether two ranges share a value — which for the four header
// bands would let two message types be mistaken for each other.
func (r Range) Overlaps(o Range) bool { return r.Min <= o.Max && o.Min <= r.Max }

// Params are the AmneziaWG obfuscation parameters. Both ends must agree on every
// value, which is why they live in the server's row and go out in every client
// config.
//
// The first five fields are the whole of AmneziaWG 1.5 and are what every row
// written before 3.1 holds: how many junk packets precede the handshake and how
// big they are (Jc, Jmin, Jmax), and how much random padding the two handshake
// messages carry (S1, S2). Everything after them is 3.1 and optional — a zero
// value is simply not written, so a 1.5 row still renders the 1.5 configuration
// it always did and the engine runs it unchanged.
type Params struct {
	Jc   int `json:"jc"`
	Jmin int `json:"jmin"`
	Jmax int `json:"jmax"`
	S1   int `json:"s1"`
	S2   int `json:"s2"`

	// S3/S4 pad the cookie and transport messages, which 1.5 left at their
	// telltale fixed sizes. [3.1]
	S3 int `json:"s3,omitempty"`
	S4 int `json:"s4,omitempty"`

	// H1–H4 replace WireGuard's message types 1/2/3/4. In 1.5 each was one
	// number; in 3.1 each is a band the sender picks from per packet, so the type
	// is not even constant across a session.
	H1 Range `json:"h1"`
	H2 Range `json:"h2"`
	H3 Range `json:"h3"`
	H4 Range `json:"h4"`

	// HeaderKey (base64, 32 bytes) encrypts the parts of the packet header that
	// stay in the clear under 1.5 — the static fingerprint a DPI box matched on.
	// This is the headline of 3.1 and the reason the rest is worth having. [3.1]
	HeaderKey string `json:"header_key,omitempty"`

	// Padding is how many extra bytes ride on a transport packet, so the length
	// series of a session stops tracking the plaintext's. [3.1]
	Padding Range `json:"padding,omitzero"`

	// Trailers appends random bytes after the payload. [3.1]
	Trailers bool `json:"trailers,omitempty"`

	// I1/I2 are the decoy datagrams sent ahead of a handshake, in the engine's
	// tag language — see imitate.go. Empty means "send none", which is what a
	// block written before 3.1 has and what a client must see rather than an
	// empty value: the upstream tools crash on I-parameters that are present but
	// blank. [3.1]
	I1 string `json:"i1,omitempty"`
	I2 string `json:"i2,omitempty"`

	// Imitation names the profile I1/I2 came from ("dns", "quic", "stun"). It is
	// the panel's own note, never written to the config or the engine: what a
	// server pretends to be is a thing the operator should be able to read off a
	// screen without decoding a hex chain, and printing the chains themselves
	// where the other parameters are read as empty values. [3.1]
	Imitation string `json:"imitation,omitempty"`

	// The timers, in seconds. WireGuard's are constants, and a constant is a
	// clock a classifier can lock onto; these spread each one over a band. Zero
	// leaves the protocol default, which is what a 1.5 row gets. [3.1]
	RekeyAfter   Range `json:"rekey_after,omitzero"`
	RekeyTimeout Range `json:"rekey_timeout,omitzero"`
	RejectAfter  Range `json:"reject_after,omitzero"`
	Keepalive    Range `json:"keepalive,omitzero"`
	Handshakes   Range `json:"handshakes,omitzero"`
}

// IsZero reports an unset parameter block (a server that has never had AWG on).
func (p Params) IsZero() bool { return p == Params{} }

// NeedsAgent31 reports whether this block can only be carried by a node agent
// that understands AmneziaWG 3.1. It is true as soon as anything 3.1 is set —
// including a header written as a band rather than a single value, which is the
// field an older agent would fail to decode.
func (p Params) NeedsAgent31() bool {
	if p.S3 != 0 || p.S4 != 0 || p.HeaderKey != "" || p.Trailers || p.I1 != "" || p.I2 != "" ||
		!p.Padding.IsZero() || !p.RekeyAfter.IsZero() || !p.RekeyTimeout.IsZero() ||
		!p.RejectAfter.IsZero() || !p.Keepalive.IsZero() || !p.Handshakes.IsZero() {
		return true
	}
	for _, h := range []Range{p.H1, p.H2, p.H3, p.H4} {
		if h.Min != h.Max {
			return true
		}
	}
	return false
}

// The padded sizes of WireGuard's three fixed-length messages. A handshake
// initiation is 148 bytes, a response 92 and a cookie reply 64; S1/S2/S3 add to
// those. Two of them coming out the same length would hand a classifier the
// distinction the padding exists to remove, so the generator and Validate both
// keep them apart — and these constants are why the differences are 56, 84 and 28.
const (
	sizeInit     = 148
	sizeResponse = 92
	sizeCookie   = 64
)

// RandomParams picks a parameter set inside the ranges the AmneziaWG authors
// recommend for 3.1: a handful of junk packets ahead of the handshake, padding on
// all four message types, four header bands well clear of WireGuard's own 1–4 and
// of each other, a header-protection key, transport padding and trailers, and the
// timers spread rather than fixed.
func RandomParams() Params {
	p := Params{
		Jc:       randInt(3, 8),
		Jmin:     50,
		Jmax:     1000,
		S1:       randInt(30, 120),
		S2:       randInt(30, 120),
		S3:       randInt(30, 120),
		S4:       randInt(12, 30),
		Padding:  Range{0, 32},
		Trailers: true,
		// Amnezia's own recommended spreads, in seconds. Each brackets the
		// WireGuard constant it replaces (120, 5, 180, 10 and 20 attempts).
		RekeyAfter:   Range{110, 130},
		RekeyTimeout: Range{4, 6},
		RejectAfter:  Range{175, 195},
		Keepalive:    Range{12, 18},
		Handshakes:   Range{10, 15},
	}
	for sizeInit+p.S1 == sizeResponse+p.S2 {
		p.S2 = randInt(30, 120)
	}
	for sizeInit+p.S1 == sizeCookie+p.S3 || sizeResponse+p.S2 == sizeCookie+p.S3 {
		p.S3 = randInt(30, 120)
	}

	// Four bands, one per message type, each in its own quarter of the 32-bit
	// space so they cannot overlap however wide the spread is drawn.
	spread := uint32(randInt(50_000, 200_000))
	band := func(lo, hi int) Range {
		start := uint32(randInt(lo, hi))
		return Range{start, start + spread}
	}
	p.H1 = band(100_000_000, 900_000_000)
	p.H2 = band(1_000_000_000, 1_900_000_000)
	p.H3 = band(2_000_000_000, 2_900_000_000)
	p.H4 = band(3_000_000_000, 3_900_000_000)

	key := make([]byte, headerKeyLen)
	if _, err := rand.Read(key); err == nil {
		p.HeaderKey = base64.StdEncoding.EncodeToString(key)
	}

	im := randomImitation()
	p.I1, p.I2, p.Imitation = im.I1, im.I2, im.Name
	return p
}

// headerKeyLen is the ChaCha20 key the header protection uses — the same 32 bytes
// as every other key here, so keyHex validates it too.
const headerKeyLen = 32

// Validate refuses parameter sets amneziawg-go would refuse or that break the
// obfuscation: the same rules RandomParams follows, stated as bounds. A 1.5 block
// still passes — every 3.1 field is optional and only checked once it is set.
func (p Params) Validate() error {
	switch {
	case p.Jc < 0 || p.Jc > 128:
		return errors.New("awg: jc must be 0–128")
	case p.Jmin < 0 || p.Jmax < 0 || p.Jmin > p.Jmax || p.Jmax > 1280:
		return errors.New("awg: jmin ≤ jmax ≤ 1280")
	case p.S1 < 0 || p.S1 > 1132 || p.S2 < 0 || p.S2 > 1188:
		return errors.New("awg: s1 ≤ 1132, s2 ≤ 1188")
	case p.S3 < 0 || p.S3 > 1216 || p.S4 < 0 || p.S4 > 65535:
		return errors.New("awg: s3 ≤ 1216, s4 ≤ 65535")
	case sizeInit+p.S1 == sizeResponse+p.S2:
		return errors.New("awg: padded init and response must differ in length (s2 ≠ s1 + 56)")
	case p.S3 > 0 && sizeInit+p.S1 == sizeCookie+p.S3:
		return errors.New("awg: padded init and cookie must differ in length (s3 ≠ s1 + 84)")
	case p.S3 > 0 && sizeResponse+p.S2 == sizeCookie+p.S3:
		return errors.New("awg: padded response and cookie must differ in length (s3 ≠ s2 + 28)")
	}
	headers := []Range{p.H1, p.H2, p.H3, p.H4}
	for i, h := range headers {
		if h.Min < 5 {
			return fmt.Errorf("awg: h%d must be ≥ 5, clear of WireGuard's own 1–4", i+1)
		}
		for j := i + 1; j < len(headers); j++ {
			if h.Overlaps(headers[j]) {
				return fmt.Errorf("awg: h%d and h%d overlap — two message types could be read as one", i+1, j+1)
			}
		}
	}
	if p.HeaderKey != "" {
		if _, err := keyBytes(p.HeaderKey); err != nil {
			return fmt.Errorf("awg: header key: %w", err)
		}
	}
	for i, chain := range []string{p.I1, p.I2} {
		if err := validateChain(chain); err != nil {
			return fmt.Errorf("i%d: %w", i+1, err)
		}
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
	fmt.Fprintf(&b, "jc=%d\njmin=%d\njmax=%d\ns1=%d\ns2=%d\n", p.Jc, p.Jmin, p.Jmax, p.S1, p.S2)
	fmt.Fprintf(&b, "h1=%s\nh2=%s\nh3=%s\nh4=%s\n", p.H1, p.H2, p.H3, p.H4)
	// Everything below is 3.1 and optional: a parameter block written before it
	// leaves these zero, and an unwritten key is the engine's own default — which
	// is exactly the 1.5 behaviour such a block was generated for.
	if p.S3 > 0 {
		fmt.Fprintf(&b, "s3=%d\n", p.S3)
	}
	if p.S4 > 0 {
		fmt.Fprintf(&b, "s4=%d\n", p.S4)
	}
	if p.HeaderKey != "" {
		// The UAPI takes this key in hex, while the client config carries it in
		// base64 — the same split WireGuard already makes for the private key.
		hk, err := keyHex(p.HeaderKey)
		if err != nil {
			return "", fmt.Errorf("awg: header key: %w", err)
		}
		fmt.Fprintf(&b, "header_protection_key=%s\n", hk)
	}
	// Written only when set: an I-parameter that is present but empty is what
	// crashes the upstream tools, so "no imitation" must be silence, not a blank.
	if p.I1 != "" {
		fmt.Fprintf(&b, "i1=%s\n", p.I1)
	}
	if p.I2 != "" {
		fmt.Fprintf(&b, "i2=%s\n", p.I2)
	}
	if !p.Padding.IsZero() {
		fmt.Fprintf(&b, "content_padding_addition=%s\n", p.Padding)
	}
	if p.Trailers {
		b.WriteString("random_trailers=true\n")
	}
	for _, t := range []struct {
		key string
		val Range
	}{
		{"rekey_after_time", p.RekeyAfter},
		{"rekey_timeout", p.RekeyTimeout},
		{"reject_after_time", p.RejectAfter},
		{"keepalive_timeout", p.Keepalive},
		{"max_handshake_attempts", p.Handshakes},
	} {
		if !t.val.IsZero() {
			fmt.Fprintf(&b, "%s=%s\n", t.key, t.val)
		}
	}
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
	// The order is Amnezia's own: the junk counts, then the four paddings, then the
	// four headers, then the rest. An INI parser should not care, but this is a file
	// people compare against the examples in the docs by eye, and S3/S4 sitting
	// apart from S1/S2 reads as a mistake even when it is not one.
	fmt.Fprintf(&b, "Jc = %d\nJmin = %d\nJmax = %d\nS1 = %d\nS2 = %d\n", p.Jc, p.Jmin, p.Jmax, p.S1, p.S2)
	// The 3.1 half, written only when it is set. An older client reading a 1.5
	// block sees exactly the file it has always seen; the keys below are the ones
	// amneziawg-tools names, so a 3.1 client reads them as the engine does.
	if p.S3 > 0 {
		fmt.Fprintf(&b, "S3 = %d\n", p.S3)
	}
	if p.S4 > 0 {
		fmt.Fprintf(&b, "S4 = %d\n", p.S4)
	}
	fmt.Fprintf(&b, "H1 = %s\nH2 = %s\nH3 = %s\nH4 = %s\n", p.H1, p.H2, p.H3, p.H4)
	if p.I1 != "" {
		fmt.Fprintf(&b, "I1 = %s\n", p.I1)
	}
	if p.I2 != "" {
		fmt.Fprintf(&b, "I2 = %s\n", p.I2)
	}
	if p.HeaderKey != "" {
		fmt.Fprintf(&b, "HeaderProtectionKey = %s\n", p.HeaderKey)
	}
	if !p.Padding.IsZero() {
		fmt.Fprintf(&b, "ContentPaddingAddition = %s\n", p.Padding)
	}
	if p.Trailers {
		// The INI parser spells its booleans on/off, not true/false.
		b.WriteString("RandomTrailers = on\n")
	}
	for _, t := range []struct {
		key string
		val Range
	}{
		{"RekeyAfterTime", p.RekeyAfter},
		{"RekeyTimeout", p.RekeyTimeout},
		{"RejectAfterTime", p.RejectAfter},
		{"KeepaliveTimeout", p.Keepalive},
		{"MaxHandshakeAttempts", p.Handshakes},
	} {
		if !t.val.IsZero() {
			fmt.Fprintf(&b, "%s = %s\n", t.key, t.val)
		}
	}
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
