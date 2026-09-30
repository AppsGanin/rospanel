package server

import (
	"net/http"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/sub"
)

// buildAccess fills the page's "access to your account" card: the page's own address
// is the key to it, and Telegram can be linked for whoever has not yet.
func (rt *Router) buildAccess(r *http.Request, u model.User, set *model.Settings) sub.Access {
	var a sub.Access
	if u.TgChatID == 0 {
		a.TGLink = rt.telegramSupportURL(r.Context(), set, u)
	}
	return a
}
