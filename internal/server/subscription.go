package server

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/AppsGanin/rospanel/internal/branding"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/sub"
	"github.com/AppsGanin/rospanel/internal/telegram"
	qrcode "github.com/skip2/go-qrcode"
)

// handleSub serves the public subscription surface at /sub/<token>[/page|/qr.png].
// An invalid/unknown token falls through to the decoy — indistinguishable from a
// normal site's 404, so the surface never confirms a token's (non)existence.
func handleSub(rt *Router, w http.ResponseWriter, r *http.Request, rest string) {
	parts := strings.SplitN(strings.TrimPrefix(rest, "/"), "/", 2)
	token := parts[0]
	leaf := ""
	if len(parts) == 2 {
		leaf = parts[1]
	}

	u, err := rt.mgr.Store().GetUserBySubToken(token)
	if err != nil {
		rt.decoy.ServeHTTP(w, r)
		return
	}
	set, err := rt.mgr.Store().GetSettings()
	if err != nil {
		// Never surface an internal error on the public subscription surface — a
		// real static site wouldn't 500 with a JSON body, which would confirm the
		// panel is here and leak the error text. Fall through to the decoy instead.
		rt.decoy.ServeHTTP(w, r)
		return
	}
	rt.applyTLSHints(set)

	switch leaf {
	case "":
		// A real browser (Accept: text/html) gets the human page; a proxy client
		// gets the machine payload.
		if isBrowser(r) {
			if err := servePage(w, *u, set); err != nil {
				rt.decoy.ServeHTTP(w, r) // keep the masquerade intact on render errors
			}
			return
		}
		// Machine payload, format chosen by the client (User-Agent or ?format=).
		supportURL := rt.telegramSupportURL(r.Context(), set, *u)
		setSubHeaders(w, *u, set, supportURL)
		rt.setRoutingHeaders(w, r, set)
		switch subFormat(r) {
		case "clash":
			// Mihomo/Clash ignores the routing header — inject the routing rules
			// straight into the YAML by merging proxies into the template.
			body := sub.ClashYAML(*u, set)
			if set.SubRouting && strings.TrimSpace(set.SubRoutingMihomo) != "" {
				if tpl, err := rt.mgr.FetchRoutingTemplate(set.SubRoutingMihomo); err == nil {
					body = sub.ClashWithTemplate(*u, set, tpl)
				}
			}
			w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
			_, _ = w.Write([]byte(body))
		case "singbox", "sing-box":
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = w.Write([]byte(sub.SingBoxJSON(*u, set)))
		default:
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			links := sub.ShareLinks(*u, set)
			var body string
			if set.SubBase64 {
				body = sub.Base64Payload(links)
			} else {
				body = strings.Join(links, "\n")
				if supportURL != "" {
					body = "#support-url: " + supportURL + "\n" + body
				}
			}
			_, _ = w.Write([]byte(body))
		}

	case "logo.svg":
		// Honour a custom branding logo on the public subscription page too; falls
		// back to the built-in mark when none is set.
		b, err := branding.ReadLogo(rt.dataDir)
		if err != nil {
			b = sub.Logo()
		}
		w.Header().Set("Content-Type", branding.LogoContentType(b))
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(b)

	case "qr.png":
		png, err := qrcode.Encode(sub.URL(set, u.SubToken), qrcode.Medium, 512)
		if err != nil {
			rt.decoy.ServeHTTP(w, r) // keep the masquerade intact on internal errors
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(png)

	default:
		rt.decoy.ServeHTTP(w, r)
	}
}

// isBrowser reports whether the request looks like a web browser (so we serve
// the human page instead of the machine subscription payload).
func isBrowser(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/html")
}

// servePage renders the human-facing subscription page. It returns an error
// (writing nothing) instead of a 500 so the caller can fall through to the decoy
// and keep the masquerade intact.
func servePage(w http.ResponseWriter, u model.User, set *model.Settings) error {
	html, err := sub.Page(u, set)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(html)
	return nil
}

// telegramSupportURL is the Happ/INCY support-url (Telegram user bot deep link),
// or "" when the user bot is disabled or its @username can't be resolved.
func (rt *Router) telegramSupportURL(ctx context.Context, set *model.Settings, u model.User) string {
	if !set.TGUserBotEnabled {
		return ""
	}
	bot := botUsername(ctx, set.TGUserBotToken)
	if bot == "" {
		return ""
	}
	// Already linked: just point at the bot (no bind needed). Otherwise mint a
	// fresh one-time bind code so the subscription page's link binds this account.
	if u.TgChatID != 0 {
		return telegram.UserBotLink(bot)
	}
	code, err := rt.mgr.GenerateUserTgLinkCode(u.ID)
	if err != nil {
		return telegram.UserBotLink(bot)
	}
	return telegram.UserDeepLink(bot, code)
}

// setSubHeaders adds the standard subscription headers every client reads:
// title, update interval, usage/quota/expiry, profile web page, and Happ support-url.
func setSubHeaders(w http.ResponseWriter, u model.User, set *model.Settings, supportURL string) {
	title := sub.SubTitle(u, set)
	// Go canonicalizes header keys on the wire and clients match case-insensitively
	// (RFC 7230), so a single canonical "Profile-Title" suffices — a second
	// lowercase Set() would just overwrite this with the same value.
	w.Header().Set("Profile-Title", "base64:"+base64.StdEncoding.EncodeToString([]byte(title)))
	// 0 = never: omit the header so clients don't auto-refresh.
	if set.SubUpdateInterval > 0 {
		w.Header().Set("Profile-Update-Interval", strconv.Itoa(set.SubUpdateInterval))
	}
	// used = upload+download, total = limit; 0 means unlimited / never.
	w.Header().Set("Subscription-Userinfo", fmt.Sprintf(
		"upload=%d; download=%d; total=%d; expire=%d",
		u.UsedUp, u.UsedDown, u.DataLimit, u.ExpireAt))
	w.Header().Set("Profile-Web-Page-Url", sub.URL(set, u.SubToken))
	if supportURL != "" {
		// Happ shows it as a support button on the traffic bar; Telegram links get
		// the TG icon. Points the client at the public user bot.
		w.Header().Set("support-url", supportURL)
	}
	w.Header().Set("Cache-Control", "no-store")
}

// setRoutingHeaders attaches the RoscomVPN-style auto-routing headers honored by
// Happ / INCY: "routing" carries the actual deeplink (happ:// / incy://) — the
// fetched content of the configured URL, NOT the URL itself — and
// "routing-enable" turns it on. The deeplink source is chosen by User-Agent.
func (rt *Router) setRoutingHeaders(w http.ResponseWriter, r *http.Request, set *model.Settings) {
	if !set.SubRouting {
		return
	}
	ua := strings.ToLower(r.Header.Get("User-Agent"))
	url := set.SubRoutingHapp // default to the Happ profile
	switch {
	case strings.Contains(ua, "incy"):
		url = set.SubRoutingIncy
	case strings.Contains(ua, "clash"), strings.Contains(ua, "mihomo"),
		strings.Contains(ua, "meta"), strings.Contains(ua, "stash"):
		// Clash/Mihomo gets its rules injected into the YAML, not via a header.
		return
	}
	if strings.TrimSpace(url) == "" {
		return
	}
	deeplink, err := rt.mgr.FetchRoutingTemplate(url)
	if err != nil {
		return
	}
	w.Header().Set("routing", strings.TrimSpace(deeplink))
	w.Header().Set("routing-enable", "true")
}

// subFormat picks the subscription format: an explicit ?format= wins, otherwise
// Clash-family clients (by User-Agent) get YAML; everyone else gets the
// universal base64 v2ray list.
func subFormat(r *http.Request) string {
	if f := strings.ToLower(r.URL.Query().Get("format")); f != "" {
		return f
	}
	ua := strings.ToLower(r.Header.Get("User-Agent"))
	for _, k := range []string{"clash", "mihomo", "stash", "meta"} {
		if strings.Contains(ua, k) {
			return "clash"
		}
	}
	// Official sing-box apps (SFA/SFI/SFM/SFT) want a full sing-box config.
	for _, k := range []string{"sing-box", "sfa/", "sfi/", "sfm/", "sft/"} {
		if strings.Contains(ua, k) {
			return "singbox"
		}
	}
	return "v2ray"
}
