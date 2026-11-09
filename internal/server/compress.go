package server

import (
	"bytes"
	"compress/gzip"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The panel's SPA ships as one content-hashed bundle: 1.7 MB of JavaScript and 74 KB
// of CSS before a single screen renders. net/http compresses nothing on its own, so
// until this every first load pulled those bytes as they are — three times the data on
// the mobile link an operator is often on, from a box that is usually a 1-vCPU VPS.
//
// Each file is compressed at the default level once, on its first request, and kept:
// the assets are immutable (their URL is their content hash, TTL a year — see
// cacheControl), so a process compresses a given file at most once and a client
// downloads it at most once. Compressing per request instead would trade those 490 KB
// for 600 KB at the fast level, or spend 50 ms of a single-core box's time on every
// load at the good one.
//
// gzip only: brotli would be another dependency for ~20% more, and nothing sits
// between the panel and its admin to negotiate it.
type spaAssets struct {
	files map[string]*spaAsset // keyed by file name under assets/, e.g. "index-abc123.js"
}

type spaAsset struct {
	raw   []byte
	ctype string
	once  sync.Once
	gz    []byte // nil when the type does not compress or compression failed
}

// newSPAAssets reads the built SPA's assets into memory. They are embedded in the
// binary and immutable, so there is nothing to re-read; compression is deferred to the
// first request so a panel that is restarted often does not redo that work on boot.
func newSPAAssets(fsys fs.FS) (*spaAssets, error) {
	entries, err := fs.ReadDir(fsys, "assets")
	if err != nil {
		return nil, err
	}
	a := &spaAssets{files: make(map[string]*spaAsset, len(entries))}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		raw, err := fs.ReadFile(fsys, "assets/"+e.Name())
		if err != nil {
			return nil, err
		}
		a.files[e.Name()] = &spaAsset{raw: raw, ctype: assetContentType(e.Name())}
	}
	return a, nil
}

// compressed returns the gzip form of the file, or nil when compressing it would not
// help. Building it here (not per request) is what makes the default level affordable.
func (f *spaAsset) compressed() []byte {
	if !compressible(f.ctype) || len(f.raw) < minCompressible {
		return nil
	}
	f.once.Do(func() {
		var buf bytes.Buffer
		buf.Grow(len(f.raw) / 3)
		zw, err := gzip.NewWriterLevel(&buf, gzip.DefaultCompression)
		if err != nil {
			return
		}
		if _, err := zw.Write(f.raw); err != nil {
			return
		}
		if err := zw.Close(); err != nil {
			return
		}
		f.gz = buf.Bytes()
	})
	return f.gz
}

func (a *spaAssets) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name, ok := strings.CutPrefix(r.URL.Path, "/assets/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	// A map lookup, not an fs lookup: nothing a client sends can walk out of the
	// directory, and a name the build did not produce is a 404.
	f := a.files[name]
	if f == nil {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", f.ctype)
	// On every response, compressed or not: what the URL means depends on
	// Accept-Encoding, and a cache that does not know that may hand gzip bytes to a
	// client that asked for none.
	h.Add("Vary", "Accept-Encoding")

	// A Range request is answered with 206 and byte offsets into the file, so it stays
	// on the raw path — a browser resuming a download must not be handed a compressed
	// stream it did not ask to reassemble.
	gz := f.compressed()
	if gz != nil && r.Header.Get("Range") == "" && acceptsGzip(r.Header.Get("Accept-Encoding")) {
		h.Set("Content-Encoding", "gzip")
		// Known before the first byte, unlike a streaming writer's: the browser can
		// show progress and the response needs no chunked framing.
		h.Set("Content-Length", strconv.Itoa(len(gz)))
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(gz)
		}
		return
	}
	// Identity: ServeContent gives Range, HEAD and If-Modified-Since their usual
	// meaning without a second implementation of them.
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(f.raw))
}

// minCompressible is the size below which gzip's header and trailer cost more than the
// compression saves — every real asset is far above it.
const minCompressible = 512

// assetContentType answers with the shipped asset types rather than whatever
// mime.TypeByExtension finds in this machine's mime.types: the panel must send the same
// Content-Type on a minimal VPS as on the developer's machine, and the build only ever
// emits these.
func assetContentType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json", ".map":
		return "application/json"
	case ".svg":
		return "image/svg+xml"
	case ".html":
		return "text/html; charset=utf-8"
	case ".woff2":
		return "font/woff2"
	case ".woff":
		return "font/woff"
	case ".ttf":
		return "font/ttf"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".ico":
		return "image/x-icon"
	case ".txt":
		return "text/plain; charset=utf-8"
	}
	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

// compressible reports whether a Content-Type is text worth encoding. Text is the
// allow-list rather than "everything but images" on purpose: a font or an archive is
// already compressed, and parking bytes through gzip only adds a wrapper.
func compressible(contentType string) bool {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	switch ct {
	case "text/javascript", "application/javascript", "application/json",
		"image/svg+xml", "application/xml", "text/xml":
		return true
	}
	return strings.HasPrefix(ct, "text/")
}
