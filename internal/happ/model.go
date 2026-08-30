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
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	URL               string `json:"url"`
	Enabled           bool   `json:"enabled"`
	UpdateIntervalMin int    `json:"update_interval_min"` // 0 = manual only; default 59
	LastFetchAt       int64  `json:"last_fetch_at"`
	LastSuccessAt     int64  `json:"last_success_at"`
	LastError         string `json:"last_error,omitempty"`
	NodeCount         int    `json:"node_count"`
	CreatedAt         int64  `json:"created_at"`
}

// Node is one parsed proxy endpoint from a Subscription.
// It is shown in the Servers section and, when enabled, registered as an
// Xray outbound with tag "happ-<id>".
type Node struct {
	ID             int64  `json:"id"`
	SubscriptionID int64  `json:"subscription_id"`
	IdentityKey    string `json:"identity_key"` // SHA256-based dedup key
	Name           string `json:"name"`         // from URI fragment (#Name)
	Protocol       string `json:"protocol"`     // vless | vmess | trojan | ss | hysteria2
	Host           string `json:"host"`
	Port           int    `json:"port"`
	Enabled        bool   `json:"enabled"`
	URI            string `json:"uri"` // raw proxy URI for Xray outbound generation
	LastSeenAt     int64  `json:"last_seen_at"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
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
