// Package happ implements Happ subscription fetching, decryption, parsing, and
// Xray outbound generation. It bridges external Happ proxy subscriptions into
// the panel's server list, where enabled nodes are used as Xray egress outbounds.
//
// Supported subscription formats:
//   - Plain-text URI list (one vless://, vmess://, trojan://, ss://, hysteria2:// per line)
//   - Base64-encoded URI list (standard or URL-safe base64)
//   - happ://crypt through happ://crypt5 deep links (RSA PKCS1v15 / RSA+ChaCha20-Poly1305)
package happ

import (
	"crypto/sha256"
	"fmt"
	"time"
)

// Subscription is a managed Happ subscription source.
// One subscription maps to many HappNodes (parsed proxy endpoints).
type Subscription struct {
	ID                int64
	Name              string
	URL               string
	Enabled           bool
	UpdateIntervalMin int // 0 = manual only; default 59
	LastFetchAt       int64
	LastSuccessAt     int64
	LastError         string
	NodeCount         int
	CreatedAt         int64
}

// Node is one parsed proxy endpoint from a Subscription.
// It is shown in the Servers section and, when enabled, registered as an
// Xray outbound with tag "happ-<id>".
type Node struct {
	ID             int64
	SubscriptionID int64
	IdentityKey    string // SHA256-based dedup key
	Name           string // from URI fragment (#Name)
	Protocol       string // vless | vmess | trojan | ss | hysteria2
	Host           string
	Port           int
	Enabled        bool
	URI            string // raw proxy URI for Xray outbound generation
	LastSeenAt     int64
	CreatedAt      int64
	UpdatedAt      int64
}

// XrayTag returns the Xray outbound tag for this node.
func (n *Node) XrayTag() string {
	return fmt.Sprintf("happ-%d", n.ID)
}

// IdentityKeyFor computes the deterministic deduplication key for a proxy
// endpoint identified by subscriptionID, protocol, host, port, and userinfo.
// The same endpoint across syncs produces the same key — enabling upsert semantics.
func IdentityKeyFor(subscriptionID int64, protocol, host string, port int, userinfo string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%d\x00%s\x00%s\x00%d\x00%s", subscriptionID, protocol, host, port, userinfo)
	return fmt.Sprintf("%x", h.Sum(nil))
}

// SyncResult summarises one subscription sync operation.
type SyncResult struct {
	SubscriptionID int64
	Added          int
	Updated        int
	Removed        int
	Total          int
	Error          error
	At             time.Time
}
