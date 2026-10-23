package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/plugin"
	"github.com/AppsGanin/rospanel/internal/plugin/manifest"
	"github.com/AppsGanin/rospanel/internal/status"
	"github.com/AppsGanin/rospanel/internal/sub"
	"github.com/AppsGanin/rospanel/internal/telegram"
)

// buildAccess fills the page's "access to your account" card: the page's own address
// is the key to it, and Telegram can be linked — or changed to another one.
func (rt *Router) buildAccess(r *http.Request, u model.User, set *model.Settings) sub.Access {
	var a sub.Access
	lang := i18n.FromAcceptLanguage(r.Header.Get("Accept-Language"))
	if set.SubSelfRotateHours > 0 {
		a.RotatePath = sub.URL(set, u.SubToken) + "/rotate"
		a.RotateWait = i18n.T(lang, "sub.rotateWait", i18n.Every(lang, set.SubSelfRotateHours))
	}
	terms, privacy := rt.legalLinks(set)
	if terms != "" {
		a.Legal = append(a.Legal, sub.LegalLink{Title: sub.LegalTitle(model.LegalTerms, lang), URL: terms})
	}
	if privacy != "" {
		a.Legal = append(a.Legal, sub.LegalLink{Title: sub.LegalTitle(model.LegalPrivacy, lang), URL: privacy})
	}
	if rt.mgr.ChurnSurveyOpen(u) {
		a.Churn = sub.NewChurn(sub.URL(set, u.SubToken)+"/churn", lang)
	}
	if rt.plugins != nil {
		if t, ok := rt.plugins.Theme(); ok {
			a.ThemeCSS = sub.URL(set, u.SubToken) + "/theme/" + manifest.ThemeCSS + "?v=" + t.Version
		}
	}
	for _, b := range rt.pluginBlocks(r.Context(), u, lang) {
		if blk, ok := sub.NewBlock(b.Type, b.Text, b.Label, b.URL); ok {
			a.Blocks = append(a.Blocks, blk)
		}
	}
	linked := u.TgChatID != 0
	if !set.TGUserBotEnabled || (linked && !set.SubTGRebind) || (!linked && !set.SubTGBind) {
		return a
	}
	// Moved too recently: the page says how often it may, rather than offering a
	// button the bot would refuse.
	if every := int64(set.SubTGRebindHours) * 3600; linked && every > 0 {
		if at, err := rt.mgr.Store().TGChangedAt(u.ID); err == nil && time.Now().Unix()-at < every {
			a.TGWait = i18n.T(lang, "sub.tgChangeWait", i18n.Every(lang, set.SubTGRebindHours))
			return a
		}
	}
	bot := botUsername(r.Context(), set.TGUserBotToken, set.TelegramProxyURL())
	if bot == "" {
		return a
	}
	code, err := rt.mgr.UserTgBindCode(u)
	if err != nil {
		return a
	}
	a.TGLink, a.TGLinked = telegram.UserDeepLink(bot, code), linked
	return a
}

// pluginBlocks asks the plugins what to add to this user's page (each within a
// fraction of a second, cached for minutes — see plugin.Host.SubBlocks).
func (rt *Router) pluginBlocks(ctx context.Context, u model.User, lang i18n.Lang) []plugin.SubBlock {
	if rt.plugins == nil {
		return nil
	}
	return rt.plugins.SubBlocks(ctx, pluginUser(u), string(lang))
}

// pluginUser is the user as plugins see them on the subscription paths.
func pluginUser(u model.User) map[string]any {
	return map[string]any{
		"id": u.ID, "name": u.Name, "status": u.Status, "expire_at": u.ExpireAt, "plan_id": u.PlanID,
		"data_limit": u.DataLimit, "used": u.UsedUp + u.UsedDown, "telegram_id": u.TgChatID,
	}
}

// pluginConfig passes a served profile through the plugins that rewrite them.
func (rt *Router) pluginConfig(ctx context.Context, u model.User, format, body string) string {
	if rt.plugins == nil || !rt.plugins.TransformsSubscription() {
		return body
	}
	return rt.plugins.TransformConfig(ctx, pluginUser(u), format, body)
}

// pluginLinks is pluginConfig for share links.
func (rt *Router) pluginLinks(ctx context.Context, u model.User, links []string) []string {
	if rt.plugins == nil || !rt.plugins.TransformsSubscription() {
		return links
	}
	return rt.plugins.TransformLinks(ctx, pluginUser(u), links)
}

// subNoticeBodyMax keeps a notice to a few lines: the page is about the reader's
// subscription, and the whole story is a link away.
const subNoticeBodyMax = 280

// subNotices puts the status page's current incidents and soon-due maintenance on
// the subscription page — the ones about a server the reader can use, or about the
// whole service.
func (rt *Router) subNotices(a *sub.Access, set *model.Settings, servers []sub.Server, lang i18n.Lang) {
	list, err := rt.mgr.SubPageIncidents()
	if err != nil || len(list) == 0 {
		return
	}
	reached := map[int64]bool{}
	for _, s := range servers {
		if s.Reaches() {
			reached[s.Set.ServerID] = true
		}
	}
	loc, now := rt.mgr.Location(), time.Now().Unix()
	for i := range list {
		in := &list[i]
		if !concernsAny(in, reached) {
			continue
		}
		n := sub.Notice{Kind: in.Kind, Title: in.Title, Body: shorten(in.Body, subNoticeBodyMax), NodeIDs: in.NodeIDs}
		if in.Phase(now) == model.IncidentScheduled {
			n.Scheduled = i18n.T(lang, "status.phaseScheduled")
		}
		if in.Kind == model.IncidentKindMaintenance {
			n.Label = i18n.T(lang, "status.kindMaintenance")
			n.When = status.Window(lang, loc, in.StartsAt, in.EndsAt)
		} else {
			n.Label = i18n.T(lang, "status.kindIncident")
			n.When = i18n.T(lang, "status.since", status.Stamp(lang, time.Unix(in.StartsAt, 0).In(loc)))
		}
		a.Notices = append(a.Notices, n)
	}
	a.NoticesLabel = i18n.T(lang, "status.title")
	if len(a.Notices) > 0 && set.StatusEnabled {
		a.StatusURL = "/" + set.StatusPathOr()
		a.StatusMore = i18n.T(lang, "sub.statusMore")
	}
}

// concernsAny reports whether an incident is about the whole service or names a
// server in reached.
func concernsAny(in *model.Incident, reached map[int64]bool) bool {
	if len(in.NodeIDs) == 0 {
		return true
	}
	for _, id := range in.NodeIDs {
		if reached[id] {
			return true
		}
	}
	return false
}

// shorten cuts s to at most max runes, ending with an ellipsis when it did.
func shorten(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[:max-1])) + "…"
}
