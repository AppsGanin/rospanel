package model

// The admin audit trail: what was done to the panel itself — the admin roster, the
// settings, TLS, backups — and by whom, from where.
//
// Deliberately not UserEvent: that log is user-scoped (every row hangs off a user
// id). These events have no user. The two stay separate rather than one growing a
// nullable column, so neither journal has to explain the other's empty fields.
//
// Note the Audit* prefix: AdminEvent* is already taken by the Telegram notification
// categories, which are a different thing entirely.

// AdminAudit is one row of the admin trail.
type AdminAudit struct {
	ID        int64  `json:"id"`
	Action    string `json:"action"` // one of the Audit* keys below
	Target    string `json:"target"` // what it was done TO (an admin login, a key name); "" when the action says it all
	ActorKind string `json:"actor_kind"`
	ActorName string `json:"actor_name"` // admin login; "" for an anonymous failed sign-in
	IP        string `json:"ip"`         // where it came from — the whole point of a sign-in row
	Details   any    `json:"details"`    // decoded JSON object, nil when the row carried none
	CreatedAt int64  `json:"created_at"`
}

// AdminAuditRetentionDays matches the user journal's window.
const AdminAuditRetentionDays = 90

// Audit action keys. Stable strings persisted in admin_audit.action — never renamed
// once shipped, or old rows lose their label.
const (
	// Sessions.
	AuditLogin       = "admin.login"
	AuditLoginFailed = "admin.login_failed"
	AuditLogout      = "admin.logout"

	// The roster.
	AuditAdminCreated       = "admin.created"
	AuditAdminDeleted       = "admin.deleted"
	AuditAdminRoleChanged   = "admin.role_changed"
	AuditAdminPasswordReset = "admin.password_reset"

	// Your own account.
	AuditPasswordChanged    = "admin.password_changed"
	AuditCredentialsChanged = "admin.credentials_changed"

	// Settings — one action for all of them. Which section was touched goes in the
	// row's Target ("Маршрутизация", "DNS", …), not into a key of its own: a filter
	// with twenty near-identical entries is a filter nobody uses, and the answer the
	// owner wants ("who has been changing settings?") is one row type, not twenty.
	AuditSettings = "settings.changed"

	// Tariff plans.
	AuditPlanSaved    = "plan.saved"
	AuditPlanDeleted  = "plan.deleted"
	AuditPlanMigrated = "plan.migrated"

	// The API surface.
	AuditAPIKeyCreated  = "apikey.created"
	AuditAPIKeyRevoked  = "apikey.revoked"
	AuditWebhookCreated = "webhook.created"
	AuditWebhookUpdated = "webhook.updated"
	AuditWebhookDeleted = "webhook.deleted"

	// Mass broadcasts. Kept as their own actions rather than folded into
	// AuditSettings: "who sent a message to every user, and what was in it" is the
	// question this journal exists to answer, and it must not need reading a
	// settings-changed row to find.
	AuditBroadcastStarted = "broadcast.started"
	AuditBroadcastChanged = "broadcast.changed"
	AuditBroadcastTest    = "broadcast.test"

	// The panel itself.
	AuditXrayRestarted = "panel.xray_restarted"
	AuditStatsReset    = "panel.stats_reset"
	AuditBackupTaken   = "panel.backup_downloaded"
	AuditRestored      = "panel.restored"
	AuditFactoryReset  = "panel.factory_reset"
	AuditUpdated       = "panel.updated"
)

// Audit categories. What the journal is FILTERED by — a handful of areas instead of
// two dozen near-identical actions.
//
// The actions themselves stay precise: "администратор удалён" and "администратор
// создан" are not the same event, and folding them into one key to shorten a
// dropdown would throw away the only thing the row is for. So the filter is unified,
// not the events: pick an area, read the exact action on each row.
const (
	AuditCatSession  = "session"
	AuditCatAdmins   = "admins"
	AuditCatSettings = "settings"
	AuditCatPlans    = "plans"
	AuditCatAPI       = "api"
	AuditCatBroadcast = "broadcast"
	AuditCatPanel     = "panel"
)

// AdminAuditCategories is the filter's list, in the order it renders.
var AdminAuditCategories = []struct{ Key, Label string }{
	{AuditCatSession, "Входы"},
	{AuditCatAdmins, "Администраторы"},
	{AuditCatSettings, "Настройки"},
	{AuditCatPlans, "Тарифы"},
	{AuditCatAPI, "API и вебхуки"},
	{AuditCatBroadcast, "Рассылки"},
	{AuditCatPanel, "Панель"},
}

// AdminAuditEntry is one action: its stable key, how it reads, and the area it
// belongs to.
type AdminAuditEntry struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Category string `json:"category"`
}

// AdminAuditCatalog is the stable action list the journal UI iterates over (to render
// an action name, and to expand a category filter). Adding an event appends here.
var AdminAuditCatalog = []AdminAuditEntry{
	{AuditLogin, "Вход в панель", AuditCatSession},
	{AuditLoginFailed, "Неудачный вход", AuditCatSession},
	{AuditLogout, "Выход", AuditCatSession},

	{AuditAdminCreated, "Администратор создан", AuditCatAdmins},
	{AuditAdminDeleted, "Администратор удалён", AuditCatAdmins},
	{AuditAdminRoleChanged, "Роль изменена", AuditCatAdmins},
	{AuditAdminPasswordReset, "Пароль сброшен", AuditCatAdmins},
	{AuditPasswordChanged, "Смена своего пароля", AuditCatAdmins},
	{AuditCredentialsChanged, "Смена своих учётных данных", AuditCatAdmins},

	{AuditSettings, "Изменение настроек", AuditCatSettings},

	{AuditPlanSaved, "Тариф сохранён", AuditCatPlans},
	{AuditPlanDeleted, "Тариф удалён", AuditCatPlans},
	{AuditPlanMigrated, "Перенос пользователей тарифа", AuditCatPlans},

	{AuditAPIKeyCreated, "Ключ API создан", AuditCatAPI},
	{AuditAPIKeyRevoked, "Ключ API отозван", AuditCatAPI},
	{AuditWebhookCreated, "Вебхук создан", AuditCatAPI},
	{AuditWebhookUpdated, "Вебхук изменён", AuditCatAPI},
	{AuditWebhookDeleted, "Вебхук удалён", AuditCatAPI},

	{AuditBroadcastStarted, "Рассылка запущена", AuditCatBroadcast},
	{AuditBroadcastChanged, "Рассылка: пауза, отмена или повтор", AuditCatBroadcast},
	{AuditBroadcastTest, "Тестовая отправка рассылки", AuditCatBroadcast},

	{AuditXrayRestarted, "Перезапуск Xray", AuditCatPanel},
	{AuditStatsReset, "Сброс статистики", AuditCatPanel},
	{AuditBackupTaken, "Бэкап скачан", AuditCatPanel},
	{AuditRestored, "Восстановление из бэкапа", AuditCatPanel},
	{AuditFactoryReset, "Сброс к заводским", AuditCatPanel},
	{AuditUpdated, "Обновление панели", AuditCatPanel},
}

// AdminAuditLabel returns the human label for an action key, falling back to the key
// itself so a row written by a newer build still renders as something.
func AdminAuditLabel(action string) string {
	for _, e := range AdminAuditCatalog {
		if e.Key == action {
			return e.Label
		}
	}
	return action
}

// AdminAuditActionsIn returns every action key in a category — what a category filter
// expands to. An unknown category yields nothing, which the store reads as "match
// nothing" rather than "match everything": a filter that silently ignores itself and
// dumps the whole trail is worse than an empty page.
func AdminAuditActionsIn(category string) []string {
	var out []string
	for _, e := range AdminAuditCatalog {
		if e.Category == category {
			out = append(out, e.Key)
		}
	}
	return out
}
