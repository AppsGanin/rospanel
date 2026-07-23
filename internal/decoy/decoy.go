// Package decoy serves an innocent-looking website ("заглушка") for every
// request that doesn't carry the secret panel path. The goal: a visitor, DPI,
// or scanner sees an ordinary site, never a hint that a VPN panel exists.
//
// Looking ordinary is not only about the HTML. A probe compares what the server
// DOES against what it claims to be, so the handler mimics a static file server
// down to the mechanics: Content-Length on every response (net/http chunks bodies
// over its 2 KB sniff buffer, and no static host chunks static files),
// Last-Modified + ETag + conditional 304s, byte ranges, a 405 for methods a file
// server does not implement, and a not-found page whose status agrees with its
// body. See Handler.ServeHTTP.
package decoy

import (
	"bytes"
	"crypto/rand"
	"embed"
	"fmt"
	"io/fs"
	"math/big"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
)

//go:embed all:templates
var templatesFS embed.FS

// serverName is the Server header the decoy presents.
//
// It is deliberately NOT "nginx". Xray terminates TLS for :443 and falls back to
// this handler, so everything an outside prober fingerprints at the TLS layer —
// ClientHello response, extension order, session tickets, ALPN handling — is Go's
// crypto/tls. An nginx banner over a Go TLS stack is a contradiction that costs
// one JA4S lookup to spot. Caddy is Go, is a mainstream choice for exactly this
// kind of static site, and matches the behaviour implemented below, so the banner
// and the machine underneath tell the same story.
const serverName = "Caddy"

// Available returns the list of bundled template slugs.
func Available() ([]string, error) {
	entries, err := fs.ReadDir(templatesFS, "templates")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// busyTemplates are the slugs picked from at install time. A decoy is not only
// read by a scanner — it is also the cover story for the traffic volume the box
// carries, and gigabytes a day flowing to an 8 KB "coming soon" placeholder is a
// mismatch no amount of encryption hides. These templates are sites where large,
// long-lived transfers are the whole point.
//
// The slugs are directory names, not brands: "YouTube" is a video-hosting layout
// that presents itself as "Видеоландия" and carries no third-party naming.
//
// Left out are the templates that contradict a busy box rather than explain it:
// the placeholders (coming-soon), the maintenance pages (503-*, maintenance) and
// the bare nginx page. All stay selectable by hand — an operator who actually
// wants a site that looks parked can still say so.
var busyTemplates = []string{"filecloud", "downloader", "converter", "speedtest", "10gag", "YouTube"}

// RandomTemplate returns a template slug for a fresh install. Choosing at random
// keeps a fleet from sharing one recognisable front page — the body stamp (see
// Stamp) separates two installs that land on the same slug, and this separates
// their look as well.
func RandomTemplate() string {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(busyTemplates))))
	if err != nil {
		return busyTemplates[0]
	}
	return busyTemplates[n.Int64()]
}

// asset is one preloaded file of a template, already stamped for this install.
// Preloading trades ~1 MB (the largest template) for a request path that neither
// allocates a copy of the file nor recomputes its validators.
type asset struct {
	name    string
	body    []byte
	ct      string
	etag    string
	modTime time.Time
}

// Handler serves a single decoy template.
type Handler struct {
	files    map[string]*asset
	index    *asset
	notFound *asset // the template's own 404.html, when it ships one
	down     bool   // maintenance template: every request answers 503 with the index page
}

// maintenanceTemplates are slugs whose front page advertises the site as
// temporarily unavailable; they should answer with 503, not 200, so the body and
// status agree (a "503" page served as 200 is an easy tell).
var maintenanceTemplates = map[string]bool{"503-1": true, "503-2": true, "maintenance": true}

// New returns a decoy handler for the given template slug, stamped with this
// install's entropy (see LoadStamp).
func New(template string, st Stamp) (*Handler, error) {
	if template == "" {
		template = "coming-soon"
	}
	sub, err := fs.Sub(templatesFS, path.Join("templates", template))
	if err != nil {
		return nil, fmt.Errorf("decoy template %q: %w", template, err)
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, fmt.Errorf("decoy template %q missing index.html: %w", template, err)
	}

	h := &Handler{files: map[string]*asset{}, down: maintenanceTemplates[template]}
	err = fs.WalkDir(sub, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := fs.ReadFile(sub, name)
		if err != nil {
			return err
		}
		h.files[name] = newAsset(name, body, st)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("decoy template %q: %w", template, err)
	}
	h.index = h.files["index.html"]
	h.notFound = h.files["404.html"]
	return h, nil
}

// newAsset stamps one file and derives its validators. Only HTML carries the
// per-install mark: binary assets have no place to put one, and their bytes are
// not what a body-hash search matches on anyway.
func newAsset(name string, body []byte, st Stamp) *asset {
	if strings.HasSuffix(name, ".html") {
		body = stampHTML(body, st.mark())
	}
	mod := st.modTime(name)
	return &asset{
		name:    name,
		body:    body,
		ct:      contentType(name),
		modTime: mod,
		// Caddy's static ETag: base-36 modification time followed by the hex size.
		etag: fmt.Sprintf("%q", strconv.FormatInt(mod.Unix(), 36)+strconv.FormatInt(int64(len(body)), 16)),
	}
}

// stampHTML inserts the per-install mark just before </body>, or appends it when
// the document has no body tag.
func stampHTML(body []byte, mark string) []byte {
	at := bytes.LastIndex(bytes.ToLower(body), []byte("</body>"))
	if at < 0 {
		return append(append(append([]byte{}, body...), '\n'), mark...)
	}
	out := make([]byte, 0, len(body)+len(mark)+1)
	out = append(out, body[:at]...)
	out = append(out, mark...)
	out = append(out, '\n')
	return append(out, body[at:]...)
}

// ServeHTTP answers like a static file server: known paths are served with full
// validators, unknown ones get the template's not-found behaviour, and methods a
// file server doesn't implement get a 405.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Server", serverName)

	// A maintenance decoy is down for everything, method included. The config it
	// imitates (nginx `return 503` for the whole server) answers every request the
	// same way, so gating methods first would have it refuse a POST with 405 while
	// its own page says the site is unavailable.
	if h.down {
		h.serve(w, r, h.index, http.StatusServiceUnavailable)
		return
	}

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		// A static host has nothing to POST to, and OPTIONS isn't something a file
		// server implements either — nginx and Caddy both answer 405. Serving the full
		// front page under a 200 for every method, as this used to, is not something
		// either does.
		w.Header().Set("Allow", "GET, HEAD")
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	a, ok := h.files[name]
	if !ok {
		h.serveMiss(w, r, name)
		return
	}
	h.serve(w, r, a, http.StatusOK)
}

// serveMiss answers a path the template doesn't have.
func (h *Handler) serveMiss(w http.ResponseWriter, r *http.Request, name string) {
	switch {
	// A template with its own 404 page is a classic static site: every miss is a 404
	// carrying that page.
	case h.notFound != nil:
		h.serve(w, r, h.notFound, http.StatusNotFound)

	// The single-page templates ship no 404 page, and the hosting they imitate
	// (`try_files $uri /index.html`) answers an extensionless miss with the app
	// shell under a 200. Serving that same shell under a 404 — which is what
	// falling back to index used to do — is a contradiction no static host
	// produces, and one GET of a random path next to a GET of / exposes it.
	case path.Ext(name) == "":
		h.serve(w, r, h.index, http.StatusOK)

	// A missing asset is a genuine 404, with the empty body such hosts return.
	default:
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusNotFound)
	}
}

// serve writes one asset. The 200 path goes through http.ServeContent, which
// supplies Content-Length, Last-Modified, Accept-Ranges, byte ranges and the
// conditional 304s — the behaviour a static server has and a plain Write does
// not. Other statuses are written directly (ServeContent always writes 200), with
// Content-Length set so those bodies aren't chunked either.
func (h *Handler) serve(w http.ResponseWriter, r *http.Request, a *asset, status int) {
	w.Header().Set("Content-Type", a.ct)
	w.Header().Set("Etag", a.etag)
	if status == http.StatusOK {
		http.ServeContent(w, r, a.name, a.modTime, bytes.NewReader(a.body))
		return
	}
	w.Header().Set("Last-Modified", a.modTime.UTC().Format(http.TimeFormat))
	w.Header().Set("Content-Length", strconv.Itoa(len(a.body)))
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(a.body)
	}
}

func contentType(name string) string {
	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		return ct
	}
	return "text/html; charset=utf-8"
}
