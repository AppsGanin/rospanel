package core

import (
	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
)

// NotifyPluginPaused tells the admin chats a plugin was paused and why.
func (m *Manager) NotifyPluginPaused(plugin, reason string) {
	m.notifyAdminEvent(model.AdminEventPlugins, i18n.T(m.botLang(), "notify.pluginPaused", escHTML(plugin), escHTML(reason)))
}
