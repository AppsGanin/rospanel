package link

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
)

// CustomLabel is the node name a custom inbound shows in the client: the operator's
// own name for it, prefixed with the server label on a multi-node install so two
// servers' inbounds never read as one.
func CustomLabel(in model.Inbound, set *model.Settings) string {
	if set != nil && set.NodeLabel != "" {
		return set.NodeLabel + " · " + in.Name
	}
	return in.Name
}

// Custom builds the share link for one operator-defined inbound, or "" for a
// protocol that has no link form. The three protocols each map to their own scheme
// and credential: VLESS carries the UUID, Trojan and Hysteria2 the password.
func Custom(u model.User, in model.Inbound, set *model.Settings) string {
	switch in.Protocol {
	case model.InbVLESS:
		return customVLESS(u, in, set)
	case model.InbTrojan:
		return customTrojan(u, in, set)
	case model.InbHysteria:
		return customHysteria(u, in, set)
	case model.InbShadowsocks:
		return customShadowsocks(u, in, set)
	}
	return ""
}

// customAssemble is assemble() for a custom inbound: same shape, but the label is
// the inbound's own name rather than a protocol constant.
func customAssemble(scheme, cred string, in model.Inbound, q url.Values, set *model.Settings) string {
	return fmt.Sprintf("%s://%s@%s:%d?%s#%s",
		scheme, cred, set.Host, in.Port, q.Encode(), url.PathEscape(CustomLabel(in, set)))
}

// transportParams writes the transport-specific query parameters (the ones that tell
// the client HOW to frame its traffic) shared by VLESS and Trojan links.
func transportParams(q url.Values, in model.Inbound, set *model.Settings) {
	o := in.Opts
	q.Set("type", o.Transport)
	host := o.Host
	if host == "" {
		host = linkSNI(in, set)
	}
	switch o.Transport {
	case model.TrTCP:
		// Raw TCP carries no parameters unless the HTTP masquerade is on, in which
		// case the client must reproduce the same framing or the server won't
		// recognize the connection.
		if o.HeaderType == "http" {
			q.Set("headerType", "http")
			q.Set("host", strings.Join(o.HeaderHosts, ","))
			q.Set("path", strings.Join(o.HeaderPathsOr(), ","))
		}
	case model.TrWS, model.TrHTTPUpgrade:
		q.Set("path", o.Path)
		q.Set("host", host)
	case model.TrXHTTP:
		q.Set("path", o.Path)
		q.Set("host", host)
		if o.Mode != "" {
			q.Set("mode", o.Mode)
		}
		// The advanced block, handed over verbatim. Xray's client reads `extra` as a
		// complete XHTTP config exactly as the inbound does, so the same stored object
		// configures both ends and there is no mapping to fall out of step. Only
		// Xray-core clients understand it; mihomo and sing-box get the basic form (see
		// model.SupportsClash / SupportsSingBox).
		if len(o.XHTTPExtra) > 0 {
			q.Set("extra", string(o.XHTTPExtra))
		}
	case model.TrGRPC:
		q.Set("serviceName", o.ServiceName)
		if o.MultiMode {
			q.Set("mode", "multi")
		} else {
			q.Set("mode", "gun")
		}
		if o.Authority != "" {
			q.Set("authority", o.Authority)
		}
	}
}

// securityParams writes the security-specific query parameters. "none" writes
// security=none and nothing else — the client must not be told to verify a
// certificate that isn't being presented.
func securityParams(q url.Values, in model.Inbound, set *model.Settings) {
	o := in.Opts
	switch o.Security {
	case model.SecTLS:
		q.Set("security", "tls")
		q.Set("sni", linkSNI(in, set))
		q.Set("fp", o.FPOr())
		if alpn := linkALPN(o.Transport); alpn != "" {
			q.Set("alpn", alpn)
		}
		pinSelfSigned(q, set)
	case model.SecReality:
		q.Set("security", "reality")
		q.Set("sni", o.RealitySNI())
		q.Set("fp", o.FPOr())
		q.Set("pbk", o.RealityPublicKey)
		if ids := o.RealityShortIDs(); len(ids) > 0 {
			q.Set("sid", ids[0])
		}
		q.Set("spx", "/")
	default:
		q.Set("security", "none")
	}
}

// linkSNI is the server name a TLS-secured custom inbound presents: its own override
// when set, else the server's.
func linkSNI(in model.Inbound, set *model.Settings) string {
	if in.Opts.SNI != "" {
		return in.Opts.SNI
	}
	return set.SNI
}

// linkALPN mirrors the ALPN the inbound actually offers (see xray.customALPN), so
// the client's ClientHello matches what the server will accept instead of proposing
// a protocol the transport can't carry.
func linkALPN(transport string) string {
	switch transport {
	case model.TrWS, model.TrHTTPUpgrade:
		return "http/1.1"
	case model.TrGRPC:
		return "h2"
	case model.TrTCP, model.TrXHTTP:
		return "h2,http/1.1"
	}
	return ""
}

// customVLESS builds a vless:// link for a custom inbound.
func customVLESS(u model.User, in model.Inbound, set *model.Settings) string {
	q := url.Values{}
	q.Set("encryption", "none")
	securityParams(q, in, set)
	transportParams(q, in, set)
	if in.Opts.Flow != "" {
		q.Set("flow", in.Opts.Flow)
	}
	return customAssemble("vless", u.UUID, in, q, set)
}

// customTrojan builds a trojan:// link for a custom inbound.
func customTrojan(u model.User, in model.Inbound, set *model.Settings) string {
	q := url.Values{}
	securityParams(q, in, set)
	transportParams(q, in, set)
	return customAssemble("trojan", url.QueryEscape(u.Password), in, q, set)
}

// customHysteria builds a hysteria2:// link for a custom inbound. Same Xray/Happ
// dialect as the built-in lane (see Hysteria2), including the double-encoded fm
// parameter that carries port-hopping.
func customHysteria(u model.User, in model.Inbound, set *model.Settings) string {
	q := url.Values{}
	q.Set("type", "hysteria")
	q.Set("security", "tls")
	q.Set("sni", linkSNI(in, set))
	q.Set("alpn", "h3")
	pinSelfSigned(q, set)
	if in.UsesHopping() {
		q.Set("fm", url.QueryEscape(hopParams(model.HopAdvertised(in.Port, in.Opts.HopStart), in.Opts.HopEnd, in.Opts.HopIntervalOr())))
	}
	return customAssemble("hysteria2", url.QueryEscape(u.Password), in, q, set)
}

// customShadowsocks builds an ss:// link for a Shadowsocks-2022 inbound, in the
// SIP002 form that mihomo, sing-box, v2rayN, Shadowrocket and Streisand parse:
//
//	ss://base64url(method:serverKey:userKey)@host:port#label
//
// The userinfo is the whole "method:serverKey:userKey" triple, base64url without
// padding — the multi-user 2022 shape, where the password is the server key and the
// user key joined by a colon. It is NOT percent-encoded like the other schemes: the
// credential is the base64 blob itself, and a client splits it on the first colon.
func customShadowsocks(u model.User, in model.Inbound, set *model.Settings) string {
	o := in.Opts
	userKey := model.UserShadowKey(u.UUID, o.Method)
	userinfo := base64.RawURLEncoding.EncodeToString(
		[]byte(o.Method + ":" + o.ShadowKey + ":" + userKey))
	return fmt.Sprintf("ss://%s@%s:%d#%s",
		userinfo, set.Host, in.Port, url.PathEscape(CustomLabel(in, set)))
}
