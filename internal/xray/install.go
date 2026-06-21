package xray

import (
	"archive/zip"
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

// PinnedVersion is the Xray-core release auto-installed when no binary is found.
// Keep in sync with deploy/install-xray.sh and the Dockerfile ARG.
//
// Why pinned: Xray 26.3.27 shipped a broken Hysteria2 server-side auth handshake;
// 26.6.1 fixes it. A floating "latest" can silently regress, so we pin.
const PinnedVersion = "v26.6.1"

// pinnedSHA256 is the SHA-256 of each platform's Xray release zip for
// PinnedVersion, taken from XTLS's published <asset>.dgst files. The downloaded
// archive is rejected if it doesn't match — this defends against a corrupted,
// truncated, or substituted binary before it is extracted and run as root. Update
// these together with PinnedVersion.
var pinnedSHA256 = map[string]string{
	"Xray-linux-64.zip":        "136e822e99e616692550723e8b607cd8858c62a390aea5704938bc27930904ba",
	"Xray-linux-arm64-v8a.zip": "5c0f397b2605a853e5b308ae6485ad2ec395ca11ed7ae0fd023c480c7fdb279d",
	"Xray-macos-64.zip":        "2636b49efed01ed4fae07d3988241819d89d880c46a353f6526a2e5195c14d06",
	"Xray-macos-arm64-v8a.zip": "b0c13d8215eea03929c773056dce21e903fe6e7543348f46bb42184190fb5e50",
}

// releaseAsset returns the XTLS release zip name for the current platform.
func releaseAsset() (string, error) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64":
		return "Xray-linux-64.zip", nil
	case "linux/arm64":
		return "Xray-linux-arm64-v8a.zip", nil
	case "darwin/amd64":
		return "Xray-macos-64.zip", nil
	case "darwin/arm64":
		return "Xray-macos-arm64-v8a.zip", nil
	default:
		return "", fmt.Errorf("no prebuilt Xray for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
}

// EnsureBinary returns the path to an Xray binary in dir, downloading the pinned
// release there if one isn't already present. It lets a fresh box come up without
// a separate install step when no system Xray (XRAY_BIN / PATH) is found.
func EnsureBinary(dir string) (string, error) {
	dest := filepath.Join(dir, "xray")
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
	url := fmt.Sprintf("https://github.com/XTLS/Xray-core/releases/download/%s/%s", PinnedVersion, asset)

	// Download the release zip to a temp file (archive/zip needs random access).
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	tmpZip, err := os.CreateTemp(dir, "xray-*.zip")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmpZip.Name())
	n, err := io.Copy(tmpZip, resp.Body)
	tmpZip.Close()
	if err != nil {
		return "", err
	}
	if resp.ContentLength >= 0 && n != resp.ContentLength {
		return "", fmt.Errorf("download truncated: got %d of %d bytes", n, resp.ContentLength)
	}

	// Integrity gate: the archive must match the pinned SHA-256 before we extract
	// and run it as root. Refuse otherwise.
	want, ok := pinnedSHA256[asset]
	if !ok {
		return "", fmt.Errorf("no pinned checksum for %s", asset)
	}
	sum, err := sha256File(tmpZip.Name())
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(sum, want) {
		return "", fmt.Errorf("xray %s checksum mismatch (got %s, want %s) — refusing to install", asset, sum, want)
	}

	// Extract just the "xray" entry, then move it into place with an executable bit.
	zr, err := zip.OpenReader(tmpZip.Name())
	if err != nil {
		return "", err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if filepath.Base(f.Name) != "xray" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		tmp := dest + ".new"
		out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			rc.Close()
			return "", err
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			os.Remove(tmp)
			return "", err
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
	return "", fmt.Errorf("no xray binary inside %s", asset)
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
