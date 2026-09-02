package xray

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// PinnedVersion is the Xray-core release the panel installs and keeps its managed
// binary on. Keep in sync with the Dockerfile ARG.
//
// Why pinned: Xray 26.3.27 shipped a broken Hysteria2 server-side auth handshake
// (26.6.x fixes it). A floating "latest" can silently regress, so we pin an exact
// release and its checksums.
const PinnedVersion = "v26.7.28"

// VersionMatchesPinned reports whether a reported Xray version is the pinned
// release, tolerating a leading "v": PinnedVersion carries it, but `xray version`
// output does not (Supervisor.Version parses "26.7.28"), so a plain string compare
// would spuriously flag every node as version-skewed.
func VersionMatchesPinned(v string) bool {
	return strings.TrimPrefix(v, "v") == strings.TrimPrefix(PinnedVersion, "v")
}

// pinnedSHA256 is the SHA-256 of each platform's Xray release zip for
// PinnedVersion, taken from XTLS's published <asset>.dgst files. The downloaded
// archive is rejected if it doesn't match — this defends against a corrupted,
// truncated, or substituted binary before it is extracted and run as root. Update
// these together with PinnedVersion.
var pinnedSHA256 = map[string]string{
	"Xray-linux-64.zip":        "8195d909f1109b8f3d99eefe401a3c451d7bf4af71f24d3815420f77e5dd2a40",
	"Xray-linux-arm64-v8a.zip": "f5698bb218ada3b4022db26fafc39601c5f53b46b19eb76c9616325985807501",
	"Xray-macos-64.zip":        "812f7d9de6d3506795eabda2f6928ba301c632c3fe6fa39c52ea8e0ed9e4e244",
	"Xray-macos-arm64-v8a.zip": "9b99a351febe31b7e0c7f22deeb1577a1da0b98aaa51aec7fd17832e68cf63d6",
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
// release there if one isn't already present — or if the one present is a different
// release. It lets a fresh box come up without a separate install step when no
// system Xray (XRAY_BIN / PATH) is found, and lets a panel update carry the Xray
// update with it: without the second half, a box installed once would sit on
// whatever release it first downloaded, however far the pin moved on.
//
// Only the binary this function manages is ever replaced. An operator who points
// XRAY_BIN at their own build, or has one on PATH, never reaches here.
func EnsureBinary(dir string) (string, error) {
	dest := filepath.Join(dir, "xray")
	have := ""
	if fi, err := os.Stat(dest); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
		v := binaryVersion(dest)
		if VersionMatchesPinned(v) {
			return dest, nil // already the pinned release
		}
		slog.Info("xray: installed release differs from the pinned one — upgrading",
			"have", v, "want", PinnedVersion, "path", dest)
		have = dest
	}
	asset, err := releaseAsset()
	if err != nil {
		return have, orKeep(have, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return have, orKeep(have, err)
	}
	url := fmt.Sprintf("https://github.com/XTLS/Xray-core/releases/download/%s/%s", PinnedVersion, asset)

	// Download the release zip to a temp file (archive/zip needs random access).
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return have, orKeep(have, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return have, orKeep(have, fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode))
	}
	tmpZip, err := os.CreateTemp(dir, "xray-*.zip")
	if err != nil {
		return have, orKeep(have, err)
	}
	defer os.Remove(tmpZip.Name())
	n, err := io.Copy(tmpZip, resp.Body)
	tmpZip.Close()
	if err != nil {
		return have, orKeep(have, err)
	}
	if resp.ContentLength >= 0 && n != resp.ContentLength {
		return have, orKeep(have, fmt.Errorf("download truncated: got %d of %d bytes", n, resp.ContentLength))
	}

	// Integrity gate: the archive must match the pinned SHA-256 before we extract
	// and run it as root. Refuse otherwise.
	want, ok := pinnedSHA256[asset]
	if !ok {
		return have, orKeep(have, fmt.Errorf("no pinned checksum for %s", asset))
	}
	sum, err := sha256File(tmpZip.Name())
	if err != nil {
		return have, orKeep(have, err)
	}
	if !strings.EqualFold(sum, want) {
		return have, orKeep(have, fmt.Errorf("xray %s checksum mismatch (got %s, want %s) — refusing to install", asset, sum, want))
	}

	// Extract just the "xray" entry, then move it into place with an executable bit.
	zr, err := zip.OpenReader(tmpZip.Name())
	if err != nil {
		return have, orKeep(have, err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if filepath.Base(f.Name) != "xray" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return have, orKeep(have, err)
		}
		tmp := dest + ".new"
		out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			rc.Close()
			return have, orKeep(have, err)
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			os.Remove(tmp)
			return have, orKeep(have, err)
		}
		if err := os.Chmod(tmp, 0o755); err != nil {
			os.Remove(tmp)
			return have, orKeep(have, err)
		}
		if err := os.Rename(tmp, dest); err != nil {
			return have, orKeep(have, err)
		}
		return dest, nil
	}
	return have, orKeep(have, fmt.Errorf("no xray binary inside %s", asset))
}

// orKeep swallows a download failure when a usable binary is already installed:
// an upgrade that can't reach GitHub is a reason to stay on the release that works,
// not to take the panel down. With nothing installed the error stands, and the
// caller (which cannot run without Xray) fails loudly.
func orKeep(have string, err error) error {
	if have == "" {
		return err
	}
	slog.Warn("xray: staying on the installed release — could not fetch the pinned one",
		"want", PinnedVersion, "path", have, "err", err)
	return nil
}

// binaryVersion reports the version of an Xray binary ("26.7.28"), or "" when it
// won't run or won't say. `xray version` prints "Xray 26.7.28 (Xray, ...)" and
// exits, so this is cheap; the timeout is there for a binary that hangs instead.
func binaryVersion(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").Output()
	if err != nil {
		return ""
	}
	line := strings.SplitN(string(out), "\n", 2)[0]
	if f := strings.Fields(line); len(f) >= 2 {
		return f[1]
	}
	return ""
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
