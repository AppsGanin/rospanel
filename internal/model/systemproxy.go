package model

import "strings"

// SystemProxyAccount is one login the system proxy accepts. Several exist so the
// operator can hand a separate credential to each consumer — a scraper, a bot, a
// colleague — and revoke exactly one of them later without breaking the rest.
type SystemProxyAccount struct {
	User string `json:"user"`
	Pass string `json:"pass"`
}

// SystemProxy is one server's forward-proxy configuration: a SOCKS5 and/or an HTTP
// listener that anything can be pointed at — a scraper, a bot, another RosPanel
// chaining its egress here — with traffic leaving under that server's routing.
//
// It is deliberately NOT part of the VPN surface: no VPN user's credential opens it,
// no access group gates it, and it never appears in a subscription. Its accounts are
// the operator's to hand out.
type SystemProxy struct {
	SocksEnabled bool `json:"socks_enabled"`
	SocksPort    int  `json:"socks_port"`
	HTTPEnabled  bool `json:"http_enabled"`
	HTTPPort     int  `json:"http_port"`
	// Accounts are write-mostly: reads return them to the panel (the operator needs
	// the strings to paste into whatever they are proxying) but never to a VPN client.
	Accounts []SystemProxyAccount `json:"accounts"`
}

// SystemProxyPortDefault is what a first-time enable suggests for each protocol.
// Both are above 1024 (no privileged bind) and away from the built-in lanes.
const (
	SystemProxySocksPortDefault = 1080
	SystemProxyHTTPPortDefault  = 3128
	// MaxSystemProxyAccounts bounds the list. Every account is written into the
	// generated Xray config on this server (and, for a node, shipped to it), so an
	// unbounded list is an unbounded config.
	MaxSystemProxyAccounts = 32
)

// Normalize trims the accounts, drops the blank rows an editor leaves behind, and
// fills in a default port for a protocol switched on without one — so "enable it" is
// a single click in the UI and a two-field body over the API.
func (p *SystemProxy) Normalize() {
	out := make([]SystemProxyAccount, 0, len(p.Accounts))
	for _, a := range p.Accounts {
		a.User = strings.TrimSpace(a.User)
		a.Pass = strings.TrimSpace(a.Pass)
		if a.User == "" && a.Pass == "" {
			continue // an empty row is a row the operator added and never filled in
		}
		out = append(out, a)
	}
	p.Accounts = out
	if p.SocksEnabled && p.SocksPort == 0 {
		p.SocksPort = SystemProxySocksPortDefault
	}
	if p.HTTPEnabled && p.HTTPPort == 0 {
		p.HTTPPort = SystemProxyHTTPPortDefault
	}
}

// Validate checks a configuration on its own terms (ports in range, at least one
// complete account, no duplicate logins, the two listeners not on one port).
// Collisions with the server's OTHER listeners are checked where those are known —
// see core.otherListenerPorts.
//
// An account is required whenever either listener is on, and there is no way to turn
// that off: an unauthenticated proxy on a public port is found by scanners in hours
// and is then someone else's spam relay, with this server's IP on it.
func (p SystemProxy) Validate() error {
	if !p.SocksEnabled && !p.HTTPEnabled {
		return nil // fully off — the rest doesn't matter
	}
	if len(p.Accounts) == 0 {
		return fieldErr("err.proxyNeedsAccount", "добавьте хотя бы одного пользователя прокси")
	}
	if len(p.Accounts) > MaxSystemProxyAccounts {
		return fieldErr("err.proxyTooManyAccounts", "не больше {{max}} пользователей прокси",
			map[string]any{"max": MaxSystemProxyAccounts})
	}
	// Each incomplete row is reported as ITSELF, naming the login. "needs at least one
	// user" for a row whose password is simply empty sent the operator looking for a
	// missing user while the real one sat on screen with a blank field.
	seen := make(map[string]bool, len(p.Accounts))
	for _, a := range p.Accounts {
		if a.User == "" {
			return fieldErr("err.proxyAccountNoUser", "у пользователя прокси не заполнен логин")
		}
		if a.Pass == "" {
			return fieldErr("err.proxyAccountNoPass", "у пользователя {{value}} не заполнен пароль",
				map[string]any{"value": a.User})
		}
		if strings.ContainsAny(a.User, ": ") {
			return fieldErr("err.proxyUserCharset", "логин прокси не может содержать пробел или двоеточие")
		}
		if seen[a.User] {
			return fieldErr("err.proxyUserDuplicate", "логин {{value}} повторяется", map[string]any{"value": a.User})
		}
		seen[a.User] = true
	}
	if p.SocksEnabled {
		if err := validProxyPort(p.SocksPort); err != nil {
			return err
		}
	}
	if p.HTTPEnabled {
		if err := validProxyPort(p.HTTPPort); err != nil {
			return err
		}
	}
	if p.SocksEnabled && p.HTTPEnabled && p.SocksPort == p.HTTPPort {
		return fieldErr("err.proxyPortsCollide", "порты SOCKS и HTTP должны отличаться")
	}
	return nil
}

func validProxyPort(port int) error {
	if port < 1 || port > 65535 {
		return fieldErr("err.portRange", "порт вне диапазона 1–65535")
	}
	return nil
}
