package model

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// External subscriptions: servers that belong to somebody else, read from their
// subscription and handed on to this panel's users as extra entries — a partner's
// fleet, a second provider used as a spare, an old panel during a migration. The
// panel owns none of them: no credential of ours is on them, no traffic of theirs
// is counted here. What it owns is the list — which of them a user gets, decided by
// the same access groups that gate its own lanes.
//
// The same sources (a URL, a happ:// link, a pasted list) are also accepted as the
// upstreams of an egress lane, where the servers carry traffic OUT of this server
// rather than being handed to users; that path is internal/proxypool and needs no
// row here.

// ExtSyncInterval is how often an enabled subscription is re-read.
const ExtSyncInterval = time.Hour

// ExtSubscription is one source and what the last reading of it found.
type ExtSubscription struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// Source is an http(s) URL, fetched on every sync, or the payload itself
	// (a happ://crypt… link, a base64 blob, a list of links) decoded in place.
	Source string `json:"source"`
	// Identity is what this panel presents when it fetches that source. Every field
	// is an override of a default, so an untouched subscription behaves exactly as it
	// did before the fields existed.
	Identity    ExtIdentity `json:"identity"`
	Enabled     bool        `json:"enabled"`
	LastFetchAt int64       `json:"last_fetch_at"`
	LastOKAt    int64       `json:"last_ok_at"`
	LastError   string      `json:"last_error,omitempty"`
	ServerCount int         `json:"server_count"`
	CreatedAt   int64       `json:"created_at"`
}

// ExtIdentity is the device this panel claims to be when it reads somebody else's
// subscription. Panels increasingly refuse a caller that does not identify a device —
// ours does — so the fetch has to answer that question, and the answer has to be
// stable across syncs or every refresh burns another device slot upstream.
//
// Every field is OPTIONAL and every empty one falls back to a default the panel
// derives (see extsub.Headers). The fields exist for the case the defaults are not
// what the other side expects: an id the operator was given, an app name a panel
// whitelists, a client string that decides which format it serves. All of them, rather
// than a chosen few, because a partially editable identity is an arbitrary hole to
// explain later.
type ExtIdentity struct {
	HWID        string `json:"hwid"`
	DeviceOS    string `json:"device_os"`
	OSVersion   string `json:"os_version"`
	DeviceModel string `json:"device_model"`
	UserAgent   string `json:"user_agent"`
}

// ExtServer is one server a subscription listed. Key is the server's identity
// across syncs (see extsub.Endpoint.Key), which is what lets the operator's on/off
// choice survive a re-read; Link is handed to users as is and is encrypted at
// rest, since it carries somebody else's credential.
type ExtServer struct {
	ID       int64  `json:"id"`
	SubID    int64  `json:"sub_id"`
	Key      string `json:"-"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Link     string `json:"-"`
	Enabled  bool   `json:"enabled"`
	SeenAt   int64  `json:"seen_at"`
}

// ExtToken is a group-grant token for an external server.
func ExtToken(serverID int64) string { return fmt.Sprintf("ext:%d", serverID) }

// ParseExtToken returns the external server id a token refers to, or ok=false.
func ParseExtToken(token string) (int64, bool) {
	rest, ok := strings.CutPrefix(token, "ext:")
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	return id, err == nil
}

// AllowsExt reports whether the user may be handed an external server.
func (a Access) AllowsExt(serverID int64) bool {
	return a.All || a.Tokens[ExtToken(serverID)]
}

// MaxExtSubscriptionName bounds the label; it is a heading in the UI, nothing more.
const MaxExtSubscriptionName = 64

// CleanExtSubscriptionName trims and bounds a subscription's name; empty is
// allowed (the UI falls back to the source's host).
func CleanExtSubscriptionName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if len([]rune(name)) > MaxExtSubscriptionName {
		return "", fieldErr("err.extNameLong", "название слишком длинное")
	}
	if strings.ContainsAny(name, "\n\r\t") {
		return "", fieldErr("err.extNameCharset", "название не может содержать переводы строк")
	}
	return name, nil
}
