package catalog

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/plugin/manifest"
)

// The maintainers' side: `rospanel plugin keygen` and `rospanel plugin index`.
//
// A catalog is a folder:
//
//	plugins/<id>/<id>-<version>.zip   the packages
//	verified.json                     {"<id>": ["1.0.0", …]}: versions whose code was reviewed
//	index.json, index.json.sig        written by Build and Sign

// Keygen makes a signing key: the private key (base64 seed, kept by the
// maintainers) and the public key (base64, built into the panel).
func Keygen() (private, public string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(priv.Seed()), base64.StdEncoding.EncodeToString(pub), nil
}

// Sign signs an index with a private key from Keygen; the result is index.json.sig.
func Sign(raw []byte, private string) ([]byte, error) {
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(private))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("catalog: the key is not a private key from `rospanel plugin keygen`")
	}
	sig := ed25519.Sign(ed25519.NewKeyFromSeed(seed), raw)
	return []byte(base64.StdEncoding.EncodeToString(sig) + "\n"), nil
}

// PublicKey is the public half of a private key from Keygen.
func PublicKey(private string) (string, error) {
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(private))
	if err != nil || len(seed) != ed25519.SeedSize {
		return "", errors.New("catalog: the key is not a private key from `rospanel plugin keygen`")
	}
	pub := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	return base64.StdEncoding.EncodeToString(pub), nil
}

// Build reads a catalog folder and returns its index.json. Every package is read
// the way the panel reads it; one that would not install stops the build.
func Build(dir string, now time.Time) ([]byte, error) {
	verified := map[string][]string{}
	if b, err := os.ReadFile(filepath.Join(dir, "verified.json")); err == nil {
		if err := json.Unmarshal(b, &verified); err != nil {
			return nil, fmt.Errorf("verified.json: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	// Dates stay as they were for packages already listed.
	published := map[string]int64{}
	if b, err := os.ReadFile(filepath.Join(dir, "index.json")); err == nil {
		if old, err := Parse(b); err == nil {
			for _, e := range old.Plugins {
				for _, v := range e.Versions {
					published[v.SHA256] = v.Published
				}
			}
		}
	}
	zips, err := filepath.Glob(filepath.Join(dir, "plugins", "*", "*.zip"))
	if err != nil {
		return nil, err
	}
	slices.Sort(zips)
	byID := map[string]*Entry{}
	newest := map[string]*manifest.Manifest{}
	for _, path := range zips {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		rel, _ := filepath.Rel(dir, path)
		pkg, err := manifest.Read(raw, "")
		if err != nil {
			return nil, fmt.Errorf("%s: %w", rel, err)
		}
		m := pkg.Manifest
		if folder := filepath.Base(filepath.Dir(path)); folder != m.ID {
			return nil, fmt.Errorf("%s: plugin %q is in the folder of %q", rel, m.ID, folder)
		}
		e := byID[m.ID]
		if e == nil {
			e = &Entry{ID: m.ID}
			byID[m.ID] = e
		}
		if e.Find(m.Version) != nil {
			return nil, fmt.Errorf("%s: %s %s is listed twice", rel, m.ID, m.Version)
		}
		sum := sha256.Sum256(raw)
		v := Version{
			Version: m.Version, Panel: m.Panel, URL: filepath.ToSlash(rel), SHA256: hex.EncodeToString(sum[:]),
			Size: len(raw), Perms: m.Permissions, Net: m.Net,
			Verified: slices.Contains(verified[m.ID], m.Version), Published: published[hex.EncodeToString(sum[:])],
		}
		if v.Published == 0 {
			v.Published = now.Unix()
		}
		e.Versions = append(e.Versions, v)
		if n := newest[m.ID]; n == nil || manifest.VersionLess(n.Version, m.Version) {
			newest[m.ID] = m
		}
	}
	for id, list := range verified {
		e := byID[id]
		for _, ver := range list {
			if e == nil || e.Find(ver) == nil {
				return nil, fmt.Errorf("verified.json: %s %s is not in plugins/", id, ver)
			}
		}
	}
	idx := Index{API: 1, GeneratedAt: now.Unix()}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		e, m := byID[id], newest[id]
		e.Name, e.Description, e.Author, e.Homepage = m.Name, m.Description, m.Author, m.Homepage
		idx.Plugins = append(idx.Plugins, *e)
	}
	b, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return nil, err
	}
	// Sorted newest first, as Parse would.
	parsed, err := Parse(b)
	if err != nil {
		return nil, err
	}
	b, err = json.MarshalIndent(parsed, "", "  ")
	return append(b, '\n'), err
}
