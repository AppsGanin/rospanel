package extsub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/netguard"
	"github.com/AppsGanin/rospanel/internal/version"
)

const (
	maxBodyBytes = 2 << 20 // a subscription is a list of links; 2 MB is thousands of them
	fetchTimeout = 30 * time.Second
)

// Headers builds the request headers this panel presents when it reads somebody else's
// subscription.
//
// Panels increasingly refuse a caller that does not identify a device — ours does, and
// the refusal is a 403 with a human-readable message, not something the parser can tell
// apart from an empty subscription. So the fetch answers the question a client would.
//
// Every field of the identity is an override: an empty one falls back to the default
// below, so a subscription nobody configured still answers the device question and
// still presents the same device on every sync. Set fields are sent verbatim — the
// point of having them is that the operator knows something we do not about what the
// other side accepts.
func Headers(source string, id model.ExtIdentity) map[string]string {
	return map[string]string{
		model.HeaderHWID:        or(id.HWID, DerivedHWID(source)),
		model.HeaderDeviceOS:    or(id.DeviceOS, DefaultDeviceOS),
		model.HeaderOSVersion:   or(id.OSVersion, version.Version),
		model.HeaderDeviceModel: or(id.DeviceModel, DefaultDeviceModel),
		"User-Agent":            or(id.UserAgent, DefaultUserAgent()),
	}
}

// The defaults. The descriptive three are not required by anything; they are there so
// the operator on the other end sees a row they can recognise instead of an anonymous
// device they might unbind at random.
const (
	DefaultDeviceOS    = "rospanel"
	DefaultDeviceModel = "panel"
)

// DefaultUserAgent is deliberately not a client's: the format this package parses is
// the share-link list, which is what a panel serves when it does not recognise the
// caller. Claiming to be Clash or sing-box would get us a document it cannot read.
// An operator who needs a particular one overrides it.
func DefaultUserAgent() string { return "rospanel/" + version.Version }

func or(v, fallback string) string {
	if v = strings.TrimSpace(v); v != "" {
		return v
	}
	return fallback
}

// DerivedHWID is the device id a subscription presents when the operator has not
// chosen one. Exported because the editor shows it as the placeholder: the operator
// should be able to see what will actually be sent before deciding to override it.
//
// Derived rather than random or stored, so the same subscription presents the same
// device on every sync — the upstream binds one slot instead of burning a new one per
// refresh and eventually locking us out. It is a hash, so the upstream's own token,
// which is what makes the source unique, is not echoed back in a second place. Different
// sources give different ids, so two upstreams never share a slot.
func DerivedHWID(source string) string {
	sum := sha256.Sum256([]byte("rospanel/extsub\x00" + strings.TrimSpace(source)))
	return hex.EncodeToString(sum[:16])
}

// IsURL reports whether a source is fetched (an http(s) address) rather than
// decoded in place (a pasted happ:// link, a base64 blob, a list of links).
func IsURL(source string) bool {
	s := strings.ToLower(strings.TrimSpace(source))
	return strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://")
}

// ValidateSource checks a source before it is stored: a URL must pass the same
// SSRF gate as every other address the panel fetches, and an inline payload must
// decode to at least one usable link — an operator pasting the wrong thing is
// told now, not by an empty list later.
func ValidateSource(source string) error {
	source = strings.TrimSpace(source)
	if source == "" {
		return errors.New("empty source")
	}
	if IsURL(source) {
		return netguard.ValidateFetchURL(source)
	}
	if len(source) > maxBodyBytes {
		return errors.New("payload too large")
	}
	if len(ParseAll(Decode([]byte(source)))) == 0 {
		return errors.New("no share links found in the payload")
	}
	return nil
}

// Load resolves a source to its endpoints: fetched through the SSRF-safe client
// when it is a URL, decoded in place otherwise.
// id is the device identity to present to a panel that requires one; its empty fields
// fall back to defaults (see Headers). Ignored for an inline payload, which is decoded
// here and fetches nothing.
func Load(ctx context.Context, source string, id model.ExtIdentity) ([]Endpoint, error) {
	source = strings.TrimSpace(source)
	var body []byte
	if IsURL(source) {
		if err := netguard.ValidateFetchURL(source); err != nil {
			return nil, err
		}
		if _, ok := ctx.Deadline(); !ok {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, fetchTimeout)
			defer cancel()
		}
		var err error
		if body, err = netguard.GetWithHeaders(ctx, source, maxBodyBytes, Headers(source, id)); err != nil {
			return nil, fmt.Errorf("fetch: %w", err)
		}
	} else {
		body = []byte(source)
	}
	eps := ParseAll(Decode(body))
	if len(eps) == 0 {
		return nil, errors.New("no share links found")
	}
	return eps, nil
}
