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
	// An admin ended one of their own sessions from the account screen (or all but
	// the current one). Its own action: "someone signed me out" and "I signed out"
	// are different stories when a cookie has been misused.
	AuditSessionRevoked = "admin.session_revoked"

	// The roster.
	AuditAdminCreated       = "admin.created"
	AuditAdminDeleted       = "admin.deleted"
	AuditAdminRoleChanged   = "admin.role_changed"
	AuditAdminPasswordReset = "admin.password_reset"

	// Your own account.
	AuditPasswordChanged    = "admin.password_changed"
	AuditCredentialsChanged = "admin.credentials_changed"
	// The second factor. Its own actions rather than a generic settings row: "who
	// turned 2FA off on this account, and when" is the first question asked after an
	// admin account is misused.
	AuditTOTPEnabled  = "admin.totp_enabled"
	AuditTOTPDisabled = "admin.totp_disabled"

	// Settings — one action for all of them. Which section was touched goes in the
	// row's Target ("Routing", "DNS", …), not into a key of its own: a filter
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
	AuditUserMessaged     = "broadcast.user_messaged"

	// The panel itself.
	AuditXrayRestarted   = "panel.xray_restarted"
	AuditPanelRestarted  = "panel.restarted"
	AuditStatsReset      = "panel.stats_reset"
	AuditBackupTaken     = "panel.backup_downloaded"
	AuditRestored        = "panel.restored"
	AuditFactoryReset    = "panel.factory_reset"
	AuditWatchdogRestart = "panel.watchdog_restart"
	AuditWatchdogWedged  = "panel.watchdog_wedged" // wedge detected but auto-recovery off — alert only
	AuditUpdated         = "panel.updated"
	// Every user, with their credentials, downloaded as one file (users.exported).
	AuditUsersExported = "users.exported"
)

// Audit categories. What the journal is FILTERED by — a handful of areas instead of
// two dozen near-identical actions.
//
// The actions themselves stay precise: "administrator deleted" and "administrator
// created" are not the same event, and folding them into one key to shorten a
// dropdown would throw away the only thing the row is for. So the filter is unified,
// not the events: pick an area, read the exact action on each row.
const (
	AuditCatSession   = "session"
	AuditCatAdmins    = "admins"
	AuditCatSettings  = "settings"
	AuditCatPlans     = "plans"
	AuditCatAPI       = "api"
	AuditCatBroadcast = "broadcast"
	AuditCatPanel     = "panel"
)

// AdminAuditCategories is the filter's list, in the order it renders. Keys only —
// the panel labels them from its own dictionaries (audit.cat.*), so the journal
// follows the admin's chosen language rather than the server's.
var AdminAuditCategories = []string{
	AuditCatSession,
	AuditCatAdmins,
	AuditCatSettings,
	AuditCatPlans,
	AuditCatAPI,
	AuditCatBroadcast,
	AuditCatPanel,
}

// AdminAuditEntry is one action: its stable key and the area it belongs to. There
// is no label — see AdminAuditCategories.
type AdminAuditEntry struct {
	Key      string `json:"key"`
	Category string `json:"category"`
}

// AdminAuditCatalog is the stable action list the journal UI iterates over (to render
// an action name, and to expand a category filter). Adding an event appends here.
var AdminAuditCatalog = []AdminAuditEntry{
	{AuditLogin, AuditCatSession},
	{AuditLoginFailed, AuditCatSession},
	{AuditLogout, AuditCatSession},
	{AuditSessionRevoked, AuditCatSession},

	{AuditAdminCreated, AuditCatAdmins},
	{AuditAdminDeleted, AuditCatAdmins},
	{AuditAdminRoleChanged, AuditCatAdmins},
	{AuditAdminPasswordReset, AuditCatAdmins},
	{AuditPasswordChanged, AuditCatAdmins},
	{AuditCredentialsChanged, AuditCatAdmins},
	{AuditTOTPEnabled, AuditCatAdmins},
	{AuditTOTPDisabled, AuditCatAdmins},

	{AuditSettings, AuditCatSettings},

	{AuditPlanSaved, AuditCatPlans},
	{AuditPlanDeleted, AuditCatPlans},
	{AuditPlanMigrated, AuditCatPlans},

	{AuditAPIKeyCreated, AuditCatAPI},
	{AuditAPIKeyRevoked, AuditCatAPI},
	{AuditWebhookCreated, AuditCatAPI},
	{AuditWebhookUpdated, AuditCatAPI},
	{AuditWebhookDeleted, AuditCatAPI},

	{AuditBroadcastStarted, AuditCatBroadcast},
	{AuditBroadcastChanged, AuditCatBroadcast},
	{AuditBroadcastTest, AuditCatBroadcast},
	{AuditUserMessaged, AuditCatBroadcast},

	{AuditXrayRestarted, AuditCatPanel},
	{AuditPanelRestarted, AuditCatPanel},
	{AuditUsersExported, AuditCatPanel},
	{AuditStatsReset, AuditCatPanel},
	{AuditBackupTaken, AuditCatPanel},
	{AuditRestored, AuditCatPanel},
	{AuditFactoryReset, AuditCatPanel},
	{AuditUpdated, AuditCatPanel},
	{AuditWatchdogRestart, AuditCatPanel},
	{AuditWatchdogWedged, AuditCatPanel},
}

// AdminAuditKnown reports whether an action is in the catalog. The panel renders
// its label from the frontend dictionaries, so an action missing here shows up in
// the journal as a bare key like "settings.changed" and cannot be filtered for.
func AdminAuditKnown(action string) bool {
	for _, e := range AdminAuditCatalog {
		if e.Key == action {
			return true
		}
	}
	return false
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

// AuditSectionPrefix marks an audit target that is a settings section, so the panel
// knows to translate it rather than print it. Everything else a target can hold —
// an admin login, an API key name, a webhook URL — is free-form and shown as-is.
const AuditSectionPrefix = "audit.sec."
