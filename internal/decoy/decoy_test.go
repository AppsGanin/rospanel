package decoy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// newTestHandler builds a handler with its own fresh per-install stamp.
func newTestHandler(t *testing.T, template string) *Handler {
	t.Helper()
	h, err := New(template, LoadStamp(t.TempDir()))
	if err != nil {
		t.Fatalf("New(%q): %v", template, err)
	}
	return h
}

func get(h *Handler, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

// A static host always declares a length. net/http chunks anything past its 2 KB
// sniff buffer unless it can determine one, and the templates carry assets into
// the hundreds of kilobytes — no file server chunks static files.
func TestAssetsCarryContentLengthAndValidators(t *testing.T) {
	h := newTestHandler(t, "filecloud")
	rec := get(h, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	cl := rec.Header().Get("Content-Length")
	if n, err := strconv.Atoi(cl); err != nil || n != rec.Body.Len() {
		t.Errorf("Content-Length = %q, want %d", cl, rec.Body.Len())
	}
	for _, hdr := range []string{"Last-Modified", "Etag", "Accept-Ranges"} {
		if rec.Header().Get(hdr) == "" {
			t.Errorf("missing %s", hdr)
		}
	}
	if got := rec.Header().Get("Server"); got != serverName {
		t.Errorf("Server = %q, want %q", got, serverName)
	}
}

// httptest.ResponseRecorder never runs net/http's transfer-encoding decision, so
// the claim this fix rests on — that the decoy stops chunking — can only be
// checked over a real connection. Before it, every asset past the 2 KB sniff
// buffer went out chunked, which no static host does with a static file.
func TestNothingIsChunkedOnTheWire(t *testing.T) {
	srv := httptest.NewServer(newTestHandler(t, "filecloud"))
	defer srv.Close()

	// The front page, an asset far past the sniff buffer, an extensionless miss and
	// a missing asset — every shape of response the handler produces.
	for _, p := range []string{"/", "/assets/v1/script.js", "/nothing-here", "/assets/nope.js"} {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}
		if len(resp.TransferEncoding) > 0 {
			t.Errorf("%s went out %v — a static host would have sent a length", p, resp.TransferEncoding)
		}
		if resp.ContentLength < 0 {
			t.Errorf("%s has no Content-Length", p)
		}
		resp.Body.Close()
	}
}

func TestConditionalRequestGets304(t *testing.T) {
	h := newTestHandler(t, "filecloud")
	etag := get(h, "/").Header().Get("Etag")
	if etag == "" {
		t.Fatal("no ETag to revalidate with")
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("If-None-Match", etag)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304 (a host that never revalidates is a tell)", rec.Code)
	}
}

func TestRangeRequestIsServed(t *testing.T) {
	h := newTestHandler(t, "filecloud")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Range", "bytes=0-9")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", rec.Code)
	}
	if rec.Body.Len() != 10 {
		t.Errorf("body = %d bytes, want 10", rec.Body.Len())
	}
}

// The old handler answered any miss with index.html under a 404, so comparing /
// against a random path returned identical bytes with different statuses —
// something no static host produces. Whatever a miss returns now, status and body
// must agree.
func TestMissNeverServesIndexUnderA404(t *testing.T) {
	for _, template := range []string{"filecloud", "coming-soon", "10gag", "nginx", "YouTube"} {
		t.Run(template, func(t *testing.T) {
			h := newTestHandler(t, template)
			index := get(h, "/").Body.Bytes()
			miss := get(h, "/nothing-here")
			if miss.Code == http.StatusNotFound && bytes.Equal(miss.Body.Bytes(), index) {
				t.Fatal("404 status carrying the index page — the contradiction that gives the decoy away")
			}
		})
	}
}

// Templates that ship a 404 page are classic static sites: every miss is a 404
// carrying that page.
func TestClassicTemplateServesItsOwn404(t *testing.T) {
	h := newTestHandler(t, "coming-soon")
	rec := get(h, "/nothing-here")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Error("empty body, want the template's 404 page")
	}
	if rec.Header().Get("Content-Length") == "" {
		t.Error("missing Content-Length on the 404")
	}
}

// The single-page templates ship no 404 page and imitate `try_files $uri
// /index.html` hosting: an extensionless miss is the app shell under a 200, a
// missing asset is a real 404.
func TestSPATemplateFallbackAndAssetMiss(t *testing.T) {
	h := newTestHandler(t, "filecloud")
	if rec := get(h, "/dashboard/files"); rec.Code != http.StatusOK {
		t.Errorf("extensionless miss status = %d, want 200", rec.Code)
	}
	rec := get(h, "/assets/nope.js")
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing asset status = %d, want 404", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("missing asset body = %d bytes, want empty", rec.Body.Len())
	}
}

// A maintenance decoy is down for everything — every path AND every method. The
// nginx it imitates (`return 503` for the whole server) does not answer a POST
// with 405 while its own page says the site is unavailable.
func TestMaintenanceTemplateIs503Everywhere(t *testing.T) {
	h := newTestHandler(t, "503-1")
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPost, http.MethodDelete} {
		for _, p := range []string{"/", "/anything"} {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(m, p, nil))
			if rec.Code != http.StatusServiceUnavailable {
				t.Errorf("%s %s status = %d, want 503", m, p, rec.Code)
			}
		}
	}
}

// A file server implements GET and HEAD; nginx and Caddy both answer 405 to
// everything else, OPTIONS included. Serving the front page under a 200 for any
// method — which is what handling them all alike did — is not something either does.
func TestNonReadMethodsAreRejected(t *testing.T) {
	h := newTestHandler(t, "filecloud")
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodOptions, "PROPFIND"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(m, "/", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s status = %d, want 405", m, rec.Code)
		}
		if rec.Header().Get("Allow") == "" {
			t.Errorf("%s: 405 without an Allow header", m)
		}
		if rec.Body.Len() != 0 {
			t.Errorf("%s: 405 carried a body", m)
		}
	}
}

// Whatever the request, the response declares a length that matches what it sends.
// A missing Content-Length is what makes net/http chunk, and a wrong one is worse
// than either — so this sweeps every response shape at once, including the odd
// paths a scanner actually sends.
func TestEveryResponseDeclaresItsLength(t *testing.T) {
	for _, tmpl := range []string{"filecloud", "coming-soon", "503-1"} {
		srv := httptest.NewServer(newTestHandler(t, tmpl))
		probes := []struct{ method, path string }{
			{"GET", "/"}, {"HEAD", "/"}, {"OPTIONS", "/"}, {"POST", "/"}, {"PROPFIND", "/"},
			{"GET", "/index.html"}, {"GET", "/404.html"}, {"GET", "/nope"}, {"GET", "/nope.js"},
			{"GET", "/assets"}, {"GET", "/assets/"}, {"GET", "//"}, {"GET", "/?q=1"},
			{"GET", "/../../etc/passwd"}, {"GET", "/" + strings.Repeat("a", 2000)},
		}
		for _, p := range probes {
			req, err := http.NewRequest(p.method, srv.URL+p.path, nil)
			if err != nil {
				t.Fatalf("[%s] %s %s: %v", tmpl, p.method, p.path, err)
			}
			resp, err := srv.Client().Do(req)
			if err != nil {
				t.Fatalf("[%s] %s %s: %v", tmpl, p.method, p.path, err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			what := tmpl + " " + p.method + " " + p.path
			if len(resp.TransferEncoding) > 0 {
				t.Errorf("%s: chunked (%v)", what, resp.TransferEncoding)
			}
			if resp.ContentLength < 0 {
				t.Errorf("%s: no Content-Length", what)
			} else if p.method != http.MethodHead && int(resp.ContentLength) != len(body) {
				t.Errorf("%s: Content-Length %d but %d bytes of body", what, resp.ContentLength, len(body))
			}
			if resp.Header.Get("Server") != serverName {
				t.Errorf("%s: Server = %q", what, resp.Header.Get("Server"))
			}
		}
		srv.Close()
	}
}

// The bundled templates are byte-identical in every binary, so without a
// per-install stamp the hash of the served page identifies the whole fleet.
func TestStampMakesEachInstallUnique(t *testing.T) {
	a := newTestHandler(t, "filecloud")
	b := newTestHandler(t, "filecloud")

	pageA, pageB := get(a, "/"), get(b, "/")
	if bytes.Equal(pageA.Body.Bytes(), pageB.Body.Bytes()) {
		t.Error("two installs serve identical index bytes — one body hash finds them all")
	}
	if pageA.Header().Get("Etag") == pageB.Header().Get("Etag") {
		t.Error("two installs share an ETag")
	}
	if pageA.Header().Get("Last-Modified") == pageB.Header().Get("Last-Modified") {
		t.Error("two installs share a Last-Modified")
	}
	// The mark is an inert HTML comment: the page still parses and still ends in
	// its closing tag.
	if !strings.Contains(strings.ToLower(pageA.Body.String()), "</body>") {
		t.Error("stamped page lost its </body>")
	}
}

// Stable across restarts: a site whose bytes and validators change on every
// reload is a tell of its own.
func TestStampIsStableForOneInstall(t *testing.T) {
	dir := t.TempDir()
	first, err := New("filecloud", LoadStamp(dir))
	if err != nil {
		t.Fatal(err)
	}
	// Drop the in-process cache so the seed is genuinely re-read from disk, the way
	// it would be after a restart.
	stampMu.Lock()
	delete(stampCache, dir)
	stampMu.Unlock()

	second, err := New("filecloud", LoadStamp(dir))
	if err != nil {
		t.Fatal(err)
	}
	a, b := get(first, "/"), get(second, "/")
	if !bytes.Equal(a.Body.Bytes(), b.Body.Bytes()) {
		t.Error("index bytes changed across a restart")
	}
	if a.Header().Get("Etag") != b.Header().Get("Etag") {
		t.Error("ETag changed across a restart")
	}
}

func TestRandomTemplateIsBundled(t *testing.T) {
	available, err := Available()
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, a := range available {
		have[a] = true
	}
	for _, want := range busyTemplates {
		if !have[want] {
			t.Errorf("busyTemplates lists %q, which is not bundled", want)
		}
	}
	if _, err := New(RandomTemplate(), LoadStamp(t.TempDir())); err != nil {
		t.Errorf("RandomTemplate returned an unusable slug: %v", err)
	}
}
