package tlsmgr

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// challengePath is where an ACME server looks for an HTTP-01 answer. Plain HTTP on
// port 80, always — the CA will not follow this to 443, which is why port 80 cannot
// simply be redirected away.
const challengePath = "/.well-known/acme-challenge/"

var (
	// challenges holds token → key authorization for the challenges currently in
	// flight. Tiny and short-lived: lego stores one per domain and removes it as soon
	// as the CA has validated.
	challenges sync.Map
	// sharedHTTP01 is set while something in this process is already listening on
	// port 80 and serving ServeHTTP01 from it.
	sharedHTTP01 atomic.Bool
)

// UseSharedHTTP01 declares that port 80 is held by a listener in this process that
// answers ACME challenges through ServeHTTP01, so ObtainACME must not stand up its
// own server on that port.
//
// Without this, the two compete for the same port and the loser is certificate
// renewal — which fails silently for weeks and then takes the whole panel down when
// the certificate expires. Any listener that takes port 80 has to turn this on, and
// turn it off again if it stops.
func UseSharedHTTP01(on bool) { sharedHTTP01.Store(on) }

// SharedHTTP01 reports whether ACME challenges are served by a shared listener.
func SharedHTTP01() bool { return sharedHTTP01.Load() }

// sharedProvider answers HTTP-01 out of the challenges map instead of binding a
// port. It satisfies lego's challenge.Provider.
type sharedProvider struct{}

func (sharedProvider) Present(_, token, keyAuth string) error {
	challenges.Store(token, keyAuth)
	return nil
}

func (sharedProvider) CleanUp(_, token, _ string) error {
	challenges.Delete(token)
	return nil
}

// PresentForTest stores a challenge answer as lego's provider would, so the port-80
// listener can be tested against a real one.
func PresentForTest(token, keyAuth string) { challenges.Store(token, keyAuth) }

// ServeHTTP01 answers an ACME HTTP-01 challenge, reporting whether the request was
// one. Everything under the challenge path is claimed, including tokens we do not
// hold: they get a 404, which is what a server that handles this path does with an
// unknown one. Redirecting them to HTTPS instead would send a CA somewhere it will
// not follow.
func ServeHTTP01(w http.ResponseWriter, r *http.Request) bool {
	token, ok := strings.CutPrefix(r.URL.Path, challengePath)
	if !ok {
		return false
	}
	v, held := challenges.Load(token)
	if !held || token == "" {
		http.NotFound(w, r)
		return true
	}
	keyAuth, _ := v.(string)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Length", strconv.Itoa(len(keyAuth)))
		w.WriteHeader(http.StatusOK)
		return true
	}
	_, _ = w.Write([]byte(keyAuth))
	return true
}
