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

// source is one downloadable database: its mirrors (tried in order) and whether
// the bytes must be checksum-verified before being written.
type source struct {
	urls []string
	// verify demands a companion "<url>.sha256sum" from the same host and refuses
	// the download on mismatch. The iplist services publish no checksum, so their
	// lists are taken on trust — see downloadPlain.
	verify bool
}

// Mirrors for the rule databases, tried in order (GitHub raw is sometimes
// DPI-degraded in RU, so jsDelivr is a fallback).
var sources = map[string]source{
	"geoip.dat": {verify: true, urls: []string{
		"https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geoip.dat",
		"https://cdn.jsdelivr.net/gh/Loyalsoldier/v2ray-rules-dat@release/geoip.dat",
	}},
	"geosite.dat": {verify: true, urls: []string{
		"https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geosite.dat",
		"https://cdn.jsdelivr.net/gh/Loyalsoldier/v2ray-rules-dat@release/geosite.dat",
	}},
	// The iplist services (github.com/rekryt/iplist) group hosts by service —
	// "ai", "youtube", "russia", "vk" … — and resolve each to live addresses. We
	// fetch the unfiltered JSON on purpose: the "?data=" filter drops the "group"
	// field, and one request per host yields the group names, their domains and
	// their pre-aggregated CIDRs in a single pass.
	ipListGlobal: {urls: []string{"https://iplist.my-handbook.ru/?format=json"}},
	ipListRussia: {urls: []string{"https://russia.iplist.opencck.org/?format=json"}},
}

const (
	ipListGlobal = "iplist-global.json"
	ipListRussia = "iplist-russia.json"
)

// ipListFiles maps the name a routing rule references ("iplist:<src>/<group>")
// to the cache file it is served from.
var ipListFiles = map[string]string{
	"global": ipListGlobal,
	"russia": ipListRussia,
}

// dbNames lists the Xray asset databases in a stable display order. Xray itself
// reads these at runtime to resolve geosite:/geoip: rules, so every box running
// Xray — panel and nodes alike — needs them on disk.
var dbNames = []string{"geoip.dat", "geosite.dat"}

// ipListNames lists the iplist databases, in display order. Unlike the .dat
// files these are never read by Xray: the PANEL compiles "iplist:" rules into
// plain domain/IP matchers and pushes the resolved config to the nodes, so only
// the panel needs them. Hence the separate *Lists functions — a node calling
// Refresh/Ensure/Status stays on the .dat files alone and does not pull ~2.7 MB
// of JSON it would never read.
var ipListNames = []string{ipListGlobal, ipListRussia}

// FileInfo is the on-disk state of one geo database (for the settings UI).
type FileInfo struct {
	Name       string `json:"name"`
	Present    bool   `json:"present"`
	Size       int64  `json:"size"`
	ModifiedAt int64  `json:"modified_at"` // unix seconds ≈ last successful download
}

// Status returns metadata for the Xray asset databases in dir. The file mtime
// doubles as the "last downloaded" timestamp (download writes atomically via
// rename).
func Status(dir string) []FileInfo { return status(dir, dbNames) }

// StatusLists returns metadata for the iplist databases (panel only).
func StatusLists(dir string) []FileInfo { return status(dir, ipListNames) }

func status(dir string, names []string) []FileInfo {
	out := make([]FileInfo, 0, len(names))
	for _, name := range names {
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

// Refresh re-downloads every Xray asset database into dir regardless of whether
// it already exists, pulling the latest published version. It attempts all files
// and returns the first error encountered.
func Refresh(dir string) error { return refresh(dir, dbNames) }

// RefreshLists re-downloads the iplist databases (panel only).
func RefreshLists(dir string) error { return refresh(dir, ipListNames) }

func refresh(dir string, names []string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var firstErr error
	for _, name := range names {
		if err := download(sources[name], filepath.Join(dir, name)); err != nil {
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

// Ensure downloads any missing Xray asset database into dir (best-effort).
// Existing files are left as-is; refresh is handled separately.
func Ensure(dir string) error { return ensure(dir, dbNames) }

// EnsureLists downloads any missing iplist database (panel only, best-effort).
func EnsureLists(dir string) error { return ensure(dir, ipListNames) }

func ensure(dir string, names []string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, name := range names {
		path := filepath.Join(dir, name)
		if fi, err := os.Stat(path); err == nil && fi.Size() > 0 {
			continue
		}
		if err := download(sources[name], path); err != nil {
			log.Printf("geo: could not fetch %s: %v (routing rules need it — retry later)", name, err)
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

func download(src source, dest string) error {
	var lastErr error
	client := &http.Client{Timeout: 90 * time.Second}
	for _, url := range src.urls {
		fetch := downloadPlain
		if src.verify {
			fetch = downloadVerified
		}
		if err := fetch(client, url, dest); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no source configured for %s", dest)
	}
	return lastErr
}

// downloadPlain fetches url to dest atomically, without a checksum — for sources
// that publish none. TLS still authenticates the host, so this stops transit
// tampering, but a compromised or hijacked upstream would be trusted. The write
// is staged in "<dest>.new" and renamed, so a failed or truncated transfer leaves
// the previous good copy in place rather than a half-written file.
func downloadPlain(client *http.Client, url, dest string) error {
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
	_, err = io.Copy(f, resp.Body)
	f.Close()
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
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
