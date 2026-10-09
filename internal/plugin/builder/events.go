package builder

import (
	"maps"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
)

// What each event carries, for the builder to offer: the fields a condition can test
// and a placeholder can fill, each with an example, and a sample of the whole event.
// It describes the payloads internal/core builds (manager_webhooks.go and the emit
// sites it lists); docs/api.md has the same catalog in prose.

// Field is one value a rule can read.
type Field struct {
	// Path is how a rule names it: user.name, data.days_left, event.
	Path string `json:"path"`
	// Type: text, number, bool, time (unix seconds), bytes, kop (kopecks), rub, list
	// or object.
	Type string `json:"type"`
	// Hint names its description in the panel's dictionaries: evField.<Hint>.
	Hint    string `json:"hint"`
	Example any    `json:"example"`
	// Maybe: not on every such event — only in some cases (see the description).
	Maybe bool `json:"maybe,omitempty"`
}

// EventInfo is one event of the panel as a rule sees it.
type EventInfo struct {
	Event string `json:"event"`
	// User says where the user it is about comes from: "data" (the user's own fields
	// are the event's), "nested" (data.user), "id" (only data.user_id, and only
	// sometimes) or "" (no user: a server, a broadcast, a request to sign up).
	User string `json:"user"`
	// Acts: the actions on a user (extend, enable, tag…) can run on it — not on a
	// user.deleted, whose user is gone by the time it comes.
	Acts bool `json:"acts"`
	// Fields: the event's own values, besides the user's.
	Fields []Field `json:"fields"`
	// Sample is data as such an event brings it.
	Sample map[string]any `json:"sample"`
}

// Catalog is everything a rule can read.
type Catalog struct {
	Context []Field     `json:"context"` // on every event
	User    []Field     `json:"user"`    // user.*, on an event about a user
	Events  []EventInfo `json:"events"`
}

var contextFields = []Field{
	{Path: "event", Type: "text", Hint: "event", Example: "user.created"},
	{Path: "event_id", Type: "text", Hint: "eventId", Example: "9f2c4e1a7b3d5f60"},
	{Path: "created_at", Type: "time", Hint: "createdAt", Example: 1767225600},
	{Path: "now", Type: "text", Hint: "now", Example: "2026-01-01T00:00:00.000Z"},
}

var userFields = []Field{
	{Path: "user.id", Type: "number", Hint: "userId", Example: 42},
	{Path: "user.name", Type: "text", Hint: "userName", Example: "ivan"},
	{Path: "user.status", Type: "text", Hint: "userStatus", Example: "active"},
	{Path: "user.enabled", Type: "bool", Hint: "userEnabled", Example: true},
	{Path: "user.expire_at", Type: "time", Hint: "userExpireAt", Example: 1769904000},
	{Path: "user.data_limit", Type: "bytes", Hint: "userDataLimit", Example: 107374182400},
	{Path: "user.plan_id", Type: "number", Hint: "userPlanId", Example: 2},
	{Path: "user.external_id", Type: "text", Hint: "userExternalId", Example: ""},
	{Path: "user.telegram_id", Type: "number", Hint: "userTelegramId", Example: 123456789},
	{Path: "user.mailing", Type: "bool", Hint: "userMailing", Example: true},
	{Path: "user.lang", Type: "text", Hint: "userLang", Example: "ru"},
}

func f(path, typ, hint string, example any) Field {
	return Field{Path: "data." + path, Type: typ, Hint: hint, Example: example}
}

func maybe(fl Field) Field { fl.Maybe = true; return fl }

var (
	deviceFields = []Field{
		f("device.hwid", "text", "deviceHwid", "a1b2c3d4"),
		f("device.os", "text", "deviceOs", "Android 14"),
		f("device.model", "text", "deviceModel", "Pixel 8"),
		f("devices", "number", "devices", 2),
		f("device_limit", "number", "deviceLimit", 3),
	}
	planFields = []Field{
		f("plan", "text", "plan", "Месяц"),
		f("prev_plan", "text", "prevPlan", "Пробный"),
	}
	messageFields = []Field{
		f("text", "text", "msgText", "Привет! Ваша подписка продлена."),
		f("buttons", "list", "buttons", []any{map[string]any{"text": "Открыть", "url": "https://example.com"}}),
		f("telegram_sent", "bool", "telegramSent", true),
	}
	mediaFields = []Field{
		maybe(f("media_kind", "text", "mediaKind", "photo")),
		maybe(f("media_name", "text", "mediaName", "banner.jpg")),
	}
	paymentFields = []Field{
		f("id", "number", "payId", 501),
		f("user_id", "number", "payUserId", 42),
		f("plan_id", "number", "payPlanId", 2),
		maybe(f("plan_name", "text", "payPlanName", "Месяц")),
		f("amount_rub", "rub", "amountRub", 199),
		f("status", "text", "payStatus", "paid"),
		f("provider", "text", "provider", "yookassa"),
		f("kind", "text", "payKind", "plan"),
		f("periods", "number", "periods", 1),
		f("discount_rub", "rub", "discountRub", 0),
		f("balance_kop", "kop", "payBalanceKop", 0),
		maybe(f("promo_code", "text", "promoCode", "SPRING")),
		f("created_at", "time", "payCreatedAt", 1767225000),
		f("paid_at", "time", "paidAt", 1767225600),
		maybe(f("user_balance_kop", "kop", "userBalanceKop", 5000)),
		maybe(f("user_name", "text", "payUserName", "ivan")),
		maybe(f("pay_url", "text", "payUrl", "https://pay.example.com/i/501")),
		maybe(f("promo_id", "number", "promoId", 3)),
		maybe(f("devices", "number", "payDevices", 1)),
		maybe(f("change_from", "number", "changeFrom", 1)),
		maybe(f("pack_bytes", "bytes", "packBytes", 53687091200)),
	}
	// An auto message's buttons are null when the rule has none, and it carries no
	// attachment.
	autoMessageFields = []Field{
		f("text", "text", "msgText", "Привет! Ваша подписка продлена."),
		maybe(f("buttons", "list", "buttons", []any{map[string]any{"text": "Открыть", "url": "https://example.com"}})),
		f("telegram_sent", "bool", "telegramSent", true),
	}
	nodeFields = []Field{
		f("node_id", "number", "nodeId", 3),
		f("name", "text", "nodeName", "Германия"),
		f("host", "text", "nodeHost", "de.example.com"),
	}
)

func with(base []Field, more ...Field) []Field {
	return append(append([]Field(nil), base...), more...)
}

// events: the catalog, in model.WebhookEventCatalog's order.
var events = []EventInfo{
	{Event: model.WebhookUserCreated, User: "data"},
	{Event: model.WebhookUserDeleted, User: "data"},
	{Event: model.WebhookUserRegistered, User: "data", Fields: []Field{
		maybe(f("moderated", "bool", "moderated", true)),
		maybe(f("request_id", "number", "requestId", 17)),
	}},
	{Event: model.WebhookRegistrationRequested, Fields: []Field{
		f("request_id", "number", "requestId", 17),
		f("name", "text", "regName", "ivan"),
		maybe(f("telegram_id", "number", "regTelegramId", 123456789)),
		maybe(f("external_id", "text", "regExternalId", "site-1001")),
	}},
	{Event: model.WebhookRegistrationRejected, User: "id", Fields: []Field{
		f("request_id", "number", "requestId", 17),
		f("name", "text", "regName", "ivan"),
		f("reason", "text", "rejectReason", "operator"),
		maybe(f("telegram_id", "number", "regTelegramId", 123456789)),
		maybe(f("external_id", "text", "regExternalId", "site-1001")),
		maybe(f("user_id", "number", "rejectUserId", 42)),
	}},
	{Event: model.WebhookUserExpired, User: "data"},
	{Event: model.WebhookUserLimited, User: "data"},
	{Event: model.WebhookUserDeviceLimit, User: "data", Fields: []Field{
		maybe(f("refused", "bool", "refused", true)),
		maybe(f("device.hwid", "text", "deviceHwid", "a1b2c3d4")),
		maybe(f("device.os", "text", "deviceOs", "Android 14")),
		maybe(f("device.model", "text", "deviceModel", "Pixel 8")),
		maybe(f("devices", "number", "devices", 3)),
		maybe(f("device_limit", "number", "deviceLimit", 3)),
	}},
	{Event: model.WebhookUserExpiring, User: "data", Fields: []Field{
		f("days_left", "number", "daysLeft", 3),
		f("stage", "number", "stage", 3),
		maybe(f("auto_renew", "bool", "autoRenew", true)),
		maybe(f("renew_price_kop", "kop", "renewPriceKop", 19900)),
		maybe(f("balance_kop", "kop", "balanceKop", 5000)),
	}},
	{Event: model.WebhookUserTrafficLow, User: "data", Fields: []Field{
		f("used", "bytes", "used", 85899345920),
		f("percent", "number", "trafficPercent", 80),
	}},
	{Event: model.WebhookUserSubRotated, User: "data", Fields: []Field{
		maybe(f("sub_url", "text", "subUrl", "https://example.com/sub/abc123")),
	}},
	{Event: model.WebhookUserTelegramLinked, User: "data"},
	{Event: model.WebhookUserTelegramUnlinked, User: "data"},
	{Event: model.WebhookUserEnabled, User: "data"},
	{Event: model.WebhookUserDisabled, User: "data"},
	{Event: model.WebhookUserAbuse, User: "data", Fields: []Field{
		f("measure", "text", "measure", "throttled"),
		maybe(f("until", "time", "until", 1767312000)),
		maybe(f("speed_limit", "number", "speedLimit", 512)),
		maybe(f("was", "text", "was", "throttle")),
	}},
	{Event: model.WebhookPlanChanged, User: "data", Fields: with(planFields,
		maybe(f("order_id", "number", "planOrderId", 501)))},
	{Event: model.WebhookPlanDowngraded, User: "data", Fields: with(planFields)},
	{Event: model.WebhookPlanCancelled, User: "data", Fields: with(planFields)},
	{Event: model.WebhookBalanceAdjusted, User: "data", Fields: []Field{
		f("amount_kop", "kop", "amountKop", 10000),
		f("balance_kop", "kop", "balanceKop", 15000),
	}},
	{Event: model.WebhookUserReferred, User: "data", Fields: []Field{
		f("referrer_id", "number", "referrerId", 7),
	}},
	{Event: model.WebhookReferralReward, User: "data", Fields: []Field{
		f("referred_user_id", "number", "referredUserId", 42),
		f("order_id", "number", "rewardOrderId", 501),
		f("reward_kop", "kop", "rewardKop", 5000),
		f("reward_days", "number", "rewardDays", 0),
		f("banked", "bool", "banked", true),
	}},
	{Event: model.WebhookPromoWinback, User: "data", Fields: []Field{
		f("code", "text", "code", "BACK-7KQ2"),
		f("percent", "number", "promoPercent", 20),
		f("expires_at", "time", "promoExpiresAt", 1767830400),
		f("attached", "bool", "attached", true),
	}},
	{Event: model.WebhookPromoRedeemed, User: "data", Fields: []Field{
		f("code", "text", "code", "SPRING"),
		f("kind", "text", "promoKind", "days"),
		f("value", "number", "promoValue", 7),
		maybe(f("plan", "text", "promoPlan", "Месяц")),
		maybe(f("balance_kop", "kop", "balanceKop", 5000)),
	}},
	{Event: model.WebhookUserLimitsChanged, User: "data", Fields: []Field{
		maybe(f("extended_days", "number", "extendedDays", 30)),
		maybe(f("refund_order_id", "number", "refundOrderId", 501)),
	}},
	{Event: model.WebhookUserTermStarted, User: "data"},
	{Event: model.WebhookUserTrafficReset, User: "data", Fields: []Field{
		f("auto", "bool", "resetAuto", false),
		maybe(f("used_before", "bytes", "usedBefore", 53687091200)),
	}},
	{Event: model.WebhookUserDeviceBound, User: "data", Fields: deviceFields},
	{Event: model.WebhookUserDeviceUnbound, User: "data", Fields: []Field{
		f("devices", "number", "devicesReleased", 1),
		maybe(f("hwid", "text", "hwid", "a1b2c3d4")),
		maybe(f("reason", "text", "unbindReason", "sub_rotated")),
	}},
	{Event: model.WebhookPaymentCreated, User: "nested", Fields: with(paymentFields)},
	{Event: model.WebhookPaymentPaid, User: "nested", Fields: with(paymentFields,
		maybe(f("renewal", "bool", "renewal", true)),
		maybe(f("undelivered", "bool", "undelivered", true)))},
	{Event: model.WebhookPaymentCancelled, User: "nested", Fields: with(paymentFields)},
	{Event: model.WebhookPaymentRefunded, User: "nested", Fields: with(paymentFields,
		f("plan_cancelled", "bool", "planCancelled", false),
		maybe(f("refunded_at", "time", "refundedAt", 1767312000)),
		maybe(f("refund_source", "text", "refundSource", "balance")),
		maybe(f("refund_kop", "kop", "refundKop", 19900)),
		maybe(f("taken_kop", "kop", "takenKop", 19900)),
		maybe(f("returned_kop", "kop", "returnedKop", 19900)))},
	{Event: model.WebhookUserMessage, User: "data", Fields: with(messageFields, mediaFields...)},
	{Event: model.WebhookUserAutoMessage, User: "data", Fields: with(autoMessageFields,
		f("rule_id", "number", "ruleId", 4),
		f("rule", "text", "ruleName", "Вернуть ушедших"),
		f("trigger", "text", "trigger", "expired"),
		maybe(f("code", "text", "code", "BACK-7KQ2")),
		maybe(f("percent", "number", "promoPercent", 20)),
		maybe(f("code_expires_at", "time", "codeExpiresAt", 1767830400)))},
	{Event: model.WebhookBroadcastSent, Fields: []Field{
		f("id", "number", "bcId", 12),
		f("text", "text", "msgText", "Новые серверы уже в подписке"),
		f("buttons", "list", "buttons", []any{}),
		f("audience", "text", "audience", "active"),
		f("telegram_recipients", "number", "telegramRecipients", 230),
		f("users", "list", "bcUsers", []any{map[string]any{"id": 42, "external_id": ""}}),
		maybe(f("media_kind", "text", "mediaKind", "photo")),
		maybe(f("media_name", "text", "mediaName", "banner.jpg")),
	}},
	{Event: model.WebhookUserMailing, User: "data"},
	{Event: model.WebhookNodeDown, Fields: with(nodeFields, f("last_seen", "time", "lastSeen", 1767225000))},
	{Event: model.WebhookNodeUp, Fields: with(nodeFields, f("down_seconds", "number", "downSeconds", 420))},
	{Event: model.WebhookXrayDown, Fields: []Field{
		f("node_id", "number", "nodeId", 0),
		f("name", "text", "nodeName", "Германия"),
		maybe(f("host", "text", "nodeHost", "de.example.com")),
		maybe(f("reason", "text", "xrayReason", "exit status 1")),
	}},
	{Event: model.WebhookXrayUp, Fields: []Field{
		f("node_id", "number", "nodeId", 0),
		f("name", "text", "nodeName", "Германия"),
		maybe(f("host", "text", "nodeHost", "de.example.com")),
		f("down_seconds", "number", "downSeconds", 35),
	}},
}

// EventCatalog is what the builder offers, the samples filled in.
func EventCatalog() Catalog {
	out := Catalog{Context: contextFields, User: userFields, Events: make([]EventInfo, len(events))}
	for i, e := range events {
		e.Sample = sampleData(e)
		e.Acts = actsOnUser(e.Event)
		if e.Fields == nil {
			e.Fields = []Field{}
		}
		out.Events[i] = e
	}
	return out
}

// eventInfo is the catalog's entry for an event, or nothing.
func eventInfo(event string) (EventInfo, bool) {
	for _, e := range events {
		if e.Event == event {
			return e, true
		}
	}
	return EventInfo{}, false
}

// userSample is a user as an event carries one.
func userSample() map[string]any {
	u := map[string]any{}
	for _, fl := range userFields {
		u[strings.TrimPrefix(fl.Path, "user.")] = fl.Example
	}
	return u
}

// sampleData is data as the event brings it: the user's fields where they are the
// event's, under "user" where they are nested, and the event's own on top.
func sampleData(e EventInfo) map[string]any {
	data := map[string]any{}
	switch e.User {
	case "data":
		maps.Copy(data, userSample())
	case "nested":
		data["user"] = userSample()
	}
	for _, fl := range e.Fields {
		put(data, strings.Split(strings.TrimPrefix(fl.Path, "data."), "."), fl.Example)
	}
	return data
}

func put(m map[string]any, path []string, v any) {
	if len(path) == 1 {
		m[path[0]] = v
		return
	}
	next, ok := m[path[0]].(map[string]any)
	if !ok {
		next = map[string]any{}
		m[path[0]] = next
	}
	put(next, path[1:], v)
}

// actsOnUser: whether a rule on the event can act on its user. A user.deleted
// carries the user's fields, but the user is gone: tag, enable and the rest fail.
func actsOnUser(event string) bool {
	e, ok := eventInfo(event)
	return ok && e.User != "" && event != model.WebhookUserDeleted
}
