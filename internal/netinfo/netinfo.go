// Package netinfo resolves the server's own public IP, used for unattended
// first-boot host configuration and the post-factory-reset redirect.
package netinfo

import (
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// PublicIP best-effort resolves the server's public IP so a fresh install can
// obtain an IP certificate without the operator pre-setting a host. It asks a
// couple of external echo services, then falls back to the address of the primary
// outbound interface. Returns "" if nothing usable is found.
func PublicIP() string {
	client := &http.Client{Timeout: 5 * time.Second}
	for _, url := range []string{"https://api.ipify.org", "https://ifconfig.me/ip"} {
		resp, err := client.Get(url)
		if err != nil {
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
		resp.Body.Close()
		if err != nil {
			continue
		}
		if ip := strings.TrimSpace(string(body)); net.ParseIP(ip) != nil {
			return ip
		}
	}
	// Fallback: the local address chosen for an outbound connection (no packet is
	// actually sent for a UDP "dial").
	if conn, err := net.Dial("udp", "8.8.8.8:80"); err == nil {
		defer conn.Close()
		if ua, ok := conn.LocalAddr().(*net.UDPAddr); ok && !ua.IP.IsLoopback() {
			return ua.IP.String()
		}
	}
	return ""
}
