package core

import (
	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
)

// NotifyPluginPaused tells the admin chats a plugin stopped and why; retrying,
// that the panel brings it back on its own.
func (m *Manager) NotifyPluginPaused(plugin, reason string, retrying bool) {
	key := "notify.pluginPaused"
	if retrying {
		key = "notify.pluginRetrying"
	}
	m.notifyAdminEvent(model.AdminEventPlugins, i18n.T(m.botLang(), key, escHTML(plugin), escHTML(reason)))
}
