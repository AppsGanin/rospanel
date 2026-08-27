package model

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ShareLinkPrefix is the URL scheme prefix for encoded node share links.
const ShareLinkPrefix = "rpnshare://"

// NodeRentalSettings holds the owner-configured sharing and resource allocation parameters.
type NodeRentalSettings struct {
	ShareEnabled      bool   `json:"share_enabled"`
	ShareQuotaPercent int    `json:"share_quota_percent"` // 1-100% of node traffic/resources
	ShareSpeedLimit   int    `json:"share_speed_limit"`   // Total speed cap for all tenants in kbit/s (0 = unlimited)
	ShareToken        string `json:"share_token"`         // Secret token for authenticating tenant nodes
	MaxTenants        int    `json:"max_tenants"`         // Maximum allowed concurrent tenants (default 10, max 50)
}

// NodeTenant represents one active tenant renting/sharing this node.
type NodeTenant struct {
	ID          int64  `json:"id"`
	NodeID      int64  `json:"node_id"`
	TenantID    string `json:"tenant_id"` // Unique identifier of the tenant panel/user
	Name        string `json:"name"`      // Display name / label
	TrafficUp   int64  `json:"traffic_up"`
	TrafficDown int64  `json:"traffic_down"`
	SpeedLimit  int    `json:"speed_limit"` // Effective speed limit allocated to this tenant (kbit/s)
	LastSeen    int64  `json:"last_seen"`
	CreatedAt   int64  `json:"created_at"`
}

// NodeSharePayload is the data serialized and encrypted/encoded inside a share link.
type NodeSharePayload struct {
	Version       int      `json:"v"`
	NodeID        int64    `json:"nid"`
	Host          string   `json:"host"`
	NodePath      string   `json:"npath,omitempty"`
	Name          string   `json:"name"`
	ShareToken    string   `json:"token"`
	QuotaPercent  int      `json:"quota"`
	SpeedLimit    int      `json:"speed"`
	ReservedPorts []int    `json:"ports"`
	Protocols     []string `json:"protos"`
	NodeVersion   string   `json:"node_ver,omitempty"`
	XrayVersion   string   `json:"xray_ver,omitempty"`
	CPUPercent    float64  `json:"cpu,omitempty"`
	MemUsed       int64    `json:"mem_u,omitempty"`
	MemTotal      int64    `json:"mem_t,omitempty"`
	DiskUsed      int64    `json:"disk_u,omitempty"`
	DiskTotal     int64    `json:"disk_t,omitempty"`
	HostUptime    int64    `json:"uptime,omitempty"`
	Signature     string   `json:"sig,omitempty"`
	CreatedAt     int64    `json:"ts"`
}

// NodeRentalSyncReq is the payload sent by a tenant panel to sync with the owner panel.
type NodeRentalSyncReq struct {
	NodeID     int64     `json:"node_id"`
	ShareToken string    `json:"share_token"`
	TenantID   string    `json:"tenant_id"`
	TenantName string    `json:"tenant_name"`
	Inbounds   []Inbound `json:"inbounds"`
}

// NodeRentalSyncResp is the telemetry returned to a tenant by the owner panel.
type NodeRentalSyncResp struct {
	Online        bool    `json:"online"`
	NodeVersion   string  `json:"node_version"`
	XrayVersion   string  `json:"xray_version"`
	XrayRunning   bool    `json:"xray_running"`
	CPUPercent    float64 `json:"cpu_percent"`
	MemUsed       int64   `json:"mem_used"`
	MemTotal      int64   `json:"mem_total"`
	DiskUsed      int64   `json:"disk_used"`
	DiskTotal     int64   `json:"disk_total"`
	HostUptime    int64   `json:"host_uptime"`
	ReservedPorts []int   `json:"reserved_ports"`
}

// PortInfo describes an individual port usage on a node.
type PortInfo struct {
	Port     int    `json:"port"`
	Protocol string `json:"protocol"` // "tcp" or "udp"
	Service  string `json:"service"`  // "VLESS-Vision", "REALITY", "Hysteria2", "Inbound: <name>", "System"
	IsOwner  bool   `json:"is_owner"` // true if owned by master/node owner, false if tenant
	TenantID string `json:"tenant_id,omitempty"`
}

// CalculateTenantSpeed divides the total share speed limit evenly among active tenants.
func CalculateTenantSpeed(totalSpeedKbps int, activeTenants int) int {
	if totalSpeedKbps <= 0 {
		return 0
	}
	if activeTenants <= 1 {
		return totalSpeedKbps
	}
	perTenant := totalSpeedKbps / activeTenants
	if perTenant < 64 { // Minimum floor of 64 kbit/s if speed is constrained
		return 64
	}
	return perTenant
}

// CalculateTenantQuota divides the total share quota percent evenly among active tenants.
func CalculateTenantQuota(totalQuotaPercent int, activeTenants int) int {
	if totalQuotaPercent <= 0 {
		return 100
	}
	if activeTenants <= 1 {
		return min(max(totalQuotaPercent, 1), 100)
	}
	perTenant := totalQuotaPercent / activeTenants
	if perTenant < 1 {
		return 1
	}
	return perTenant
}

// ComputeShareSignature creates an HMAC-SHA256 signature for payload verification.
func ComputeShareSignature(nodeID int64, host, token string) string {
	mac := hmac.New(sha256.New, []byte(token))
	msg := fmt.Sprintf("%d:%s:%s", nodeID, strings.TrimSpace(host), token)
	mac.Write([]byte(msg))
	return hex.EncodeToString(mac.Sum(nil))[:32]
}

// EncodeShareLink packages and base64-encodes a NodeSharePayload into a share link string.
func EncodeShareLink(payload NodeSharePayload) (string, error) {
	if payload.Host == "" {
		return "", fieldErr("err.nodeHostRequired", "укажите домен или IP ноды")
	}
	if payload.ShareToken == "" {
		return "", fieldErr("err.invalidShareLink", "неверная или поврежденная ссылка шеринга ноды")
	}
	payload.Signature = ComputeShareSignature(payload.NodeID, payload.Host, payload.ShareToken)
	if payload.CreatedAt == 0 {
		payload.CreatedAt = time.Now().Unix()
	}
	if payload.Version == 0 {
		payload.Version = 1
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(data)
	return ShareLinkPrefix + encoded, nil
}

// DecodeShareLink parses and validates a base64-encoded share link.
func DecodeShareLink(link string) (*NodeSharePayload, error) {
	link = strings.TrimSpace(link)
	if link == "" {
		return nil, fieldErr("err.invalidShareLink", "неверная или поврежденная ссылка шеринга ноды")
	}
	raw := link
	if strings.HasPrefix(link, ShareLinkPrefix) {
		raw = strings.TrimPrefix(link, ShareLinkPrefix)
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		// Try standard base64 decoding if RawURLEncoding fails
		data, err = base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return nil, fieldErr("err.invalidShareLink", "неверная или поврежденная ссылка шеринга ноды")
		}
	}
	var payload NodeSharePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fieldErr("err.invalidShareLink", "неверная или поврежденная ссылка шеринга ноды")
	}
	if payload.Host == "" || payload.ShareToken == "" || payload.NodeID == 0 {
		return nil, fieldErr("err.invalidShareLink", "неверная или поврежденная ссылка шеринга ноды")
	}
	expectedSig := ComputeShareSignature(payload.NodeID, payload.Host, payload.ShareToken)
	if payload.Signature != "" && payload.Signature != expectedSig {
		return nil, fieldErr("err.invalidShareLink", "неверная подпись ссылки шеринга ноды")
	}
	return &payload, nil
}
