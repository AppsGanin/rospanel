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
	EventUserNote       = "user.note_changed" // the operator's note was edited
	EventUserTags       = "user.tags_changed" // the tag list was edited
	EventUserEnabled    = "user.enabled"
	EventUserDisabled   = "user.disabled"
	EventUserLimits     = "user.limits_changed"
	EventSpeedLimit     = "user.speed_limit"    // per-user bandwidth cap changed
	EventTrafficReset   = "user.traffic_reset"  // admin zeroed the counters
	EventQuotaReset     = "user.quota_reset"    // the automatic period rollover did
	EventResetPeriod    = "user.reset_period"   // autoreset period changed
	EventSubRotated     = "user.sub_rotated"    // subscription link reissued
	EventUserExpired    = "user.expired"        // system: subscription lapsed
	EventUserLimited    = "user.limited"        // system: quota exhausted
	EventDeviceLimited  = "user.device_limited" // system: too many devices
	EventDeviceBound    = "user.device_bound"   // a client install claimed a device slot
	EventDeviceRefused  = "user.device_refused" // system: a new device hit the device cap
	EventPolicyRefused  = "user.policy_refused" // system: connected from a source the policy refuses
	EventDeviceUnbound  = "user.device_unbound" // a device was released (admin, or token rotation)
	EventTelegramLinked = "user.telegram_linked"
	EventTelegramUnlink = "user.telegram_unlinked"
	// System: the automatic blocklist measures (model.AbuseMeasures).
	EventAbuseWarned    = "user.abuse_warned"    // the user's bot was told to stop
	EventAbuseThrottled = "user.abuse_throttled" // speed capped for a while
	EventAbuseDisabled  = "user.abuse_disabled"  // switched off for a while
	EventAbuseLifted    = "user.abuse_lifted"    // the measure ran out or was overruled

	EventPlanChanged    = "plan.changed"
	EventPlanDowngraded = "plan.downgraded" // system: paid period ended → free plan
	EventPlanCancelled  = "plan.cancelled"

	EventPaymentCreated   = "payment.created"
	EventPaymentPaid      = "payment.paid"
	EventPaymentCancelled = "payment.cancelled"
)

// UserEventCatalog is the stable key list the journal UI iterates over to build its
// filter dropdown. Keys only: the panel renders every action name from its own
// dictionaries, so the label is decided by the language the ADMIN chose in the
// browser rather than by whatever the server happens to speak. Adding an event
// appends here — and to events.action.* in the frontend dictionaries.
var UserEventCatalog = []string{
	EventUserCreated,
	EventUserRegistered,
	EventUserDeleted,
	EventUserRenamed,
	EventUserNote,
	EventUserTags,
	EventUserEnabled,
	EventUserDisabled,
	EventUserLimits,
	EventSpeedLimit,
	EventTrafficReset,
	EventQuotaReset,
	EventResetPeriod,
	EventSubRotated,
	EventUserExpired,
	EventUserLimited,
	EventDeviceLimited,
	EventDeviceBound,
	EventDeviceRefused,
	EventPolicyRefused,
	EventDeviceUnbound,
	EventTelegramLinked,
	EventTelegramUnlink,
	EventAbuseWarned,
	EventAbuseThrottled,
	EventAbuseDisabled,
	EventAbuseLifted,
	EventPlanChanged,
	EventPlanDowngraded,
	EventPlanCancelled,
	EventPaymentCreated,
	EventPaymentPaid,
	EventPaymentCancelled,
}

// ValidUserEvent reports whether k is a known audit action key.
func ValidUserEvent(k string) bool {
	for _, key := range UserEventCatalog {
		if key == k {
			return true
		}
	}
	return false
}

// UserEventRetentionDays is how long audit rows are kept before the retention sweep
// drops them.
const UserEventRetentionDays = 90
