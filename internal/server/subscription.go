package server

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/actor"
	"github.com/AppsGanin/rospanel/internal/branding"
	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/sub"
	"github.com/AppsGanin/rospanel/internal/telegram"
	qrcode "github.com/skip2/go-qrcode"
)

// subActorCtx marks the request as the VPN user acting on their own account, so the
// audit log distinguishes self-service (cancelling a plan, paying from the
// subscription page) from an admin doing it for them. The sub token already
// authenticated them.
func subActorCtx(r *http.Request, u model.User) context.Context {
	return actor.With(r.Context(), actor.UserSelf(u.Name))
}

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
		// gets the machine payload. An explicit ?format= forces the machine payload
		// even from a browser — that's how the page's own "download Clash config"
		// button fetches YAML from this very URL instead of re-rendering the page.
		if isBrowser(r) && r.URL.Query().Get("format") == "" {
			lang := i18n.FromAcceptLanguage(r.Header.Get("Accept-Language"))
			if err := rt.servePage(w, *u, set, lang); err != nil {
				rt.decoy.ServeHTTP(w, r) // keep the masquerade intact on render errors
			}
			return
		}
		// Machine payload, format chosen by the client (User-Agent or ?format=).
		// allServers spans the local server plus each enabled, connected node, so the
		// payload carries one entry per protocol × server (single-server = local only).
		allServers := rt.subServers(set, u.ID)
		supportURL := rt.telegramSupportURL(r.Context(), set, *u)
		setSubHeaders(w, *u, set, supportURL)
		rt.setRoutingHeaders(w, r, set)
		switch subFormat(r) {
		case "clash":
			// Mihomo/Clash ignores the routing header — inject the routing rules
			// straight into the YAML by merging proxies into the template.
			body := sub.ClashYAMLMulti(*u, allServers)
			if set.SubRouting && strings.TrimSpace(set.SubRoutingMihomo) != "" {
				if tpl, err := rt.mgr.FetchRoutingTemplate(set.SubRoutingMihomo); err == nil {
					body = sub.ClashWithTemplateMulti(*u, allServers, tpl)
				}
			}
			w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
			// ?dl= makes the browser save the YAML as a file (the page's download
			// button) instead of showing it inline; real Clash clients omit it.
			if r.URL.Query().Get("dl") != "" {
				w.Header().Set("Content-Disposition", `attachment; filename="clash.yaml"`)
			}
			_, _ = w.Write([]byte(body))
		case "singbox", "sing-box":
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = w.Write([]byte(sub.SingBoxJSONMulti(*u, allServers)))
		default:
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			links := sub.ShareLinksAll(*u, allServers)
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

	case "tg.js":
		// The Telegram Mini App SDK, proxied through us: the panel fetches it from
		// telegram.org server-side and serves the cached copy from our own origin, so
		// the page never loads it straight from telegram.org (blocked in Russia — a
		// direct <script> there hangs the page until the connection times out).
		//
		// A COLD cache fetches inline (bounded by the manager's budget) so the first
		// visitor still gets the real SDK; if telegram.org is unreachable this serves
		// an EMPTY body instead. The page degrades cleanly on empty: window.Telegram
		// stays undefined, so it treats itself as a plain browser (INTG=false) exactly
		// as it did when telegram.org was loaded directly and blocked.
		//
		// private, not public: the body is the same non-secret SDK for everyone, but
		// the URL embeds the user's sub token — no reason to invite shared proxies to
		// key a cache entry (and a log line) on it. no-store on the empty reply so a
		// client doesn't pin the miss.
		js, ok := rt.mgr.TelegramWebAppSDK()
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		if ok {
			w.Header().Set("Cache-Control", "private, max-age=3600")
		} else {
			w.Header().Set("Cache-Control", "no-store")
		}
		_, _ = w.Write(js)

	case "qr.png":
		png, err := qrcode.Encode(sub.URL(set, u.SubToken), qrcode.Medium, 512)
		if err != nil {
			rt.decoy.ServeHTTP(w, r) // keep the masquerade intact on internal errors
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(png)

	case "pay":
		rt.handleSubPay(w, r, *u, set)

	case "cancel":
		rt.handleSubCancel(w, r, *u, set)

	case "order":
		rt.handleSubOrder(w, r, *u)

	default:
		// /app/<n> — deep-link hand-off page (opened in the external browser from the
		// Telegram Mini App so the client scheme actually launches).
		if idx, ok := strings.CutPrefix(leaf, "app/"); ok {
			rt.handleSubApp(w, r, *u, set, idx)
			return
		}
		// /font/<file>.woff2 — self-hosted Mulish. Served from our own origin instead
		// of Google Fonts, which is throttled in Russia and would delay paint (the
		// stylesheet is render-blocking). Content is build-immutable and named per
		// subset+weight, so it caches for a year; private because the URL carries the
		// user's sub token, same as the other per-token assets.
		if name, ok := strings.CutPrefix(leaf, "font/"); ok {
			b, ok := sub.Font(name)
			if !ok {
				rt.decoy.ServeHTTP(w, r) // unknown font ⇒ behave like any other 404
				return
			}
			w.Header().Set("Content-Type", "font/woff2")
			w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
			_, _ = w.Write(b)
			return
		}
		rt.decoy.ServeHTTP(w, r)
	}
}

// handleSubApp serves the redirect page for one client deep link, chosen by its
// index in the DeepLinks list (kept in sync with the subscription page's modal).
func (rt *Router) handleSubApp(w http.ResponseWriter, r *http.Request, u model.User, set *model.Settings, idxStr string) {
	n, err := strconv.Atoi(idxStr)
	if err != nil {
		rt.decoy.ServeHTTP(w, r)
		return
	}
	lang := i18n.FromAcceptLanguage(r.Header.Get("Accept-Language"))
	links := sub.DeepLinks(sub.URL(set, u.SubToken), lang)
	if n < 0 || n >= len(links) {
		rt.decoy.ServeHTTP(w, r)
		return
	}
	html, err := sub.AppRedirect(links[n].Href, lang)
	if err != nil {
		rt.decoy.ServeHTTP(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(html)
}

// handleSubCancel cancels the user's active paid plan from the subscription page
// (moves them to the free plan immediately). Only acts on an active paid plan, so
// a stray POST on a free/expired account is a no-op success.
func (rt *Router) handleSubCancel(w http.ResponseWriter, r *http.Request, u model.User, set *model.Settings) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	lang := i18n.FromAcceptLanguage(r.Header.Get("Accept-Language"))
	if !set.BillingEnabled {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": i18n.T(lang, "sub.unavailable")})
		return
	}
	if rt.mgr.ActivePaidPlan(u) == nil {
		writeOK(w) // nothing active to cancel
		return
	}
	if err := rt.mgr.CancelUserPlan(subActorCtx(r, u), u.ID); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// handleSubPay starts a plan payment from the subscription page and returns the
// hosted pay URL as JSON, so the page can redirect the user to the provider. The
// token already authenticated the user; the plan is applied later by the provider
// webhook/poll (no Telegram needed). Errors return a plain JSON message instead of
// the decoy — the caller is a verified token holder acting on their own account.
func (rt *Router) handleSubPay(w http.ResponseWriter, r *http.Request, u model.User, set *model.Settings) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	lang := i18n.FromAcceptLanguage(r.Header.Get("Accept-Language"))
	if !set.BillingEnabled {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": i18n.T(lang, "sub.payUnavailable")})
		return
	}
	var req struct {
		PlanID   int64  `json:"plan_id"`
		Provider string `json:"provider"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	// Same "no switching while active" rule as the manager guard, applied here so it
	// also covers the manual branch below (and a hand-crafted request).
	if req.PlanID != u.PlanID {
		if active := rt.mgr.ActivePaidPlan(u); active != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": i18n.T(lang, "sub.alreadySubscribed", active.Name)})
			return
		}
	}
	// No automatic provider passed/configured ⇒ create a manual order and return the
	// payment instructions for the page to show (admin confirms it later).
	if req.Provider == "" && len(rt.mgr.PaymentMethods()) == 0 {
		_, msg, err := rt.mgr.RequestPlanPayment(subActorCtx(r, u), lang, u.ID, req.PlanID)
		if err != nil {
			writeManagerErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"manual": true, "message": msg})
		return
	}
	order, err := rt.mgr.StartPlanPaymentReturn(subActorCtx(r, u), lang, u.ID, req.PlanID, req.Provider, sub.URL(set, u.SubToken))
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"pay_url": order.PayURL})
}

// subPaymentWatchWindow caps how long the page shows "payment processing" for a
// pending order. The server keeps polling and will still apply a late payment; the
// banner just stops after this so it can't hang for hours on an abandoned checkout.
const subPaymentWatchWindow = 30 * time.Minute

// handleSubOrder reports whether the user has an automatic payment still being
// processed (and recent), so the page can show a "payment in progress" state and
// poll until the provider webhook/poll confirms it.
func (rt *Router) handleSubOrder(w http.ResponseWriter, _ *http.Request, u model.User) {
	order, err := rt.mgr.Store().LatestPendingProviderOrder(u.ID)
	if err != nil || order == nil ||
		(order.CreatedAt > 0 && time.Now().Unix()-order.CreatedAt > int64(subPaymentWatchWindow.Seconds())) {
		writeJSON(w, http.StatusOK, map[string]any{"pending": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"pending":   true,
		"order_id":  order.ID,
		"plan_name": order.PlanName,
		"amount":    order.AmountRub,
	})
}

// buildBilling assembles the optional "renew / pay" block for the subscription
// page: the active plan, its paid expiry, and the paid tariffs the user can buy or
// extend. Returns a zero (hidden) block unless billing is on with at least one
// enabled paid plan.
func (rt *Router) buildBilling(u model.User, set *model.Settings, lang i18n.Lang) sub.Billing {
	if !set.BillingEnabled {
		return sub.Billing{}
	}
	// No plan attached at all (an admin-provisioned user, not a billing customer):
	// show neither tariffs nor a pay button. Billing may still be on for the users
	// who registered through the bot and did get a plan.
	if u.PlanID == 0 {
		return sub.Billing{}
	}
	plans, err := rt.mgr.ListTariffPlans(false)
	if err != nil {
		return sub.Billing{}
	}
	subURL := sub.URL(set, u.SubToken)
	b := sub.Billing{
		Show:        true,
		CurrentPlan: rt.mgr.PlanName(u.PlanID),
		PayPath:     subURL + "/pay",
		CancelPath:  subURL + "/cancel",
		OrderPath:   subURL + "/order",
		Note:        strings.TrimSpace(set.BillingPaymentNote),
	}
	if u.ExpireAt > 0 {
		b.ExpireText = i18n.T(lang, "sub.until", time.Unix(u.ExpireAt, 0).Format("02.01.2006"))
	}
	// While a paid plan is active, switching is blocked: offer only that plan
	// (renewal) plus cancellation. Otherwise offer every paid plan to buy.
	if active := rt.mgr.ActivePaidPlan(u); active != nil {
		b.Locked = true
		b.Cancelable = true
		b.Plans = []sub.BillingPlan{{
			ID: active.ID, Name: active.Name, Label: payPlanLabel(lang, *active), Current: true,
		}}
	} else {
		for _, p := range plans {
			if p.IsFree() {
				continue // paid plans only (no free/trial self-select)
			}
			b.Plans = append(b.Plans, sub.BillingPlan{
				ID: p.ID, Name: p.Name, Label: payPlanLabel(lang, p), Current: p.ID == u.PlanID,
			})
		}
	}
	for _, m := range rt.mgr.PaymentMethods() {
		b.Providers = append(b.Providers, sub.BillingPay{Key: m, Label: rt.mgr.ProviderLabel(m)})
	}
	// No automatic provider ⇒ manual payment: the pay button still works, creating a
	// pending order and showing instructions (admin confirms it).
	b.Manual = len(b.Providers) == 0
	// Hide only when there's truly nothing to do: no plans to buy/renew and no active
	// plan to cancel.
	if len(b.Plans) == 0 && !b.Cancelable {
		return sub.Billing{}
	}
	return b
}

// payPlanLabel renders a paid plan's price/period, e.g. "199 ₽ / 30 d".
func payPlanLabel(lang i18n.Lang, p model.TariffPlan) string {
	if p.PeriodDays > 0 {
		return i18n.T(lang, "sub.pricePeriod", p.PriceRub, i18n.TN(lang, "sub.periodDays", p.PeriodDays))
	}
	return i18n.T(lang, "sub.price", p.PriceRub)
}

// isBrowser reports whether the request looks like a web browser (so we serve
// the human page instead of the machine subscription payload).
func isBrowser(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/html")
}

// servePage renders the human-facing subscription page. It returns an error
// (writing nothing) instead of a 500 so the caller can fall through to the decoy
// and keep the masquerade intact.
// lang comes from the caller's Accept-Language: the subscription page is the one
// surface a VPN *user* sees, and the panel knows nothing about their language
// preference, so the browser decides it per request.
func (rt *Router) servePage(w http.ResponseWriter, u model.User, set *model.Settings, lang i18n.Lang) error {
	// Span the local server + each enabled node so the page's individual-config list
	// covers every server (single-server ⇒ just the local set).
	html, err := sub.Page(u, rt.subServers(set, u.ID), rt.buildBilling(u, set, lang), lang)
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
	// The operator's announcement, rendered as a line inside the client itself (Happ,
	// v2RayTun). base64 is not optional: HTTP header values are ASCII, and the text is
	// Cyrillic — raw UTF-8 bytes survive Go but get mangled by a proxy in front. Every
	// panel that ships this header base64s it unconditionally, and clients expect the
	// "base64:" prefix.
	if a := strings.TrimSpace(set.SubAnnounce); a != "" {
		w.Header().Set("Announce", "base64:"+base64.StdEncoding.EncodeToString([]byte(a)))
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
