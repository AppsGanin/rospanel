package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/maphash"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/plugin/manifest"
)

// transformSubscription: a plugin rewrites what a client is served — renames
// servers, reorders them, adds routing rules. Every client refresh passes here, so
// each answer is kept for subTransformTTL by its exact input, a plugin is given
// subTransformTimeout, and anything that fails or comes back in another shape
// serves the panel's own profile.

const (
	subTransformTimeout = 300 * time.Millisecond
	subTransformTTL     = 5 * time.Minute
	subTransformCache   = 32 << 20 // bytes of answers kept, all plugins together
	subTransformEntries = 50000
	maxSubOutput        = 4 << 20
	maxLinkLen          = 8 << 10
)

// SubFormats a plugin is shown, as transformSubscription's "format".
const (
	SubLinks   = "links"
	SubClash   = "clash"
	SubSingBox = "singbox"
	SubXray    = "xray"
)

type transformCache struct {
	mu    sync.Mutex
	m     map[string]cachedTransform
	bytes int
}

type cachedTransform struct {
	out []byte // nil: the plugin had nothing to change
	at  time.Time
}

func (c *transformCache) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.m[key]
	if !ok || time.Since(t.at) >= subTransformTTL {
		return nil, false
	}
	return t.out, true
}

func (c *transformCache) put(key string, out []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Bounded in bytes and in entries: "no change" answers weigh nothing, and a key
	// per user, format and profile would otherwise grow without end.
	if c.m == nil || c.bytes+len(out) > subTransformCache || len(c.m) >= subTransformEntries {
		c.m, c.bytes = map[string]cachedTransform{}, 0 // a bound, not an eviction policy
	}
	if old, ok := c.m[key]; ok {
		c.bytes -= len(old.out)
	}
	c.m[key] = cachedTransform{out: out, at: time.Now()}
	c.bytes += len(out)
}

// TransformsSubscription reports whether any active plugin rewrites subscriptions.
func (h *Host) TransformsSubscription() bool {
	return len(h.Active(func(m *manifest.Manifest) bool { return m.Provides.Subscription })) > 0
}

// TransformLinks passes share links through the plugins, in order.
func (h *Host) TransformLinks(ctx context.Context, user map[string]any, links []string) []string {
	in, _ := json.Marshal(links)
	out := h.transform(ctx, user, SubLinks, in, func(raw []byte) bool {
		var got []string
		if json.Unmarshal(raw, &got) != nil || len(got) > 2*len(links)+50 {
			return false
		}
		for _, l := range got {
			if !validLink(l) {
				return false
			}
		}
		return true
	})
	var got []string
	if json.Unmarshal(out, &got) != nil {
		return links
	}
	return got
}

// TransformConfig passes a whole profile through the plugins: a sing-box or Xray
// JSON document, or a Clash YAML text.
func (h *Host) TransformConfig(ctx context.Context, user map[string]any, format, body string) string {
	var in []byte
	if format == SubClash {
		in, _ = json.Marshal(body)
	} else {
		if !json.Valid([]byte(body)) {
			return body
		}
		in = []byte(body)
	}
	out := h.transform(ctx, user, format, in, func(raw []byte) bool { return sameShape(format, in, raw) })
	if format == SubClash {
		var s string
		if json.Unmarshal(out, &s) != nil {
			return body
		}
		return s
	}
	if bytes.Equal(out, in) {
		return body // untouched: the panel's own (indented) text
	}
	return string(out)
}

// subSeed keys the transform cache. The cache lives in this process only, so a
// seeded non-cryptographic hash is enough — and, unlike a SHA-256 of a profile
// that carries the users' passwords, says nothing about it outside the process.
var subSeed = maphash.MakeSeed()

// transform runs the chain: each plugin gets what the one before it returned. A
// plugin's answer is used only when ok accepts it.
func (h *Host) transform(ctx context.Context, user map[string]any, format string, in []byte, ok func([]byte) bool) []byte {
	cur := in
	for _, id := range h.Active(func(m *manifest.Manifest) bool { return m.Provides.Subscription }) {
		key := fmt.Sprintf("%s/%v/%s/%016x", id, user["id"], format, maphash.Bytes(subSeed, cur))
		if out, hit := h.subTransforms.get(key); hit {
			if out != nil {
				cur = out
			}
			continue
		}
		if h.skipFailing("transformSubscription/" + id) {
			continue
		}
		field := "config"
		if format == SubLinks {
			field = "links"
		}
		arg := fmt.Appendf(nil, `{"user":%s,"format":%q,%q:%s}`, mustJSON(user), format, field, cur)
		raw, err := h.call(ctx, id, "transformSubscription", json.RawMessage(arg), subTransformTimeout,
			callOpts{readOnly: true, wait: subTransformTimeout})
		h.noteResult("transformSubscription/"+id, err)
		var out []byte
		if err == nil {
			out = pick(raw, field)
			if out != nil && (len(out) > maxSubOutput || !ok(out)) {
				if inst, e := h.get(id); e == nil {
					inst.logf("warn", "transformSubscription: the %s answer is not a valid %s profile, the panel's own is served", format, format)
				}
				out = nil
			}
		}
		if err == nil { // a failed call is not remembered: the next refresh asks again
			h.subTransforms.put(key, out)
		}
		if out != nil {
			cur = out
		}
	}
	return cur
}

// pick reads the answer: {links: […]} / {config: …}, or the value itself; nil when
// the plugin returned nothing (no change).
func pick(raw json.RawMessage, field string) []byte {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) == nil {
		if v, ok := obj[field]; ok {
			return bytes.TrimSpace(v)
		}
	}
	return raw
}

// sameShape holds an answer to the input's form: Clash stays a YAML text with its
// proxies, sing-box an object with outbounds, Xray what it was (one config or a list).
func sameShape(format string, in, out []byte) bool {
	switch format {
	case SubClash:
		var s string
		return json.Unmarshal(out, &s) == nil && strings.Contains(s, "proxies:")
	case SubSingBox:
		var doc struct {
			Outbounds []json.RawMessage `json:"outbounds"`
		}
		return json.Unmarshal(out, &doc) == nil && len(doc.Outbounds) > 0
	default:
		return len(in) > 0 && len(out) > 0 && in[0] == out[0] && json.Valid(out)
	}
}

// validLink is a share link a client can import: a URL with a proxy scheme.
func validLink(s string) bool {
	if len(s) == 0 || len(s) > maxLinkLen || strings.ContainsAny(s, "\r\n") {
		return false
	}
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	switch u.Scheme {
	case "vless", "vmess", "trojan", "ss", "hysteria2", "hy2", "tuic", "wireguard", "wg", "socks", "http", "https":
		return true
	}
	return false
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("null")
	}
	return b
}
