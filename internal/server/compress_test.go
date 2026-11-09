package server

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	webui "github.com/AppsGanin/rospanel/web"
)

// firstAsset returns the name and bytes of one built SPA asset under assets/<pattern>.
// The tests run against the embedded build, so they also stand guard over "the panel
// still ships a bundle at all".
func firstAsset(t *testing.T, pattern string) (string, []byte) {
	t.Helper()
	spa, err := webui.FS()
	if err != nil {
		t.Fatalf("spa fs: %v", err)
	}
	names, err := fs.Glob(spa, "assets/"+pattern)
	if err != nil || len(names) == 0 {
		t.Fatalf("no %s in the embedded build (err=%v) — build the SPA first", pattern, err)
	}
	raw, err := fs.ReadFile(spa, names[0])
	if err != nil {
		t.Fatalf("read %s: %v", names[0], err)
	}
	return strings.TrimPrefix(names[0], "assets/"), raw
}

func getAsset(t *testing.T, h http.Handler, target, acceptEncoding string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.RemoteAddr = testClientIP + ":40000"
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func gunzip(t *testing.T, raw []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("body is not gzip: %v", err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	return out
}

// The panel's first screen is a 1.7 MB bundle. A client that says it takes gzip has to
// get it gzipped — that is the whole point — and the bytes have to decode back to the
// file the build produced, byte for byte.
func TestAssetsAreCompressedForClientsThatAsk(t *testing.T) {
	t.Parallel()
	h, _, _ := nodeAPITestServer(t)
	name, raw := firstAsset(t, "*.js")

	rec := getAsset(t, h, "/secretpath/assets/"+name, "gzip, deflate, br")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding %q, want gzip — a client that offered gzip got the raw bundle", got)
	}
	if !strings.Contains(rec.Header().Get("Vary"), "Accept-Encoding") {
		t.Errorf("Vary %q does not name Accept-Encoding", rec.Header().Get("Vary"))
	}
	if got := rec.Body.Len(); got >= len(raw) {
		t.Errorf("compressed body is %d bytes, the file is %d — nothing was gained", got, len(raw))
	}
	// The compressed length is known up front, so the response is not chunked: a
	// browser can show progress, and a wrong Content-Length would truncate the body.
	if cl := rec.Header().Get("Content-Length"); cl != strconv.Itoa(rec.Body.Len()) {
		t.Errorf("Content-Length %q does not match the %d compressed bytes", cl, rec.Body.Len())
	}
	if got := gunzip(t, rec.Body.Bytes()); !bytes.Equal(got, raw) {
		t.Errorf("decoded body differs from the embedded %s (%d vs %d bytes)", name, len(got), len(raw))
	}
}

// A client that does not accept gzip must get the file as it is: compressing it anyway
// hands it bytes it cannot read. The stale cache or exotic tool that omits the header
// is exactly who this protects.
func TestAssetsStayReadableWithoutAcceptEncoding(t *testing.T) {
	t.Parallel()
	h, _, _ := nodeAPITestServer(t)
	name, raw := firstAsset(t, "*.css")

	rec := getAsset(t, h, "/secretpath/assets/"+name, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding %q on a client that asked for none", got)
	}
	if !bytes.Equal(rec.Body.Bytes(), raw) {
		t.Errorf("body is not the stylesheet: %d bytes, want %d", rec.Body.Len(), len(raw))
	}
	// Even here, so a cache between the panel and its admin never mixes the two forms.
	if !strings.Contains(rec.Header().Get("Vary"), "Accept-Encoding") {
		t.Errorf("Vary %q does not name Accept-Encoding", rec.Header().Get("Vary"))
	}
}

// testAssets builds the handler over a fake build: one 2 KB script worth compressing,
// one font that is already compressed, at the names Vite would emit.
func testAssets(t *testing.T) *spaAssets {
	t.Helper()
	script := []byte(strings.Repeat("export const x = 1;\n", 120))
	font := bytes.Repeat([]byte{0x77, 0x4f, 0x46, 0x32}, 300)
	a, err := newSPAAssets(fstest.MapFS{
		"assets/app-abc123.js":     {Data: script},
		"assets/font-abc123.woff2": {Data: font},
	})
	if err != nil {
		t.Fatalf("assets: %v", err)
	}
	return a
}

// The handler has to decline exactly where compressing would break something, and the
// interesting cases are the ones a browser actually sends.
func TestSpaAssetsDeclinesWhereCompressingBreaks(t *testing.T) {
	t.Parallel()
	a := testAssets(t)

	cases := []struct {
		name       string
		method     string
		path       string
		header     http.Header
		wantStatus int
		wantEnc    string
		wantBody   int // -1: do not check the length
	}{
		{name: "script is compressed", path: "/assets/app-abc123.js",
			header: http.Header{"Accept-Encoding": {"gzip"}}, wantStatus: 200, wantEnc: "gzip", wantBody: -1},
		{name: "a font is left alone", path: "/assets/font-abc123.woff2",
			header: http.Header{"Accept-Encoding": {"gzip"}}, wantStatus: 200, wantEnc: "", wantBody: 1200},
		{name: "q=0 means no", path: "/assets/app-abc123.js",
			header: http.Header{"Accept-Encoding": {"gzip;q=0"}}, wantStatus: 200, wantEnc: "", wantBody: len(strings.Repeat("export const x = 1;\n", 120))},
		{name: "a range stays byte-exact", path: "/assets/app-abc123.js",
			header: http.Header{"Accept-Encoding": {"gzip"}, "Range": {"bytes=0-9"}}, wantStatus: 206, wantEnc: "", wantBody: 10},
		{name: "a HEAD has no body to send", method: http.MethodHead, path: "/assets/app-abc123.js",
			header: http.Header{"Accept-Encoding": {"gzip"}}, wantStatus: 200, wantEnc: "gzip", wantBody: 0},
		{name: "an unknown file is a 404", path: "/assets/nope.js",
			header: http.Header{"Accept-Encoding": {"gzip"}}, wantStatus: 404, wantBody: -1},
		{name: "a name cannot walk out of the directory", path: "/assets/../secret",
			header: http.Header{"Accept-Encoding": {"gzip"}}, wantStatus: 404, wantBody: -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			method := c.method
			if method == "" {
				method = http.MethodGet
			}
			req := httptest.NewRequest(method, c.path, nil)
			req.Header = c.header
			rec := httptest.NewRecorder()
			a.ServeHTTP(rec, req)

			if rec.Code != c.wantStatus {
				t.Fatalf("status %d, want %d", rec.Code, c.wantStatus)
			}
			if got := rec.Header().Get("Content-Encoding"); got != c.wantEnc {
				t.Fatalf("Content-Encoding %q, want %q", got, c.wantEnc)
			}
			if c.wantBody >= 0 && rec.Body.Len() != c.wantBody {
				t.Errorf("body is %d bytes, want %d", rec.Body.Len(), c.wantBody)
			}
		})
	}
}

// Compressing is per file and once: a second request must not redo it, and the bytes
// must be the same ones — that is what makes the default (smaller) level affordable on
// a 1-vCPU box.
func TestAssetCompressionIsDoneOnce(t *testing.T) {
	t.Parallel()
	a := testAssets(t)
	f := a.files["app-abc123.js"]
	first := f.compressed()
	second := f.compressed()
	if len(first) == 0 {
		t.Fatal("the script was not compressed")
	}
	if &first[0] != &second[0] {
		t.Error("the second call rebuilt the compressed body — the work is not cached")
	}
	if f.compressed() != nil && len(first) >= len(f.raw) {
		t.Errorf("compressed %d bytes against %d raw — worth nothing", len(first), len(f.raw))
	}
	if nf := a.files["font-abc123.woff2"]; nf.compressed() != nil {
		t.Error("a woff2 font was compressed, which only adds bytes")
	}
}
