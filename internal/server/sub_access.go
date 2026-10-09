package server

import (
	"context"
	"net/http"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/plugin"
	"github.com/AppsGanin/rospanel/internal/plugin/manifest"
	"github.com/AppsGanin/rospanel/internal/sub"
	"github.com/AppsGanin/rospanel/internal/telegram"
)

// buildAccess fills the page's "access to your account" card: the page's own address
// is the key to it, and Telegram can be linked — or changed to another one.
func (rt *Router) buildAccess(r *http.Request, u model.User, set *model.Settings) sub.Access {
	var a sub.Access
	lang := i18n.FromAcceptLanguage(r.Header.Get("Accept-Language"))
	terms, privacy := rt.legalLinks(set)
	if terms != "" {
		a.Legal = append(a.Legal, sub.LegalLink{Title: sub.LegalTitle(model.LegalTerms, lang), URL: terms})
	}
	if privacy != "" {
		a.Legal = append(a.Legal, sub.LegalLink{Title: sub.LegalTitle(model.LegalPrivacy, lang), URL: privacy})
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
