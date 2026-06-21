// Package geo manages the geoip.dat / geosite.dat databases Xray needs to
// resolve geosite:/geoip: routing rules. They live in an asset dir pointed at by
// XRAY_LOCATION_ASSET.
package geo

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Mirrors for the rule databases, tried in order (GitHub raw is sometimes
// DPI-degraded in RU, so jsDelivr is a fallback).
var sources = map[string][]string{
	"geoip.dat": {
		"https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geoip.dat",
		"https://cdn.jsdelivr.net/gh/Loyalsoldier/v2ray-rules-dat@release/geoip.dat",
	},
	"geosite.dat": {
		"https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geosite.dat",
		"https://cdn.jsdelivr.net/gh/Loyalsoldier/v2ray-rules-dat@release/geosite.dat",
	},
}

// dbNames lists the geo databases in a stable display order.
var dbNames = []string{"geoip.dat", "geosite.dat"}

// FileInfo is the on-disk state of one geo database (for the settings UI).
type FileInfo struct {
	Name       string `json:"name"`
	Present    bool   `json:"present"`
	Size       int64  `json:"size"`
	ModifiedAt int64  `json:"modified_at"` // unix seconds ≈ last successful download
}

// Status returns metadata for the geo databases in dir. The file mtime doubles
// as the "last downloaded" timestamp (download writes atomically via rename).
func Status(dir string) []FileInfo {
	out := make([]FileInfo, 0, len(dbNames))
	for _, name := range dbNames {
		fi := FileInfo{Name: name}
		if st, err := os.Stat(filepath.Join(dir, name)); err == nil && !st.IsDir() {
			fi.Present = true
			fi.Size = st.Size()
			fi.ModifiedAt = st.ModTime().Unix()
		}
		out = append(out, fi)
	}
	return out
}

// Refresh re-downloads every database into dir regardless of whether it already
// exists, pulling the latest published version. It attempts all files and returns
// the first error encountered.
func Refresh(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var firstErr error
	for _, name := range dbNames {
		if err := download(name, sources[name], filepath.Join(dir, name)); err != nil {
			log.Printf("geo: refresh %s failed: %v", name, err)
			if firstErr == nil {
				firstErr = err
			}
		} else {
			log.Printf("geo: refreshed %s", name)
		}
	}
	return firstErr
}

// Ensure downloads any missing database into dir (best-effort). Existing files
// are left as-is; refresh is handled separately.
func Ensure(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for name, urls := range sources {
		path := filepath.Join(dir, name)
		if fi, err := os.Stat(path); err == nil && fi.Size() > 0 {
			continue
		}
		if err := download(name, urls, path); err != nil {
			log.Printf("geo: could not fetch %s: %v (geosite/geoip rules need it — retry later)", name, err)
		} else {
			log.Printf("geo: downloaded %s", name)
		}
	}
	return nil
}

// Categories parses the category names out of geosite.dat and geoip.dat in dir.
// Both files are v2ray's GeoSiteList/GeoIPList protobuf: a flat sequence of
// length-delimited entries (field 1), each whose first field (1) is the
// uppercase category code (string). We only need that code, so a tiny reader
// avoids pulling in the full xray proto. Codes are returned lowercased + sorted.
func Categories(dir string) (geosite, geoip []string, err error) {
	geosite = parseCategoryFile(filepath.Join(dir, "geosite.dat"))
	geoip = parseCategoryFile(filepath.Join(dir, "geoip.dat"))
	if len(geosite) == 0 && len(geoip) == 0 {
		return nil, nil, fmt.Errorf("no geo databases found in %s", dir)
	}
	return geosite, geoip, nil
}

func parseCategoryFile(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	seen := make(map[string]struct{})
	for len(data) > 0 {
		tag, n := binary.Uvarint(data)
		if n <= 0 || tag != 0x0A { // expect field 1, wire type 2 (length-delimited)
			break
		}
		data = data[n:]
		msgLen, n := binary.Uvarint(data)
		if n <= 0 || int(msgLen) > len(data[n:]) {
			break
		}
		data = data[n:]
		if code := firstStringField(data[:msgLen]); code != "" {
			seen[strings.ToLower(code)] = struct{}{}
		}
		data = data[msgLen:]
	}
	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// firstStringField reads field 1 (the country_code string) from an entry.
func firstStringField(msg []byte) string {
	tag, n := binary.Uvarint(msg)
	if n <= 0 || tag != 0x0A {
		return ""
	}
	msg = msg[n:]
	l, n := binary.Uvarint(msg)
	if n <= 0 || int(l) > len(msg[n:]) {
		return ""
	}
	return string(msg[n : n+int(l)])
}

func download(name string, urls []string, dest string) error {
	var lastErr error
	client := &http.Client{Timeout: 90 * time.Second}
	for _, url := range urls {
		if err := downloadVerified(client, url, dest); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return lastErr
}

// downloadVerified fetches url to dest atomically, but only after verifying its
// bytes against the SHA-256 published in the companion "<url>.sha256sum" file from
// the SAME source. The geo databases are loaded by root-run Xray, so a tampered
// mirror/CDN or a corrupt/truncated transfer must be rejected rather than written.
// (Trust model, like the self-updater: the source serves both the file and its
// checksum over TLS, so this stops mirror/transit tampering and corruption; it is
// not a defence against the upstream repo itself being compromised.)
func downloadVerified(client *http.Client, url, dest string) error {
	want, err := fetchSHA256(client, url+".sha256sum")
	if err != nil {
		return fmt.Errorf("%s: checksum unavailable: %w", url, err)
	}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	tmp := dest + ".new"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), resp.Body)
	f.Close()
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, want) {
		os.Remove(tmp)
		return fmt.Errorf("%s: checksum mismatch (got %s, want %s) — refusing", url, got, want)
	}
	return os.Rename(tmp, dest)
}

// fetchSHA256 reads a "<hex>  filename" sha256sum file and returns the hex digest.
func fetchSHA256(client *http.Client, url string) (string, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 || len(fields[0]) != 64 {
		return "", fmt.Errorf("malformed sha256sum")
	}
	return fields[0], nil
}
