// Package nodeagent runs the panel-managed node: it holds an outbound long-poll to
// the panel, applies the Xray config the panel pushes, and reports traffic back. It
// reuses the same building blocks as the panel (xray.Supervisor, tlsmgr, hop,
// connguard, decoy), so a node is the same binary in a different mode — no DB, just
// a small identity + state file under its data dir.
package nodeagent

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/version"
)

// Identity is the node's persisted credential + where to reach the panel. Stored
// 0600 as node.json in the data dir. Token is the permanent bearer credential.
type Identity struct {
	PanelURL string `json:"panel_url"` // scheme://host of the panel
	NodeAPI  string `json:"node_api"`  // the panel's node-API path segment
	NodeID   int64  `json:"node_id"`
	Token    string `json:"token"`
}

func identityPath(dataDir string) string { return filepath.Join(dataDir, "node.json") }

// LoadIdentity reads the node's identity, or an error if the node hasn't joined.
func LoadIdentity(dataDir string) (*Identity, error) {
	b, err := os.ReadFile(identityPath(dataDir))
	if err != nil {
		return nil, fmt.Errorf("node not joined (no node.json in %s): %w", dataDir, err)
	}
	var id Identity
	if err := json.Unmarshal(b, &id); err != nil {
		return nil, fmt.Errorf("corrupt node.json: %w", err)
	}
	if id.Token == "" || id.PanelURL == "" {
		return nil, fmt.Errorf("node.json missing panel URL or token")
	}
	return &id, nil
}

// Save writes the identity atomically with 0600 perms.
func (id *Identity) Save(dataDir string) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		return err
	}
	tmp := identityPath(dataDir) + ".new"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, identityPath(dataDir))
}

// syncURL / joinURL build the endpoint for an action from the identity.
func (id *Identity) syncURL() string { return id.endpoint("sync") }
func (id *Identity) endpoint(action string) string {
	return strings.TrimRight(id.PanelURL, "/") + "/" + id.NodeAPI + "/" + nodeapi.PathPrefix + "/" + action
}

// Join exchanges a one-time join URL (…/<node_api>/v1/join#<join_token>) for a
// permanent identity and persists it. insecure allows a self-signed panel cert
// (the panel usually has a real cert; this is an escape hatch for edge setups).
func Join(dataDir, joinURL string, insecure bool) (*Identity, error) {
	base, token, err := splitJoinURL(joinURL)
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(nodeapi.JoinRequest{JoinToken: token, NodeVersion: version.Version})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, base, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := joinClient(insecure).Do(req)
	if err != nil {
		return nil, fmt.Errorf("reach panel: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("panel rejected join (HTTP %d) — the join token may be wrong or expired", resp.StatusCode)
	}
	var jr nodeapi.JoinResponse
	if err := json.NewDecoder(resp.Body).Decode(&jr); err != nil {
		return nil, fmt.Errorf("decode join response (is the URL correct?): %w", err)
	}
	if jr.Token == "" {
		return nil, fmt.Errorf("panel returned an empty token")
	}
	panelURL := jr.PanelURL
	if panelURL == "" {
		panelURL = baseOrigin(base) // fall back to the origin we joined against
	}
	nodeAPI := jr.NodeAPI
	if nodeAPI == "" {
		nodeAPI = pathSegment(base) // …/<seg>/v1/join → <seg>
	}
	id := &Identity{PanelURL: panelURL, NodeAPI: nodeAPI, NodeID: jr.NodeID, Token: jr.Token}
	if err := id.Save(dataDir); err != nil {
		return nil, fmt.Errorf("save node.json: %w", err)
	}
	return id, nil
}

// splitJoinURL splits …/join#<token> into (base, token). The token lives in the
// fragment so it never lands in an HTTP access log if the URL is mistyped.
func splitJoinURL(raw string) (base, token string, err error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", "", fmt.Errorf("invalid join URL: %w", err)
	}
	token = u.Fragment
	if token == "" {
		return "", "", fmt.Errorf("join URL has no token (expected …/join#<token>)")
	}
	u.Fragment = ""
	if u.Scheme == "" || u.Host == "" {
		return "", "", fmt.Errorf("join URL must be absolute (https://panel/…/join#token)")
	}
	// Refuse plaintext http to a non-loopback panel: the token would cross the wire
	// unencrypted. Loopback http is allowed for local dev.
	if u.Scheme == "http" && !isLoopbackHost(u.Hostname()) {
		return "", "", fmt.Errorf("refusing http join to a non-loopback panel — use https")
	}
	return u.String(), token, nil
}

func baseOrigin(base string) string {
	if u, err := url.Parse(base); err == nil {
		return u.Scheme + "://" + u.Host
	}
	return base
}

// pathSegment returns the first path segment of a URL (…/<seg>/v1/join → seg).
func pathSegment(base string) string {
	u, err := url.Parse(base)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.TrimLeft(u.Path, "/"), "/")
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}

func isLoopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// joinClient is a short-lived HTTP client for the one join request.
func joinClient(insecure bool) *http.Client {
	tr := &http.Transport{}
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opt-in escape hatch
	}
	return &http.Client{Timeout: 30 * time.Second, Transport: tr}
}
