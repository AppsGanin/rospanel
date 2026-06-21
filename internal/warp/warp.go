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
	defaultEndpoint      = "engage.cloudflareclient.com:2408"
)

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
		if k := rr.Config.Peers[0].PublicKey; k != "" {
			acc.PeerPublicKey = k
		}
		if h := rr.Config.Peers[0].Endpoint.Host; h != "" {
			acc.Endpoint = h
		}
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
