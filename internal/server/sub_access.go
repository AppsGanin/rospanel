package server

import (
	"net/http"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
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
