package nodeapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// A node's desired state is mostly its users: with fifty thousand of them the Xray
// config is ten megabytes, nearly all of it client entries, and every user created,
// expired or capped used to send every node all ten again. An agent that says it
// speaks DeltaRev is sent its state in three parts instead —
//
//   - the skeleton: the Xray config with every user list empty, what each list is made
//     of, and the host-level meta without its per-user parts;
//   - one row per user the node lets in, with what the lists need of them;
//   - the addresses the source policy has blocked and an operator has banned —
//
// and once it has them, a change sends only the rows that changed (StateDelta). The
// agent writes the users back into the skeleton itself (xray.RenderUsers).
//
// Both sides fingerprint what the node holds the same way (ContentHash), so the panel
// can tell a node that already has the right state from one that does not after a
// restart, and every so often checks that the changes a node was sent added up to what
// it should have.

// DeltaRev is the revision of the split state this build speaks. An agent sends it in
// SyncRequest.DeltaRev, and a panel sends split states and deltas only to an agent of
// its own revision — anything else gets the whole config, as before. Raise it whenever
// UserSlot, UserRow, Blocked or how they are rendered or hashed change meaning.
//
// 2: WireGuard inbounds (a "wireguard" slot, and WGKey/WGAddr on the row), and the
// addresses banned by hand in Blocked.Banned.
const DeltaRev = 2

// SplitState is a node's whole desired state in parts. Skeleton, every row and Blocked
// are sent as the panel encoded them, and the agent keeps them as received: they are
// what ContentHash is taken over, on both sides.
type SplitState struct {
	// Tag names the version of the panel's inputs this state was built from. The agent
	// reports it (SyncRequest.StateTag) so the panel knows which changes it still lacks.
	// Opaque to the agent.
	Tag string `json:"tag"`
	// Hash is ContentHash over the parts below.
	Hash     string            `json:"hash"`
	Skeleton json.RawMessage   `json:"skeleton"`
	Rows     []json.RawMessage `json:"rows"` // UserRow, ascending by user id
	Blocked  json.RawMessage   `json:"blocked"`
}

// StateDelta brings a node from the state tagged From to the one tagged Tag. Rows in
// Upsert replace the node's row for that user or add one; users in Remove are no longer
// let in. Blocked, when present, replaces the node's blocked addresses; absent, they
// stay as they are. A delta with neither rows nor Blocked only renames the node's state:
// what it has is already what it should have.
type StateDelta struct {
	From    string            `json:"from"`
	Tag     string            `json:"tag"`
	Upsert  []json.RawMessage `json:"upsert,omitempty"` // UserRow
	Remove  []int64           `json:"remove,omitempty"`
	Blocked json.RawMessage   `json:"blocked,omitempty"`
}

// Skeleton is everything in a node's state that is not a user.
type Skeleton struct {
	// Config is the Xray config with the users field of every inbound in Slots empty.
	// Cert paths are the sentinels, as in NodeState.XrayConfig.
	Config json.RawMessage `json:"config"`
	Slots  []UserSlot      `json:"slots"`
	// Meta has no SpeedLimits, no AWG peers and no blocked addresses: those come from
	// the rows and from Blocked.
	Meta NodeMeta `json:"meta"`
}

// UserSlot is one user list in the skeleton's config: which inbound holds it and how a
// user's entry in it is written.
type UserSlot struct {
	Inbound int    `json:"inbound"` // index into the config's inbounds
	Kind    string `json:"kind"`    // the inbound's Xray protocol: vless, trojan, hysteria, shadowsocks, wireguard
	Flow    string `json:"flow,omitempty"`
	Method  string `json:"method,omitempty"` // shadowsocks
	// Locked is the entry a Shadowsocks list holds when nobody may use the inbound —
	// never an empty list, which Xray would read as a single-user server open to its
	// shared key (see xray.customShadowsocksClients).
	Locked json.RawMessage `json:"locked,omitempty"`
}

// UserRow is one user a node lets in.
type UserRow struct {
	ID       int64  `json:"id"`
	UUID     string `json:"uuid,omitempty"`
	Password string `json:"password,omitempty"`
	// Slots are the indexes into Skeleton.Slots of every list the user is in.
	Slots []int `json:"slots,omitempty"`
	// Speed is the user's cap on this node in kbit/s, 0 for none.
	Speed int `json:"speed,omitempty"`
	// AWGKey and AWGAddr make the user a peer of the node's AmneziaWG tunnel.
	AWGKey  string `json:"awg_key,omitempty"`
	AWGAddr string `json:"awg_addr,omitempty"`
	// WGKey and WGAddr are the same identity for the WireGuard inbounds in Slots. Kept
	// apart from the AWG pair: a user may be allowed on one lane and not the other.
	WGKey  string `json:"wg_key,omitempty"`
	WGAddr string `json:"wg_addr,omitempty"`
}

// Blocked is what a node drops at its own firewall: the source policy's refusals, which
// expire, and the addresses an operator banned, which last until lifted.
type Blocked struct {
	IPs      []string `json:"ips,omitempty"`
	TTLHours int      `json:"ttl_hours,omitempty"`
	Banned   []string `json:"banned,omitempty"`
}

// ContentHash fingerprints a split state: the skeleton, every row in ascending user
// order, and the blocked addresses, each exactly as encoded. Two states hash alike when
// they would put the same config, the same users and the same blocks on a node.
func ContentHash(skeleton json.RawMessage, rows []json.RawMessage, blocked json.RawMessage) string {
	part := func(b []byte) []byte {
		sum := sha256.Sum256(b)
		return sum[:]
	}
	users := sha256.New()
	for _, r := range rows {
		_, _ = users.Write(r)
		_, _ = users.Write([]byte{'\n'})
	}
	h := sha256.New()
	_, _ = h.Write([]byte("rospanel split state 1\n"))
	_, _ = h.Write(part(skeleton))
	_, _ = h.Write(users.Sum(nil))
	_, _ = h.Write(part(blocked))
	return hex.EncodeToString(h.Sum(nil))
}
