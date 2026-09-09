// Package warp registers a free Cloudflare WARP account and returns the
// WireGuard parameters Xray needs to use it as an outbound. The flow mirrors
// wgcf: generate a Curve25519 keypair, POST the public key to Cloudflare's
// device-registration API, and read back the assigned addresses + client id.
package warp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/curve25519"
)

// regURL is Cloudflare's device-registration endpoint (the v0a2158 client API).
const regURL = "https://api.cloudflareclient.com/v0a2158/reg"

// Well-known fallbacks if the API omits them (it usually returns its own).
const (
	defaultPeerPublicKey = "bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo="

	// peerHost is the hostname registration hands back as the peer address, and
	// anycastV4 is the address it resolves to — Cloudflare's well-known WARP anycast
	// entry point. The panel dials the address, never the hostname: see EndpointAddr.
	peerHost  = "engage.cloudflareclient.com"
	anycastV4 = "162.159.192.1"
	altV4     = "162.159.195.1"
	peerPort  = "2408"

	defaultEndpoint = anycastV4 + ":" + peerPort
)

// EndpointAddr returns ep with a literal IP for a host, so that reaching the WARP
// peer takes no name resolution at all.
//
// Xray resolves a WireGuard endpoint through its OWN dns block, which carries the
// resolvers the operator configured. When those are slow or filtered — a box inside
// Russia querying a DoH resolver the censor drops — the lookup fails and Xray logs
// "failed to set endpoint engage.cloudflareclient.com:2408: app/dns: record not
// found" on every handshake attempt: the WARP lane never comes up while every other
// lane looks healthy, and nothing in the panel says why. Cloudflare's hostname is a
// fixed anycast address, so writing the address skips the lookup entirely.
//
// A host that is already an IP is kept as it is, and so is an unrecognised hostname
// — the panel has no business inventing an address for a peer it doesn't know.
func EndpointAddr(ep string) string {
	ep = strings.TrimSpace(ep)
	if ep == "" {
		return defaultEndpoint
	}
	host, port, err := net.SplitHostPort(ep)
	if err != nil {
		host, port = ep, peerPort // no port at all, or a bare IPv6
	}
	if port == "" || port == "0" {
		port = peerPort
	}
	switch {
	case net.ParseIP(host) != nil:
		return net.JoinHostPort(host, port)
	case strings.EqualFold(host, peerHost):
		return net.JoinHostPort(anycastV4, port)
	}
	return ep
}

// Pool returns the peer addresses the WARP lane spreads over, the account's own
// endpoint first.
//
// A single endpoint is a single point of failure that no amount of retrying fixes:
// a provider can drop a whole Cloudflare range or one UDP port, and then the lane
// stops carrying traffic while the rest of the server looks healthy. So the members
// differ in BOTH dimensions — another range and another port each.
//
// Measured from a Dutch box on 2026-09-09 with a real account: 162.159.192.0/24 and
// 162.159.195.0/24 answered a WireGuard handshake on ports 2408, 4500, 500 and 1701,
// while 162.159.193.0/24 answered on nothing at all. 193.0/24 is therefore left out
// rather than carried as a member that can never come up, retrying its handshake and
// filling the log forever.
//
// The members share the one WARP account. Cloudflare accepts the same key from
// several endpoints at once — verified live, two members carried traffic
// simultaneously through different egress addresses — and Xray's health-probed
// balancer keeps the lane on whichever ones it can actually reach.
func Pool(ep string) []string {
	pool := []string{EndpointAddr(ep)}
	for _, alt := range []string{
		net.JoinHostPort(altV4, "4500"),
		net.JoinHostPort(anycastV4, "500"),
	} {
		if alt != pool[0] {
			pool = append(pool, alt)
		}
	}
	return pool
}

// peerEndpoint picks the peer address out of a registration response, preferring
// the IPv4 anycast address over the hostname. Cloudflare returns the address with
// port 0 and the real port only on the hostname, so the two are combined.
func peerEndpoint(v4, host string) string {
	port := peerPort
	if _, p, err := net.SplitHostPort(host); err == nil && p != "" && p != "0" {
		port = p
	}
	if ip, p, err := net.SplitHostPort(strings.TrimSpace(v4)); err == nil && net.ParseIP(ip) != nil {
		if p == "" || p == "0" {
			p = port
		}
		return net.JoinHostPort(ip, p)
	}
	if ip := strings.TrimSpace(v4); net.ParseIP(ip) != nil {
		return net.JoinHostPort(ip, port)
	}
	return host // may be empty — EndpointAddr then falls back to the anycast default
}

// Account holds everything needed to build a WireGuard outbound to WARP.
type Account struct {
	PrivateKey    string // our WG secret key (base64), used as Xray secretKey
	PeerPublicKey string // Cloudflare's WG public key (base64)
	Endpoint      string // host:port of the WARP peer
	AddressV4     string // assigned interface IPv4 (no mask)
	AddressV6     string // assigned interface IPv6 (no mask)
	Reserved      []int  // 3-byte client id, Xray's "reserved"
}

// Register provisions a new WARP account and returns its WireGuard parameters.
func Register(ctx context.Context) (*Account, error) {
	priv, pub, err := genKeypair()
	if err != nil {
		return nil, err
	}

	body, _ := json.Marshal(map[string]string{
		"key":        pub,
		"install_id": "",
		"fcm_token":  "",
		"tos":        time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		"model":      "PC",
		"type":       "Android",
		"locale":     "en_US",
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, regURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	req.Header.Set("User-Agent", "okhttp/3.12.1")
	req.Header.Set("CF-Client-Version", "a-6.30-3596")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("warp registration request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("warp registration HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var rr struct {
		Config struct {
			ClientID  string `json:"client_id"`
			Interface struct {
				Addresses struct {
					V4 string `json:"v4"`
					V6 string `json:"v6"`
				} `json:"addresses"`
			} `json:"interface"`
			Peers []struct {
				PublicKey string `json:"public_key"`
				Endpoint  struct {
					Host string `json:"host"`
					V4   string `json:"v4"`
					V6   string `json:"v6"`
				} `json:"endpoint"`
			} `json:"peers"`
		} `json:"config"`
	}
	if err := json.Unmarshal(raw, &rr); err != nil {
		return nil, fmt.Errorf("warp registration: bad response: %w", err)
	}

	acc := &Account{
		PrivateKey:    priv,
		PeerPublicKey: defaultPeerPublicKey,
		Endpoint:      defaultEndpoint,
		AddressV4:     rr.Config.Interface.Addresses.V4,
		AddressV6:     rr.Config.Interface.Addresses.V6,
	}
	if len(rr.Config.Peers) > 0 {
		p := rr.Config.Peers[0]
		if k := p.PublicKey; k != "" {
			acc.PeerPublicKey = k
		}
		acc.Endpoint = EndpointAddr(peerEndpoint(p.Endpoint.V4, p.Endpoint.Host))
	}
	if acc.AddressV4 == "" {
		return nil, fmt.Errorf("warp registration: no IPv4 assigned")
	}

	// reserved = the first 3 bytes of the base64-decoded client id.
	if cid, err := base64.StdEncoding.DecodeString(rr.Config.ClientID); err == nil && len(cid) >= 3 {
		acc.Reserved = []int{int(cid[0]), int(cid[1]), int(cid[2])}
	}
	return acc, nil
}

// genKeypair returns a clamped Curve25519 private key and its public key, both
// base64-encoded the way WireGuard/Cloudflare expect.
func genKeypair() (priv, pub string, err error) {
	var sk [32]byte
	if _, err = rand.Read(sk[:]); err != nil {
		return "", "", err
	}
	sk[0] &= 248
	sk[31] &= 127
	sk[31] |= 64

	pk, err := curve25519.X25519(sk[:], curve25519.Basepoint)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(sk[:]), base64.StdEncoding.EncodeToString(pk), nil
}
