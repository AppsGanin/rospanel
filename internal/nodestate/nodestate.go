// Package nodestate is a node's desired state held in parts (see nodeapi/split.go): what
// the agent keeps of a split state, how it takes a change, and how it puts the Xray
// config and meta back together. Kept apart from the agent so the panel's tests can run
// a node's side of the exchange against the panel's.
package nodestate

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// Held is a split state as the node holds and persists it. Skeleton, Rows and
// Blocked are the bytes the panel sent, which is what Hash is taken over.
type Held struct {
	Tag      string            `json:"tag"`
	Hash     string            `json:"hash"`
	Skeleton json.RawMessage   `json:"skeleton"`
	Rows     []json.RawMessage `json:"rows"` // ascending by user id
	Blocked  json.RawMessage   `json:"blocked"`
}

// Parts is a held split state decoded, kept beside it so a change does not decode
// every row again.
type Parts struct {
	Held     *Held
	Skeleton nodeapi.Skeleton
	Rows     []nodeapi.UserRow // Held.Rows decoded, in the same order
	Blocked  nodeapi.Blocked
}

// Decode decodes a held split state, checking the rows are in order.
func Decode(h *Held) (*Parts, error) {
	p := &Parts{Held: h, Rows: make([]nodeapi.UserRow, len(h.Rows))}
	if err := json.Unmarshal(h.Skeleton, &p.Skeleton); err != nil {
		return nil, fmt.Errorf("skeleton: %w", err)
	}
	if err := json.Unmarshal(h.Blocked, &p.Blocked); err != nil {
		return nil, fmt.Errorf("blocked addresses: %w", err)
	}
	for i, raw := range h.Rows {
		if err := json.Unmarshal(raw, &p.Rows[i]); err != nil {
			return nil, fmt.Errorf("row %d: %w", i, err)
		}
		if i > 0 && p.Rows[i-1].ID >= p.Rows[i].ID {
			return nil, fmt.Errorf("rows out of order at user %d", p.Rows[i].ID)
		}
	}
	return p, nil
}

// FromSplit is a split state as sent, with the hash the node computes over it — which must
// be the one the panel sent: a node that hashes its content differently from the panel
// would be sent it whole on every sync, so such a state is refused rather than held.
func FromSplit(s *nodeapi.SplitState) (*Held, error) {
	h := &Held{Tag: s.Tag, Skeleton: s.Skeleton, Rows: s.Rows, Blocked: s.Blocked}
	h.Hash = nodeapi.ContentHash(h.Skeleton, h.Rows, h.Blocked)
	if h.Hash != s.Hash {
		return nil, fmt.Errorf("content hash %s, the panel's is %s", Short(h.Hash), Short(s.Hash))
	}
	return h, nil
}

// Apply is the split state a change leaves: the rows it upserts in place of or
// beside the held ones, without the users it removes, the blocked addresses it carries
// if any, and the tag it names.
func Apply(p *Parts, d *nodeapi.StateDelta) (*Parts, error) {
	if d.From != p.Held.Tag {
		return nil, fmt.Errorf("a change from state %q, the node holds %q", Short(d.From), Short(p.Held.Tag))
	}
	upserts := make([]nodeapi.UserRow, len(d.Upsert))
	for i, raw := range d.Upsert {
		if err := json.Unmarshal(raw, &upserts[i]); err != nil {
			return nil, fmt.Errorf("changed row %d: %w", i, err)
		}
		if i > 0 && upserts[i-1].ID >= upserts[i].ID {
			return nil, fmt.Errorf("changed rows out of order at user %d", upserts[i].ID)
		}
	}
	removed := slices.Clone(d.Remove)
	slices.Sort(removed)
	for _, u := range upserts {
		if _, both := slices.BinarySearch(removed, u.ID); both {
			return nil, fmt.Errorf("user %d both changed and removed", u.ID)
		}
	}
	next := &Parts{
		Held: &Held{
			Tag:      d.Tag,
			Skeleton: p.Held.Skeleton,
			Rows:     make([]json.RawMessage, 0, len(p.Held.Rows)+len(upserts)),
			Blocked:  p.Held.Blocked,
		},
		Skeleton: p.Skeleton,
		Rows:     make([]nodeapi.UserRow, 0, len(p.Rows)+len(upserts)),
		Blocked:  p.Blocked,
	}
	add := func(row nodeapi.UserRow, raw json.RawMessage) {
		next.Rows = append(next.Rows, row)
		next.Held.Rows = append(next.Held.Rows, raw)
	}
	i, j := 0, 0
	for i < len(p.Rows) || j < len(upserts) {
		switch {
		case j == len(upserts) || i < len(p.Rows) && p.Rows[i].ID < upserts[j].ID:
			if _, gone := slices.BinarySearch(removed, p.Rows[i].ID); !gone {
				add(p.Rows[i], p.Held.Rows[i])
			}
			i++
		case i == len(p.Rows) || upserts[j].ID < p.Rows[i].ID:
			add(upserts[j], d.Upsert[j])
			j++
		default: // the same user: the change replaces the held row
			add(upserts[j], d.Upsert[j])
			i++
			j++
		}
	}
	if d.Blocked != nil {
		// Decoded into a fresh value: into the held one, a list the change leaves out
		// (every block lifted) would keep the old addresses.
		var blocked nodeapi.Blocked
		if err := json.Unmarshal(d.Blocked, &blocked); err != nil {
			return nil, fmt.Errorf("blocked addresses: %w", err)
		}
		next.Blocked, next.Held.Blocked = blocked, d.Blocked
	}
	next.Held.Hash = nodeapi.ContentHash(next.Held.Skeleton, next.Held.Rows, next.Held.Blocked)
	return next, nil
}

// Assemble puts a split state together into the state the rest of the agent applies:
// the Xray config with its users written back, and the meta with the speed caps, the
// tunnel's peers and the blocked addresses from the rows and the blocks.
func Assemble(p *Parts) (*nodeapi.NodeState, error) {
	cfg, err := xray.RenderUsers(p.Skeleton.Config, p.Skeleton.Slots, p.Rows)
	if err != nil {
		return nil, err
	}
	meta := p.Skeleton.Meta
	var tunnel *nodeapi.AWGState
	if meta.AWG != nil {
		t := *meta.AWG
		t.Peers = nil
		tunnel = &t
	}
	for _, r := range p.Rows {
		email := model.UserEmail(r.ID)
		if r.Speed > 0 {
			if meta.SpeedLimits == nil {
				meta.SpeedLimits = map[string]int{}
			}
			meta.SpeedLimits[email] = r.Speed
		}
		if tunnel != nil && r.AWGKey != "" {
			tunnel.Peers = append(tunnel.Peers, nodeapi.AWGPeer{PublicKey: r.AWGKey, Addr: r.AWGAddr, Email: email})
		}
	}
	meta.AWG = tunnel
	meta.BlockedIPs, meta.BlockTTLHours = p.Blocked.IPs, p.Blocked.TTLHours
	return &nodeapi.NodeState{Hash: p.Held.Hash, XrayConfig: cfg, Meta: meta}, nil
}

// Short is a hash or tag cut to the length logs show.
func Short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
