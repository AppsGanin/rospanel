package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/datasec"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/sub"
)

// With the operator's own subscription page set, every browser opening a link goes
// there — the operator's too. The user card hands them a preview link instead: the
// subscription link with ?preview=<expiry>.<signature>, which the panel answers with
// its own page. Signed with a key only this install holds and good for an hour, so a
// user cannot make one, and one copied out of the card soon stops working.

// subPreviewTTL is how long a preview link opens the panel's page.
const subPreviewTTL = time.Hour

var (
	subPreviewKeyOnce sync.Once
	subPreviewKeyVal  []byte
)

// subPreviewKey is this install's key for preview links; without one (encryption
// off) a key for this run of the panel, so the links die with a restart.
func subPreviewKey() []byte {
	subPreviewKeyOnce.Do(func() {
		if k, ok := datasec.Derive("sub-preview"); ok {
			subPreviewKeyVal = k
			return
		}
		subPreviewKeyVal = make([]byte, 32)
		_, _ = rand.Read(subPreviewKeyVal)
	})
	return subPreviewKeyVal
}

func subPreviewSig(token string, exp int64) string {
	mac := hmac.New(sha256.New, subPreviewKey())
	mac.Write([]byte(token + "|" + strconv.FormatInt(exp, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}

// subPreviewURL is the link that opens the panel's own page for the user, or "" when
// no page of the operator's own is set and the plain link already does.
func subPreviewURL(set *model.Settings, token string, now time.Time) string {
	if set.SubPageURL == "" || token == "" {
		return ""
	}
	exp := now.Add(subPreviewTTL).Unix()
	return sub.URL(set, token) + "?preview=" + strconv.FormatInt(exp, 10) + "." + subPreviewSig(token, exp)
}

// validSubPreview reports whether v is a live preview signature for token.
func validSubPreview(token, v string, now time.Time) bool {
	expStr, sig, ok := strings.Cut(v, ".")
	if !ok {
		return false
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || now.Unix() > exp || exp > now.Add(subPreviewTTL).Unix() {
		return false
	}
	return hmac.Equal([]byte(sig), []byte(subPreviewSig(token, exp)))
}
