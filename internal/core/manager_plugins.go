package core

import (
	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
)

// NotifyPluginPaused tells the admin chats a plugin was paused and why.
func (m *Manager) NotifyPluginPaused(plugin, reason string) {
	m.notifyAdminEvent(model.AdminEventPlugins, i18n.T(m.botLang(), "notify.pluginPaused", escHTML(plugin), escHTML(reason)))
}

// NotifyPluginUpdate tells the admins a newer version of an installed plugin is in
// the catalog. Updating stays their decision.
func (m *Manager) NotifyPluginUpdate(name, installed, latest string) {
	m.notifyAdminEvent(model.AdminEventPlugins, i18n.T(m.botLang(), "notify.pluginUpdate", escHTML(name), escHTML(installed), escHTML(latest)))
}
