// Package catalog is the plugin catalog: a signed index of community plugins the
// panel lists, installs from and checks for new versions.
//
// The index (index.json) is signed with ed25519 (index.json.sig, base64), and the
// panel trusts only the keys built into it — so a mirror, or anyone between the
// panel and the catalog, can serve the index but not change it. Every package is
// pinned by its sha256 in the index, and package links are relative to the index:
// a mirror is a copy of the folder.
package catalog

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/plugin/manifest"
)

// DefaultURL is the official catalog's index.
const DefaultURL = "https://raw.githubusercontent.com/AppsGanin/rospanel-plugins/main/index.json"

// TrustedKeys are the public keys an index may be signed with (base64). More than
// one lets the key be replaced: a new key is added a release before it signs.
var TrustedKeys = []string{
	"2oO2b6JW2q/9weIF3ArCx6W9ZedU/BLsIaq6o/oGW6Q=", // 2026-10-07, held by the RosPanel maintainers
}

// RefreshEvery is how long a fetched index is used before it is fetched again.
const RefreshEvery = 6 * time.Hour

const maxIndex = 4 << 20

// Index is index.json.
type Index struct {
	API         int     `json:"api"`
	GeneratedAt int64   `json:"generated_at"`
	Plugins     []Entry `json:"plugins"`
}

// Entry is one plugin in the catalog, its versions newest first.
type Entry struct {
	ID          string        `json:"id"`
	Name        manifest.Text `json:"name"`
	Description manifest.Text `json:"description,omitempty"`
	Author      string        `json:"author,omitempty"`
	Homepage    string        `json:"homepage,omitempty"`
	Versions    []Version     `json:"versions"`
}

// Version is one package of a plugin.
type Version struct {
	Version   string   `json:"version"`
	Panel     string   `json:"panel,omitempty"` // the manifest's "panel" requirement
	URL       string   `json:"url"`             // relative to the index, or absolute
	SHA256    string   `json:"sha256"`
	Size      int      `json:"size"`
	Perms     []string `json:"perms,omitempty"`
	Net       []string `json:"net,omitempty"`
	Verified  bool     `json:"verified,omitempty"` // its code was reviewed by the catalog's maintainers
	Published int64    `json:"published_at,omitempty"`
}

// Latest is the newest version that runs on panelVersion, or nil.
func (e *Entry) Latest(panelVersion string) *Version {
	for i := range e.Versions {
		if manifest.PanelAllows(e.Versions[i].Panel, panelVersion) {
			return &e.Versions[i]
		}
	}
	return nil
}

// Find returns a version of the entry, or nil.
func (e *Entry) Find(version string) *Version {
	for i := range e.Versions {
		if e.Versions[i].Version == version {
			return &e.Versions[i]
		}
	}
	return nil
}

// Verify checks sig (base64, as in index.json.sig) over raw against the keys.
func Verify(raw, sig []byte, keys []string) error {
	s, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(s) != ed25519.SignatureSize {
		return errors.New("catalog: the signature is not an ed25519 signature")
	}
	for _, k := range keys {
		pub, err := base64.StdEncoding.DecodeString(k)
		if err != nil || len(pub) != ed25519.PublicKeySize {
			continue
		}
		if ed25519.Verify(ed25519.PublicKey(pub), raw, s) {
			return nil
		}
	}
	return errors.New("catalog: the index is not signed by a key this panel trusts")
}

// Parse reads a verified index and sorts each plugin's versions newest first.
func Parse(raw []byte) (*Index, error) {
	var idx Index
	if err := json.Unmarshal(raw, &idx); err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	if idx.API != 1 {
		return nil, fmt.Errorf("catalog: index api %d, this panel reads 1", idx.API)
	}
	for i := range idx.Plugins {
		v := idx.Plugins[i].Versions
		slices.SortFunc(v, func(a, b Version) int {
			switch {
			case manifest.VersionLess(b.Version, a.Version):
				return -1
			case manifest.VersionLess(a.Version, b.Version):
				return 1
			}
			return 0
		})
	}
	return &idx, nil
}

// Fetch downloads url within ctx, up to max bytes.
type Fetch func(ctx context.Context, url string, max int) ([]byte, error)

// Catalog keeps the index the panel last fetched, in memory and on disk (so the
// list still shows while the catalog is unreachable).
type Catalog struct {
	fetch Fetch
	dir   string
	keys  []string

	mu      sync.Mutex
	url     string
	idx     *Index
	at      time.Time
	lastErr string
}

// New returns a catalog that keeps its copy in dir. keys replace the built-in
// TrustedKeys (tests).
func New(fetch Fetch, dir string, keys ...string) *Catalog {
	if len(keys) == 0 {
		keys = TrustedKeys
	}
	return &Catalog{fetch: fetch, dir: dir, keys: keys}
}

// State is what the panel shows about the catalog.
type State struct {
	URL       string `json:"url"`
	FetchedAt int64  `json:"fetched_at"`
	Error     string `json:"error,omitempty"`
	Index     *Index `json:"-"`
}

// Get returns the index from indexURL ("" = the official one), fetching it when
// the copy is older than RefreshEvery, refresh is set, or the address changed. A
// failed fetch keeps the last good copy and reports the error.
func (c *Catalog) Get(ctx context.Context, indexURL string, refresh bool) State {
	if indexURL == "" {
		indexURL = DefaultURL
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.idx == nil && c.url == "" {
		c.loadDisk(indexURL)
	}
	if refresh || c.url != indexURL || c.idx == nil || time.Since(c.at) > RefreshEvery {
		idx, err := c.download(ctx, indexURL)
		if err != nil {
			c.lastErr = err.Error()
			if c.url != indexURL {
				c.idx, c.at = nil, time.Time{} // another catalog's list is not this one's
			}
		} else {
			c.idx, c.at, c.lastErr = idx, time.Now(), ""
		}
		c.url = indexURL
	}
	st := State{URL: indexURL, Error: c.lastErr, Index: c.idx}
	if !c.at.IsZero() {
		st.FetchedAt = c.at.Unix()
	}
	return st
}

func (c *Catalog) download(ctx context.Context, indexURL string) (*Index, error) {
	raw, err := c.fetch(ctx, indexURL, maxIndex)
	if err != nil {
		return nil, err
	}
	sig, err := c.fetch(ctx, indexURL+".sig", 1024)
	if err != nil {
		return nil, fmt.Errorf("catalog: the signature: %w", err)
	}
	if err := Verify(raw, sig, c.keys); err != nil {
		return nil, err
	}
	idx, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	c.saveDisk(indexURL, raw, sig)
	return idx, nil
}

type diskCopy struct {
	URL   string `json:"url"`
	At    int64  `json:"at"`
	Index []byte `json:"index"`
	Sig   []byte `json:"sig"`
}

func (c *Catalog) saveDisk(indexURL string, raw, sig []byte) {
	if c.dir == "" {
		return
	}
	b, _ := json.Marshal(diskCopy{URL: indexURL, At: time.Now().Unix(), Index: raw, Sig: sig})
	_ = os.MkdirAll(c.dir, 0o700)
	tmp := filepath.Join(c.dir, "index.json.tmp")
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, filepath.Join(c.dir, "index.json"))
	}
}

// loadDisk takes the copy kept on disk — verified again: the disk is not trusted
// more than the network.
func (c *Catalog) loadDisk(indexURL string) {
	if c.dir == "" {
		return
	}
	b, err := os.ReadFile(filepath.Join(c.dir, "index.json"))
	if err != nil {
		return
	}
	var d diskCopy
	if json.Unmarshal(b, &d) != nil || d.URL != indexURL || Verify(d.Index, d.Sig, c.keys) != nil {
		return
	}
	if idx, err := Parse(d.Index); err == nil {
		c.idx, c.at, c.url = idx, time.Unix(d.At, 0), indexURL
	}
}

// PackageURL resolves a version's link against the index's address.
func PackageURL(indexURL string, v *Version) (string, error) {
	if indexURL == "" {
		indexURL = DefaultURL
	}
	base, err := url.Parse(indexURL)
	if err != nil {
		return "", err
	}
	ref, err := url.Parse(v.URL)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(ref).String(), nil
}

// Lookup finds a plugin in an index.
func (idx *Index) Lookup(id string) *Entry {
	if idx == nil {
		return nil
	}
	for i := range idx.Plugins {
		if idx.Plugins[i].ID == id {
			return &idx.Plugins[i]
		}
	}
	return nil
}
