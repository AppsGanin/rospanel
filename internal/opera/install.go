// Package opera manages the opera-proxy helper binary: a standalone Opera VPN
// client that exposes a local HTTP proxy. The panel runs it under a supervisor
// and adds it as the "opera" Xray routing lane (parallel to Cloudflare WARP).
package opera

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// PinnedVersion is the opera-proxy release auto-downloaded when no binary is
// present. Pinned (not "latest") so a release can't silently change behaviour.
const PinnedVersion = "v1.25.0"

// pinnedSHA256 is the SHA-256 of each platform's opera-proxy asset for
// PinnedVersion. The downloaded binary is rejected if it doesn't match — this
// defends against a corrupted, truncated, MITM-substituted, or repo/release-tampered
// binary before it is made executable and run as root (mirrors xray/install.go).
// Update these together with PinnedVersion.
var pinnedSHA256 = map[string]string{
	"opera-proxy.linux-amd64":   "2655296010c19309dc22ddca12059a507e32099d461df11db5e7e99c1c1f0691",
	"opera-proxy.linux-386":     "1461a5b8ef980b50641c1e97eb08ccc839cbc527c3188cd57316ef43e572e0de",
	"opera-proxy.android-arm64": "450ced891e981aa0bfc6debbe1c839a76c59dd589218c65f865a4fbde97c1044",
}

// releaseAsset returns the opera-proxy release asset name for the current
// platform. The project ships raw, unarchived ELF binaries named
// "opera-proxy.<os>-<arch>".
func releaseAsset() (string, error) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64":
		return "opera-proxy.linux-amd64", nil
	case "linux/386":
		return "opera-proxy.linux-386", nil
	case "linux/arm64":
		// No dedicated linux-arm64 asset; the android-arm64 build is an arm64 ELF
		// and runs on linux/arm64.
		return "opera-proxy.android-arm64", nil
	default:
		return "", fmt.Errorf("no prebuilt opera-proxy for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
}

// EnsureBinary returns the path to an opera-proxy binary in dir, downloading the
// pinned release there if one isn't already present and executable.
func EnsureBinary(dir string) (string, error) {
	dest := filepath.Join(dir, "opera-proxy")
	if fi, err := os.Stat(dest); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
		return dest, nil // already downloaded and executable
	}
	asset, err := releaseAsset()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	url := fmt.Sprintf(
		"https://github.com/Alexey71/opera-proxy/releases/download/%s/%s",
		PinnedVersion, asset,
	)

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}

	// Stream straight to a temp file, then atomically rename into place with the
	// executable bit set (the asset is a raw binary — no archive to unpack).
	tmp := dest + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return "", err
	}
	_, err = io.Copy(out, resp.Body)
	out.Close()
	if err != nil {
		os.Remove(tmp)
		return "", err
	}

	// Integrity gate: the binary must match the pinned SHA-256 before it is made
	// executable and run as root. Refuse otherwise.
	want, ok := pinnedSHA256[asset]
	if !ok {
		os.Remove(tmp)
		return "", fmt.Errorf("no pinned checksum for %s", asset)
	}
	sum, err := sha256File(tmp)
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	if !strings.EqualFold(sum, want) {
		os.Remove(tmp)
		return "", fmt.Errorf("opera-proxy %s checksum mismatch (got %s, want %s) — refusing to install", asset, sum, want)
	}

	if err := os.Chmod(tmp, 0o755); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// sha256File returns the hex SHA-256 of the file at path.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
