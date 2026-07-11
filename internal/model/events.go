package model

// The audit log: what happened to a user, who did it, and when. Every mutation the
// panel, the external API, the Telegram bots, the payment providers or the
// background poller perform on a user lands here as one UserEvent.

// UserEvent is one audit-log row.
type UserEvent struct {
	ID        int64  `json:"id"`
	UserID    int64  `json:"user_id"`
	UserName  string `json:"user_name"` // denormalized: the event outlives the user row
	Action    string `json:"action"`    // one of the Event* keys below
	ActorKind string `json:"actor_kind"`
	ActorName string `json:"actor_name"` // admin login, API key name, Telegram @username; "" for system
	Details   any    `json:"details"`    // decoded JSON object, nil when the row carried none
	CreatedAt int64  `json:"created_at"`
}

// Actor kinds — who performed the action.
const (
	ActorAdmin    = "admin"    // a panel session
	ActorAPIKey   = "apikey"   // the external REST API
	ActorTelegram = "telegram" // the admin Telegram bot
	ActorUser     = "user"     // the VPN user themself (user bot / subscription page)
	ActorSystem   = "system"   // the panel itself (cron, provider webhook)
)

// Audit event keys. Stable strings persisted in user_events.action — never renamed
// once shipped, or old rows lose their label.
const (
	EventUserCreated    = "user.created"
	EventUserRegistered = "user.registered" // self-registered via the user bot
	EventUserDeleted    = "user.deleted"
	EventUserRenamed    = "user.renamed"
	EventUserEnabled    = "user.enabled"
	EventUserDisabled   = "user.disabled"
	EventUserLimits     = "user.limits_changed"
	EventTrafficReset   = "user.traffic_reset"  // admin zeroed the counters
	EventQuotaReset     = "user.quota_reset"    // the automatic period rollover did
	EventResetPeriod    = "user.reset_period"   // autoreset period changed
	EventSubRotated     = "user.sub_rotated"    // subscription link reissued
	EventUserExpired    = "user.expired"        // system: subscription lapsed
	EventUserLimited    = "user.limited"        // system: quota exhausted
	EventDeviceLimited  = "user.device_limited" // system: too many devices
	EventTelegramLinked = "user.telegram_linked"
	EventTelegramUnlink = "user.telegram_unlinked"

	EventPlanChanged    = "plan.changed"
	EventPlanDowngraded = "plan.downgraded" // system: paid period ended → free plan
	EventPlanCancelled  = "plan.cancelled"

	EventPaymentCreated   = "payment.created"
	EventPaymentPaid      = "payment.paid"
	EventPaymentCancelled = "payment.cancelled"
)

// UserEventCatalog is the stable key→label list the journal UI iterates over (for
// the filter dropdown and for rendering an action name). Adding an event appends here.
var UserEventCatalog = []struct{ Key, Label string }{
	{EventUserCreated, "Пользователь создан"},
	{EventUserRegistered, "Саморегистрация"},
	{EventUserDeleted, "Пользователь удалён"},
	{EventUserRenamed, "Переименован"},
	{EventUserEnabled, "Включён"},
	{EventUserDisabled, "Отключён"},
	{EventUserLimits, "Изменены лимиты"},
	{EventTrafficReset, "Сброшен трафик"},
	{EventQuotaReset, "Автосброс квоты"},
	{EventResetPeriod, "Изменён период автосброса"},
	{EventSubRotated, "Обновлена ссылка подписки"},
	{EventUserExpired, "Подписка истекла"},
	{EventUserLimited, "Исчерпан трафик"},
	{EventDeviceLimited, "Превышен лимит устройств"},
	{EventTelegramLinked, "Telegram привязан"},
	{EventTelegramUnlink, "Telegram отвязан"},
	{EventPlanChanged, "Изменён тариф"},
	{EventPlanDowngraded, "Переведён на бесплатный тариф"},
	{EventPlanCancelled, "Подписка отменена"},
	{EventPaymentCreated, "Заказ создан"},
	{EventPaymentPaid, "Оплачено"},
	{EventPaymentCancelled, "Заказ отменён"},
}

// ValidUserEvent reports whether k is a known audit action key.
func ValidUserEvent(k string) bool {
	for _, e := range UserEventCatalog {
		if e.Key == k {
			return true
		}
	}
	return false
}

// UserEventRetentionDays is how long audit rows are kept before the retention sweep
// drops them.
const UserEventRetentionDays = 90
