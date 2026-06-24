// Package netguard provides SSRF-safe outbound HTTP helpers.
package netguard

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultFetchTimeout = 15 * time.Second

// ValidateFetchURL ensures url is an https URL whose host does not resolve to
// private/link-local/metadata addresses.
func ValidateFetchURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("пустой URL")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("неверный URL: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("разрешён только https")
	}
	if u.User != nil {
		return fmt.Errorf("учётные данные в URL не допускаются")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("не указан хост")
	}
	return rejectPrivateHost(host)
}

func rejectPrivateHost(host string) error {
	if ip := net.ParseIP(host); ip != nil {
		return rejectPrivateIP(ip)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("не удалось разрешить хост: %w", err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("хост не разрешается")
	}
	for _, ia := range ips {
		if err := rejectPrivateIP(ia.IP); err != nil {
			return err
		}
	}
	return nil
}

func rejectPrivateIP(ip net.IP) error {
	ip = ip.To16()
	if ip == nil {
		return fmt.Errorf("неверный IP")
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return fmt.Errorf("запрещённый адрес: %s", ip)
	}
	// AWS/GCP/Azure metadata endpoints.
	if ip.Equal(net.ParseIP("169.254.169.254")) {
		return fmt.Errorf("запрещённый адрес: metadata")
	}
	return nil
}

// dialValidated connects only to public IPs, re-checking the resolved address at
// dial time to block DNS rebinding between validation and the actual request.
func dialValidated(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	var targets []string
	if ip := net.ParseIP(host); ip != nil {
		if err := rejectPrivateIP(ip); err != nil {
			return nil, err
		}
		targets = []string{net.JoinHostPort(ip.String(), port)}
	} else {
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("не удалось разрешить хост: %w", err)
		}
		for _, ia := range ips {
			if err := rejectPrivateIP(ia.IP); err != nil {
				continue
			}
			targets = append(targets, net.JoinHostPort(ia.IP.String(), port))
		}
		if len(targets) == 0 {
			return nil, fmt.Errorf("запрещённый адрес")
		}
	}
	var d net.Dialer
	var lastErr error
	for _, target := range targets {
		conn, err := d.DialContext(ctx, network, target)
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("не удалось подключиться")
}

func safeTransport() *http.Transport {
	return &http.Transport{DialContext: dialValidated}
}

// Client returns an http.Client with timeout and redirect blocking to private nets.
func Client(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = defaultFetchTimeout
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: safeTransport(),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if err := ValidateFetchURL(req.URL.String()); err != nil {
				return fmt.Errorf("redirect blocked: %w", err)
			}
			if len(via) >= 3 {
				return fmt.Errorf("слишком много перенаправлений")
			}
			return nil
		},
	}
}

// Get performs a bounded GET after SSRF validation.
func Get(ctx context.Context, rawURL string, maxBody int64) ([]byte, error) {
	if err := ValidateFetchURL(rawURL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	client := Client(0)
	if deadline, ok := ctx.Deadline(); ok {
		client.Timeout = time.Until(deadline)
		if client.Timeout <= 0 {
			client.Timeout = time.Millisecond
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if maxBody <= 0 {
		maxBody = 1 << 20
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBody))
}
