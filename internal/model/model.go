// Package model holds the core domain types shared across the panel.
package model

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// TLSModeACME is the only TLS mode: ACME (Let's Encrypt or ZeroSSL) for a domain or IP.
const TLSModeACME = "acme"

// ACME CA providers.
const (
	ACMEProviderLE      = "letsencrypt" // default; no EAB required
	ACMEProviderZeroSSL = "zerossl"     // requires EAB credentials from zerossl.com
)

// Protocol display names. These appear in lockstep across the share-link "#label",
// the sing-box/Clash node tag/name, the subscription page, and the Connections UI;
// keeping them here as the single source stops those copies from drifting apart.
const (
	ProtoVLESS    = "VLESS-TCP-TLS"
	ProtoReality  = "VLESS-GRPC-REALITY"
	ProtoTrojan   = "TROJAN-WS"
	ProtoHysteria = "HYSTERIA-UDP"
)

// DeviceOnlineWindow is how long (seconds) a source IP counts as an active device.
// Matches the panel's online indicator (stats poll ~60s + access-log writes).
const DeviceOnlineWindow int64 = 120

// ConnectionRetentionDays is how long a connections row outlives its last sighting.
// Only DeviceOnlineWindow matters for the device limit; the rest of the history
// exists purely for the per-user IP list in the UI, and a roaming mobile client
// accrues a row per IP indefinitely without a sweep.
const ConnectionRetentionDays = 30

// AbuseRetentionDays is how long a blocklist match is kept.
//
// Short on purpose. This is the most sensitive data the panel holds — it names what
// a person reached, not merely that they connected — and its job is to answer "is
// this account a problem right now", which two weeks covers. An abuse complaint
// that arrives later is answered from the complaint's own timestamp, not from here.
const AbuseRetentionDays = 14

// TrafficDailyRetentionDays is how long per-day traffic history is kept. It sits
// well above the journals' 30/90 days because this is reporting data rather than a
// log — the stats page offers ranges up to a year — but it is bounded all the same:
// traffic_daily grows at users × nodes × days, so without a sweep it is the one
// table that never stops.
const TrafficDailyRetentionDays = 365

// User status values derived on read (not stored).
const (
	StatusActive        = "active"
	StatusDisabled      = "disabled"
	StatusExpired       = "expired"
	StatusLimited       = "limited"        // traffic quota exhausted
	StatusDeviceLimited = "device_limited" // too many concurrent devices
)

// Self-registration modes (Settings.TGUserRegMode).
const (
	RegOff        = "off"        // registration closed
	RegOpen       = "open"       // account created and active immediately
	RegModeration = "moderation" // account created disabled; an admin approves it
	RegInvite     = "invite"     // an invite code is required, then active
)

// User is a VPN user. In v1 one user = one credential set applied across all
// enabled protocols (M1 only wires VLESS).
type User struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	UUID      string    `json:"uuid"`       // VLESS
	Password  string    `json:"-"`          // Trojan + Hysteria2 (shared); embedded in links only
	SubToken  string    `json:"-"`          // subscription capability token
	Status    string    `json:"status"`     // active | disabled | expired | limited | device_limited
	Enabled   bool      `json:"enabled"`    // manual on/off toggle (independent of Status)
	DataLimit int64     `json:"data_limit"` // bytes, 0 = unlimited
	ExpireAt  int64     `json:"expire_at"`  // unix seconds, 0 = never
	UsedUp    int64     `json:"used_up"`
	UsedDown  int64     `json:"used_down"`
	LastUp    int64     `json:"-"` // last raw Xray uplink counter
	LastDown  int64     `json:"-"` // last raw Xray downlink counter
	CreatedAt time.Time `json:"created_at"`

	ResetPeriod string `json:"reset_period"` // none | daily | weekly | monthly | yearly
	LastResetAt int64  `json:"-"`            // unix of the last automatic quota reset
	LastSeen    int64  `json:"last_seen"`    // unix of last activity (0 = never); 0 ⇒ offline

	DeviceLimit   int `json:"device_limit"`   // max concurrent devices (unique IPs), 0 = unlimited
	ActiveDevices int `json:"active_devices"` // computed: distinct IPs seen within DeviceOnlineWindow

	TgChatID int64 `json:"tg_chat_id"` // linked Telegram chat for the user bot (0 = not linked)

	TgLinkCode   string `json:"-"` // pending one-time Telegram bind code (replaces sub-token deep links)
	TgLinkCodeAt int64  `json:"-"` // unix when TgLinkCode was issued (0 = none)

	PlanID    int64  `json:"plan_id"`             // active tariff (0 = manual limits)
	TrialUsed bool   `json:"trial_used"`          // trial period already consumed
	PlanName  string `json:"plan_name,omitempty"` // computed for API views (not stored)

	// NotifiedStatus is the last Status the operator/user was ALERTED about (admin
	// push, webhook, audit row). Persisted so a panel restart cannot lose a transition
	// that happened while it was down. "" = never alerted about.
	NotifiedStatus string `json:"-"`

	// NotifiedExpireAt is the ExpireAt a "runs out soon" warning was already sent for.
	// Holding the expiry rather than a timestamp re-arms the warning for free: a
	// renewal moves ExpireAt, the stored value stops matching, and the next cycle is
	// eligible again.
	NotifiedExpireAt int64 `json:"-"`

	// NotifiedQuotaAt marks that a "running low on traffic" notice was already sent
	// (0 = not yet). Cleared once usage falls back under the threshold, which is what
	// a quota reset or a plan change does — so the warning re-arms on its own.
	NotifiedQuotaAt int64 `json:"-"`
}

// TelegramLinkCodeTTL is how long a one-time Telegram bind code stays valid.
const TelegramLinkCodeTTL = 15 * time.Minute

// UserTgLinkCodeValid reports whether the user's pending Telegram bind code
// exists and has not expired.
func (u User) UserTgLinkCodeValid() bool {
	if strings.TrimSpace(u.TgLinkCode) == "" || u.TgLinkCodeAt == 0 {
		return false
	}
	return time.Now().Unix()-u.TgLinkCodeAt <= int64(TelegramLinkCodeTTL.Seconds())
}

// TariffPlan is a billing tier (free, trial template, or paid).
type TariffPlan struct {
	ID          int64  `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	PriceRub    int    `json:"price_rub"`
	PeriodDays  int    `json:"period_days"`
	DataLimit   int64  `json:"data_limit"`
	DeviceLimit int    `json:"device_limit"`
	SortOrder   int    `json:"sort_order"`
	Enabled     bool   `json:"enabled"`
}

// IsFree reports whether this is a free plan. A plan is free iff it has no price:
// free plans never expire and refill their quota every срок действия, while paid
// plans (price > 0) expire after their period and must be renewed.
func (p TariffPlan) IsFree() bool { return p.PriceRub <= 0 }

// PaymentOrder is a user payment request (manual or via a payment provider).
type PaymentOrder struct {
	ID         int64  `json:"id"`
	UserID     int64  `json:"user_id"`
	UserName   string `json:"user_name,omitempty"`
	PlanID     int64  `json:"plan_id"`
	PlanName   string `json:"plan_name,omitempty"`
	AmountRub  int    `json:"amount_rub"`
	Status     string `json:"status"`                // pending | paid | cancelled
	Provider   string `json:"provider"`              // "" (manual) or a payments registry key
	ProviderID string `json:"provider_id,omitempty"` // external payment/invoice id (admin-only view)
	PayURL     string `json:"pay_url,omitempty"`     // hosted payment URL for the user
	CreatedAt  int64  `json:"created_at"`
	PaidAt     int64  `json:"paid_at"`
}

// RegistrationRequest is a moderated self-registration awaiting an admin decision.
// No user exists yet — approval creates one and links ChatID; rejection just drops
// the request.
type RegistrationRequest struct {
	ID        int64  `json:"id"`
	ChatID    int64  `json:"chat_id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
}

// SupportGroup is a group the support bot has been added to — an option in the
// settings picker, never applied on its own. IsForum and IsAdmin are shown so the
// operator can see at a glance which candidate is actually usable.
type SupportGroup struct {
	ChatID  int64  `json:"chat_id"`
	Title   string `json:"title"`
	IsForum bool   `json:"is_forum"`
	IsAdmin bool   `json:"is_admin"`
}

// Subscriber is a chat that has opened the user bot — the audience a broadcast is
// addressed to. Wider than the user roster on purpose: it also holds people who
// never completed registration and people whose account was deleted, both of whom a
// broadcast may still need to reach. UserID is nil-able in SQL and 0 here when the
// chat isn't tied to an account.
//
// Active and OptOut mean different things and must not be conflated: Active=false is
// Telegram refusing delivery (blocked or deactivated), OptOut=true is the person
// choosing not to receive broadcasts while still getting service messages.
type Subscriber struct {
	ChatID    int64  `json:"chat_id"`
	UserID    int64  `json:"user_id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	Lang      string `json:"lang"`
	Active    bool   `json:"active"`
	OptOut    bool   `json:"opt_out"`
	BlockedAt int64  `json:"blocked_at"`
	StartedAt int64  `json:"started_at"`
}

// Broadcast statuses. There is no "draft": a broadcast row exists only once it has
// been launched, so nothing half-composed can be mistaken for something queued.
const (
	BroadcastRunning   = "running"
	BroadcastPaused    = "paused"
	BroadcastDone      = "done"
	BroadcastCancelled = "cancelled"
)

// Per-recipient delivery states. "blocked" is kept apart from "failed" because it is
// permanent and not the operator's problem to retry — it means Telegram will never
// accept a message for that chat again.
const (
	TargetPending = "pending"
	TargetSent    = "sent"
	TargetFailed  = "failed"
	TargetBlocked = "blocked"
	// TargetSkipped is someone who unsubscribed after the audience was frozen. The
	// snapshot is what keeps the total steady, but honouring an opt-out only until
	// the run starts would deliver marketing to a person the bot has just told they
	// are unsubscribed — which is what makes people block it outright.
	TargetSkipped = "skipped"
)

// Broadcast audiences, resolved to a chat list once at launch.
//
// The parameterised ones carry their argument in the value itself ("seen:7") rather
// than in another column: an audience is written once at launch and read back only to
// be shown, so a second field would exist purely to be kept in sync with this one.
const (
	AudienceAll      = "all"
	AudienceLinked   = "linked"   // has a panel account
	AudienceUnlinked = "unlinked" // never registered, or the account was deleted
	AudienceActive   = "active"   // account in the active state
	AudienceExpired  = "expired"  // subscription ran out
	AudienceNever    = "never"    // linked account that has never connected

	AudienceSeenPrefix     = "seen:"     // connected within the last N days
	AudienceUnseenPrefix   = "unseen:"   // not seen for N days (never counts)
	AudienceExpiringPrefix = "expiring:" // subscription runs out within N days
)

// AudienceDays extracts the N from a parameterised audience, and reports whether the
// value is one. Bounded so a typo can't turn "not seen for 90 days" into a filter
// that quietly matches nobody or everybody.
func AudienceDays(audience, prefix string) (int, bool) {
	rest, ok := strings.CutPrefix(audience, prefix)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 1 || n > 365 {
		return 0, false
	}
	return n, true
}

// ValidAudience reports whether an audience string is one the panel knows how to
// resolve.
func ValidAudience(a string) bool {
	switch a {
	case AudienceAll, AudienceLinked, AudienceUnlinked, AudienceActive,
		AudienceExpired, AudienceNever:
		return true
	}
	for _, p := range []string{AudienceSeenPrefix, AudienceUnseenPrefix, AudienceExpiringPrefix} {
		if _, ok := AudienceDays(a, p); ok {
			return true
		}
	}
	return false
}

// BroadcastButton is one URL button under a broadcast. Only URL buttons are offered:
// a callback button would need a handler in the bot, and one attached to a message
// sent months ago outlives whatever it was meant to do.
type BroadcastButton struct {
	Text string `json:"text"`
	URL  string `json:"url"`
}

// Broadcast is one mass message and its progress. The counters are derived from the
// per-recipient rows on read rather than stored, so they cannot drift from reality.
type Broadcast struct {
	ID          int64             `json:"id"`
	CreatedBy   string            `json:"created_by"`
	Text        string            `json:"text"`
	MediaKind   string            `json:"media_kind"` // "" | "photo" | "document"
	MediaFileID string            `json:"-"`
	MediaName   string            `json:"media_name"`
	Buttons     []BroadcastButton `json:"buttons"`
	Audience    string            `json:"audience"`
	Status      string            `json:"status"`
	CreatedAt   int64             `json:"created_at"`
	StartedAt   int64             `json:"started_at"`
	FinishedAt  int64             `json:"finished_at"`

	Total   int `json:"total"`
	Sent    int `json:"sent"`
	Failed  int `json:"failed"`
	Blocked int `json:"blocked"`
	Skipped int `json:"skipped"`
}

// Pending reports how many recipients are still waiting.
func (b *Broadcast) Pending() int {
	return b.Total - b.Sent - b.Failed - b.Blocked - b.Skipped
}

// PaymentProvider is one payment provider's saved setup: whether it's on, plus the
// credentials for the fields its registry entry declares (internal/payments). The
// config holds API keys, so it never leaves the server — the panel is told only
// which fields are set, never their values.
type PaymentProvider struct {
	Key     string
	Enabled bool
	Config  map[string]string
}

// APIKey is a named credential for the external REST API. The raw key is only
// ever returned once (at creation, in RawKey); the stored record keeps just its
// HMAC hash and the clear Prefix so the operator can identify it in the UI.
type APIKey struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Prefix     string `json:"prefix"`            // leading clear part, e.g. "rp_A1b2C3"
	CreatedAt  int64  `json:"created_at"`        // unix seconds
	LastUsedAt int64  `json:"last_used_at"`      // unix seconds, 0 = never used
	RevokedAt  int64  `json:"revoked_at"`        // unix seconds, 0 = active
	RawKey     string `json:"raw_key,omitempty"` // populated only on creation
}

// Active reports whether the key has not been revoked.
func (k APIKey) Active() bool { return k.RevokedAt == 0 }

// Webhook is an outbound HTTP endpoint the panel POSTs lifecycle events to. The
// Secret is a symmetric key both sides hold: the panel signs each delivery with
// HMAC-SHA256 and the receiver verifies it (so unlike an API key it stays
// readable in the UI). Events is the subscribed set; empty ⇒ every event.
type Webhook struct {
	ID            int64    `json:"id"`
	URL           string   `json:"url"`
	Secret        string   `json:"secret"`
	Events        []string `json:"events"`
	Enabled       bool     `json:"enabled"`
	CreatedAt     int64    `json:"created_at"`
	LastStatus    int      `json:"last_status"`     // last HTTP status (0 = never/connection error)
	LastAttemptAt int64    `json:"last_attempt_at"` // unix seconds, 0 = never delivered
	LastError     string   `json:"last_error"`      // last failure reason ("" = ok/never)
}

// Subscribed reports whether this webhook wants the given event. An empty Events
// set means "all events".
func (h Webhook) Subscribed(event string) bool {
	if len(h.Events) == 0 {
		return true
	}
	for _, e := range h.Events {
		if e == event {
			return true
		}
	}
	return false
}

// Webhook event keys. Stable strings sent in the payload's "event" field and the
// X-RosPanel-Event header; never renumbered/renamed once shipped.
const (
	WebhookUserCreated      = "user.created"        // created via panel or API
	WebhookUserDeleted      = "user.deleted"        //
	WebhookUserRegistered   = "user.registered"     // self-registered via the user bot
	WebhookUserExpired      = "user.expired"        // subscription lapsed
	WebhookUserLimited      = "user.limited"        // traffic quota exhausted
	WebhookUserDeviceLimit  = "user.device_limited" //
	WebhookPaymentCreated   = "payment.created"     // order opened
	WebhookPaymentPaid      = "payment.paid"        // order paid, plan applied
	WebhookPaymentCancelled = "payment.cancelled"   //
)

// WebhookEventCatalog is the stable key→label list the settings UI iterates over
// (display order). Adding an event appends here.
var WebhookEventCatalog = []struct{ Key, Label string }{
	{WebhookUserCreated, "Пользователь создан"},
	{WebhookUserDeleted, "Пользователь удалён"},
	{WebhookUserRegistered, "Саморегистрация"},
	{WebhookUserExpired, "Подписка истекла"},
	{WebhookUserLimited, "Исчерпан трафик"},
	{WebhookUserDeviceLimit, "Превышен лимит устройств"},
	{WebhookPaymentCreated, "Заказ создан"},
	{WebhookPaymentPaid, "Оплачено"},
	{WebhookPaymentCancelled, "Заказ отменён"},
}

// ValidWebhookEvent reports whether k is a known webhook event key.
func ValidWebhookEvent(k string) bool {
	for _, e := range WebhookEventCatalog {
		if e.Key == k {
			return true
		}
	}
	return false
}

// ValidWebhookURL checks a webhook target: an http or https URL with a host.
// Unlike the SSRF-guarded fetch surfaces (proxy lists, routing templates, whose
// URLs may come from less-trusted places), a webhook target is set by the
// authenticated admin and only ever receives a blind POST, so private/localhost
// hosts are allowed — the receiver is often the operator's own internal service.
func ValidWebhookURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("неверный URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL должен начинаться с http:// или https://")
	}
	if u.Host == "" {
		return fmt.Errorf("не указан хост")
	}
	return nil
}

// ProviderStat is paid-order revenue for one payment provider ("" = manual).
type ProviderStat struct {
	Provider string `json:"provider"`
	Count    int    `json:"count"`
	Sum      int    `json:"sum"` // rubles
}

// PaymentStats is the revenue dashboard shown on the Payments page.
type PaymentStats struct {
	TotalPaid    int            `json:"total_paid"`    // all-time paid revenue (₽)
	PaidCount    int            `json:"paid_count"`    // number of paid orders
	EarnedToday  int            `json:"earned_today"`  // paid revenue since local midnight
	EarnedMonth  int            `json:"earned_month"`  // paid revenue since the 1st (local)
	PendingCount int            `json:"pending_count"` // orders awaiting payment
	PendingSum   int            `json:"pending_sum"`   // their total (₽)
	ByProvider   []ProviderStat `json:"by_provider"`   // paid revenue split by provider
}

// UserEmail returns the identifier a user is keyed by inside Xray — "u<id>" —
// which appears in access logs, per-user stats, and every protocol's client
// entry. This is the single source of that format.
func UserEmail(id int64) string { return fmt.Sprintf("u%d", id) }

// Connection is a per-source-IP record of a user's connections.
type Connection struct {
	IP       string `json:"ip"`
	LastSeen int64  `json:"last_seen"`
	Count    int64  `json:"count"`
}

// DailyPoint is one day's traffic total (for charts).
type DailyPoint struct {
	Day  string `json:"day"` // YYYY-MM-DD (UTC)
	Up   int64  `json:"up"`
	Down int64  `json:"down"`
}

// UserTotal is a user's traffic total over a period (for the per-user table).
type UserTotal struct {
	UserID int64  `json:"user_id"`
	Name   string `json:"name"`
	Up     int64  `json:"up"`
	Down   int64  `json:"down"`
}

// Settings is the singleton (id=1) panel configuration. The DB is the source of
// truth; the Xray config.json is always derived from it.
type Settings struct {
	ID   int64  `json:"-"`
	Host string `json:"host"` // public domain or IP used in share links
	SNI  string `json:"sni"`  // TLS server name (link + cert SAN)
	// TLSMode is vestigial: self-signed and custom-upload were removed as operator
	// choices, so ACME is the only mode and this is always TLSModeACME. It survives
	// because the column is still written by every TLS path and read as a guard
	// before renewal — and because it's the seam where a second mode would go back
	// in. Don't branch new behaviour on it; treat ACME as given.
	TLSMode         string    `json:"tls_mode"`
	ACMEEmail       string    `json:"acme_email"`
	ACMEProvider    string    `json:"acme_provider"` // "letsencrypt" | "zerossl"
	ZeroSSLEABKID   string    `json:"-"`             // ZeroSSL External Account Binding KID
	ZeroSSLEABHMAC  string    `json:"-"`             // ZeroSSL EAB HMAC key (base64url)
	CertPath        string    `json:"cert_path"`
	KeyPath         string    `json:"key_path"`
	VLESSPort       int       `json:"vless_port"`
	ConfigRevision  int64     `json:"config_revision"`
	LastConfigError string    `json:"last_config_error"`
	UpdatedAt       time.Time `json:"updated_at"`
	PanelSecretPath string    `json:"-"` // never serialized to clients
	// Branding: custom panel display name + colour theme (empty ⇒ defaults). A
	// custom logo lives as a file under <dataDir>/branding/, not here.
	PanelName     string `json:"-"`
	PanelTheme    string `json:"-"` // JSON {accent,text,muted,bg,surface}, empty ⇒ defaults
	DecoyTemplate string `json:"decoy_template"`
	WSPath        string `json:"-"` // Trojan-WS path matched by VLESS fallbacks
	TrojanPort    int    `json:"-"` // loopback Trojan inbound port
	HysteriaPort  int    `json:"hysteria_port"`
	HopStart      int    `json:"hop_start"`
	HopEnd        int    `json:"hop_end"`
	// HopInterval is the port-hopping rotation interval in seconds ("min-max"),
	// embedded in the Hysteria2 share link's quicParams.
	HopInterval string `json:"-"`

	// Per-protocol toggles for the Connections panel. A disabled protocol drops
	// out of user subscriptions/share links and its clients are removed from the
	// Xray inbound (the listener stays up but rejects everyone).
	VLESSEnabled    bool `json:"-"`
	TrojanEnabled   bool `json:"-"`
	HysteriaEnabled bool `json:"-"`

	// VLESS + gRPC + REALITY inbound (separate port). REALITY borrows the TLS of a
	// real site (RealityDest) instead of our cert. Keys/shortId/serviceName are
	// generated by the panel; the public key + shortId go into share links.
	RealityEnabled     bool   `json:"-"`
	RealityPort        int    `json:"-"`
	RealityDest        string `json:"-"` // target site / SNI(s), e.g. "max.ru"
	RealityPrivateKey  string `json:"-"` // X25519 private (base64 raw-url)
	RealityPublicKey   string `json:"-"` // X25519 public (base64 raw-url), in links (pbk)
	RealityShortID     string `json:"-"` // hex shortId, in links (sid)
	RealityServiceName string `json:"-"` // gRPC service name

	// Proxy mode: a socks/http forward-proxy inbound so other RosPanel servers can
	// chain their egress through this one (point their proxy pool at host:port).
	ProxyModeEnabled bool   `json:"-"`
	ProxyModeType    string `json:"-"` // "socks" | "http"
	ProxyModePort    int    `json:"-"`
	ProxyModeUser    string `json:"-"`
	ProxyModePass    string `json:"-"`

	// First-run wizard state. SetupDone gates the wizard; Timezone is the IANA
	// zone anchoring the local-day boundary for stats (empty ⇒ server local).
	SetupDone bool   `json:"-"`
	Timezone  string `json:"-"`

	// Subscription delivery settings (Settings → Подписки).
	SubPath           string `json:"-"` // public subscription URL prefix /<sub_path>/<token>
	SubBase64         bool   `json:"-"` // base64-encode the universal link list
	SubNameInTitle    bool   `json:"-"` // append the user name to Profile-Title / group name
	SubTitle          string `json:"-"` // profile title base (empty ⇒ «РосПанель»)
	SubRouting        bool   `json:"-"` // attach auto-routing headers
	SubRoutingHapp    string `json:"-"` // Happ routing config URL
	SubRoutingIncy    string `json:"-"` // INCY routing config URL
	SubRoutingMihomo  string `json:"-"` // Mihomo (Clash Meta) routing config URL
	SubUpdateInterval int    `json:"-"` // subscription auto-update interval (hours)
	// SubAnnounce is a short broadcast shown inside the VPN client itself (Happ,
	// v2RayTun) via the subscription's Announce header. Empty ⇒ no announcement.
	// Clients only render the first 200 characters; the panel enforces that limit.
	SubAnnounce string `json:"-"`

	// UserAutoDeleteDays deletes an expired user this many days after their expiry
	// date. 0 ⇒ never (default): expired users pile up but nothing is ever destroyed
	// behind the operator's back.
	UserAutoDeleteDays int `json:"-"`

	XrayDNS string `json:"-"` // upstream DNS servers for Xray (newline/comma separated)

	// Per-connection uTLS fingerprint embedded in share links (fp=). Hysteria2
	// (QUIC) has none.
	VLESSFp   string `json:"-"`
	TrojanFp  string `json:"-"`
	RealityFp string `json:"-"`

	// Per-connection display names shown in VPN clients / on the subscription page
	// (the node label after '#' and the sing-box/Clash node tag). Empty ⇒ the
	// default protocol label (ProtoVLESS, ProtoReality, …). See Settings.ProtoLabel.
	VLESSName    string `json:"-"`
	RealityName  string `json:"-"`
	TrojanName   string `json:"-"`
	HysteriaName string `json:"-"`

	// Anti-DPI / anti-censorship transport hardening (Settings → Подключения).
	// TLSFragment / BlockQUIC shape the GENERATED client configs (sing-box only —
	// no server change); TLSMin13 / RealityMaxTimeDiff / RealityDestPort change the
	// SERVER inbound config.
	TLSFragment        bool `json:"-"` // sing-box ClientHello fragmentation (Vision + Trojan-WS)
	TLSMin13           bool `json:"-"` // require TLS 1.3 on the :443 inbound
	BlockQUIC          bool `json:"-"` // drop untunneled browser QUIC (UDP/443) in client configs
	RealityMaxTimeDiff int  `json:"-"` // REALITY anti-replay window in ms (0 = off)

	// Opera VPN egress: the opera-proxy helper binary exposes a local HTTP proxy
	// (127.0.0.1:OperaPort) we add as the "opera" routing lane. Country is the
	// Opera VPN region (EU|AS|AM).
	OperaEnabled bool   `json:"-"`
	OperaCountry string `json:"-"`
	OperaPort    int    `json:"-"`

	// Cloudflare WARP outbound (WireGuard). When enabled and registered, routing
	// rules with action "warp" egress through it.
	WarpEnabled    bool   `json:"-"`
	WarpPrivateKey string `json:"-"` // our WG secret key (base64)
	WarpPublicKey  string `json:"-"` // Cloudflare's WG public key (base64)
	WarpEndpoint   string `json:"-"` // host:port of the WARP peer
	WarpAddressV4  string `json:"-"` // assigned interface IPv4
	WarpAddressV6  string `json:"-"` // assigned interface IPv6
	WarpReserved   string `json:"-"` // client id as "a,b,c"

	// Telegram bot (Settings → Telegram). An authorized admin chat can view/add/
	// remove users via the bot, and scheduled backups are pushed to it. TGChatIDs
	// is the comma-separated set of authorized chats; TGLinkCode is the pending
	// one-time code an admin sends as "/start <code>" to link their chat.
	TGBotEnabled bool   `json:"-"`
	TGBotToken   string `json:"-"`
	TGChatIDs    string `json:"-"` // comma-separated authorized chat IDs
	TGLinkCode   string `json:"-"` // pending one-time linking code (cleared once used)
	TGBackupCron string `json:"-"` // 5-field cron (operator TZ) for scheduled backups; empty = off

	// Separate public user bot (Settings → Telegram): VPN clients self-register and
	// self-serve their subscription. Must use a different token than the admin bot.
	TGUserBotEnabled bool   `json:"-"`
	TGUserBotToken   string `json:"-"`
	TGUserRegEnabled bool   `json:"-"` // derived mirror of RegMode != off (kept for old readers)
	// TGUserRegMode is how self-registration behaves: RegOff | RegOpen | RegModeration
	// | RegInvite. TGUserRegCode is the invite code required in RegInvite mode.
	TGUserRegMode string `json:"-"`
	TGUserRegCode string `json:"-"`

	// TGAdminEvents is a bitmask of the AdminEvent* categories the admin bot pushes
	// to the authorized chats. Default -1 (all on); see AdminEventEnabled.
	TGAdminEvents int64 `json:"-"`

	// TGUserEvents is the same idea aimed the other way: which UserEvent* categories
	// the USER bot pushes to the person themselves. TGUserExpiringDays is how many
	// days ahead the expiry warning goes out.
	TGUserEvents       int64 `json:"-"`
	TGUserExpiringDays int   `json:"-"`

	// Abuse/blocklist config. AbuseEnabled is the master switch; AbuseCategories is a
	// bitmask of active feed categories (AbuseCat*); AbuseCustom is the operator's own
	// domains/IPs (one per line); AbuseAlertMin is matches-per-day before an alert.
	AbuseEnabled    bool   `json:"-"`
	AbuseCategories int64  `json:"-"`
	AbuseCustom     string `json:"-"`
	AbuseAlertMin   int    `json:"-"`

	// Support relay (Settings → Telegram → Поддержка): a third bot whose only job is
	// to carry messages between a user and a per-user topic in TGSupportGroupID, a
	// forum supergroup the operator's admins answer in. It is separate from the user
	// bot precisely so that everything sent to it is unambiguously a support request.
	// TGSupportBotUsername is the cached @username the user bot links to.
	TGSupportEnabled     bool   `json:"-"`
	TGSupportBotToken    string `json:"-"`
	TGSupportBotUsername string `json:"-"`
	TGSupportGroupID     int64  `json:"-"`
	TGSupportGreeting    string `json:"-"`

	// Local scheduled backups, independent of Telegram: archives are written to
	// <dataDir>/backups and the newest LocalBackupKeep are retained. Same 5-field
	// cron dialect and operator timezone as TGBackupCron; empty = off.
	LocalBackupCron string `json:"local_backup_cron"`
	LocalBackupKeep int    `json:"local_backup_keep"`

	// Billing (Settings → Оплата): plans, trial plan, free-tier fallback. The
	// trial's length is the trial plan's own period_days, not a separate setting.
	BillingEnabled     bool   `json:"-"`
	BillingFreePlanID  int64  `json:"-"`
	BillingTrialPlanID int64  `json:"-"`
	BillingPaymentNote string `json:"-"`

	// PaymentWebhookSecret is the random URL segment the provider webhooks are
	// mounted under (/<secret>/<provider>), so the callback path is fixed yet
	// unguessable and doesn't reveal the hidden panel. Provider credentials
	// themselves live in the payment_providers table, one row per provider — see
	// PaymentProvider and internal/payments.
	PaymentWebhookSecret string `json:"-"`

	// External REST API: the stable, unguessable URL segment the API is mounted
	// under (/<api_path>/v1/...). Empty ⇒ the API surface is disabled. Kept
	// separate from PanelSecretPath so rotating the panel secret never breaks
	// integrations. Keys themselves live in the api_keys table.
	APIPath string `json:"-"`

	// NodeAPIPath is the unguessable URL segment the node sync API is mounted under
	// (/<node_api_path>/v1/{join,sync}). Empty ⇒ no nodes exist yet and the surface
	// falls through to the decoy. Kept separate from APIPath and the panel secret so
	// rotating either never orphans a joined node.
	NodeAPIPath string `json:"-"`

	// MasterLabel is the panel server's display name in share-link / subscription
	// config labels (multi-node: "<master> · VLESS"). Empty ⇒ no prefix.
	MasterLabel string `json:"-"`

	// GeoRefreshHours is how often to auto-refresh the geo databases (hours; 0 ⇒
	// never — manual only). Applies to the master and is pushed to nodes.
	GeoRefreshHours int `json:"-"`

	// IPListRefreshHours is how often to auto-refresh the iplist databases (hours;
	// 0 ⇒ never — manual only). Its own cadence, not the geo one: the lists sit on a
	// different clock (their services re-resolve every ~12h) and, unlike the .dat
	// files, they are read only by the panel and never pushed to nodes.
	IPListRefreshHours int `json:"-"`

	Routing RoutingConfig `json:"-"` // structured routing config (Settings → Роутинг)

	// Computed per request (NOT stored). When the active cert isn't CA-trusted (a
	// self-signed fallback), TLSInsecure is set and TLSPinSHA256 carries the hex
	// SHA-256 of that cert so Xray links can pin it (pinnedPeerCertSha256) — clients
	// then trust this exact cert. sing-box/clash use TLSInsecure (skip verify).
	TLSInsecure  bool   `json:"-"`
	TLSPinSHA256 string `json:"-"`

	// NodeLabel is computed per request for multi-node subscriptions: when set, it's
	// appended to every protocol label ("VLESS · Нидерланды") so a client shows which
	// node each entry belongs to. Empty for the local server / single-node installs.
	NodeLabel string `json:"-"`
}

// WarpRegistered reports whether a WARP account has been provisioned.
func (s *Settings) WarpRegistered() bool { return s.WarpPrivateKey != "" }

// TelegramChatIDs parses the comma-separated authorized chat IDs into int64s,
// skipping blanks and unparseable entries.
func (s *Settings) TelegramChatIDs() []int64 {
	var out []int64
	for _, p := range strings.Split(s.TGChatIDs, ",") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		if id, err := strconv.ParseInt(p, 10, 64); err == nil {
			out = append(out, id)
		}
	}
	return out
}

// TelegramAuthorized reports whether the given chat ID is linked to the bot.
func (s *Settings) TelegramAuthorized(id int64) bool {
	for _, c := range s.TelegramChatIDs() {
		if c == id {
			return true
		}
	}
	return false
}

// Admin notification categories (bitmask flags stored in Settings.TGAdminEvents).
// The admin bot only pushes an event whose flag is set. New flags must be appended
// (never renumbered) so existing saved masks keep their meaning.
//
// XrayDown and Cert cover the whole fleet, not just this server: a node has no bot
// of its own, so the panel raises them on its behalf from what the node reports
// (see core.SweepNodeAlerts). XrayDown additionally carries "a node stopped
// answering at all" — the same fact from the operator's side, that server is not
// serving.
const (
	AdminEventRegistered    int64 = 1 << 0 // a new user self-registered via the user bot
	AdminEventExpired       int64 = 1 << 1 // a user's subscription expired
	AdminEventLimited       int64 = 1 << 2 // a user exhausted their traffic quota
	AdminEventDeviceLimited int64 = 1 << 3 // a user exceeded their device limit
	AdminEventXrayDown      int64 = 1 << 4 // Xray crashed and is being restarted
	AdminEventCert          int64 = 1 << 5 // TLS certificate renewed or renewal failed
	AdminEventPayment       int64 = 1 << 6 // payment lifecycle (order created / paid)
	AdminEventAbuse         int64 = 1 << 7 // a user's traffic hit a threat/piracy/gambling list
)

// AdminEventCatalog is the stable key→flag mapping the settings API/UI iterate
// over. Order here is the display order in the panel.
var AdminEventCatalog = []struct {
	Key string
	Bit int64
}{
	{"registered", AdminEventRegistered},
	{"expired", AdminEventExpired},
	{"limited", AdminEventLimited},
	{"device_limited", AdminEventDeviceLimited},
	{"xray_down", AdminEventXrayDown},
	{"cert", AdminEventCert},
	{"payment", AdminEventPayment},
	{"abuse", AdminEventAbuse},
}

// AdminEventEnabled reports whether the given AdminEvent* flag is enabled.
func (s *Settings) AdminEventEnabled(bit int64) bool { return s.TGAdminEvents&bit != 0 }

// Abuse blocklist categories (bitmask flags stored in Settings.AbuseCategories).
// The keys match the abuse.Category strings so the manager can map a bit straight to
// a category. Appended, never renumbered, so saved masks keep their meaning — which
// is why the retired domain bits below are still reserved rather than reused.
const (
	_                int64 = 1 << 0 // retired: threat-intelligence domains
	AbuseCatBadIP    int64 = 1 << 1 // IP-reputation feed
	_                int64 = 1 << 2 // retired: anti-piracy domains
	_                int64 = 1 << 3 // retired: gambling domains
)

// AbuseCategoryCatalog is the stable key→flag mapping the settings API/UI iterate
// over, in display order. Keys equal the abuse.Category strings.
//
// Only the IP feed remains: a domain can only be matched when the destination
// reaches the panel as a domain, and on real traffic it arrives as a bare IP (see
// package abuse).
var AbuseCategoryCatalog = []struct {
	Key string
	Bit int64
}{
	{"badip", AbuseCatBadIP},
}

// AbuseCategoryEnabled reports whether a category bit is active.
func (s *Settings) AbuseCategoryEnabled(bit int64) bool { return s.AbuseCategories&bit != 0 }

// User notification categories (bitmask flags stored in Settings.TGUserEvents).
// Named UserNotify* rather than UserEvent*, which the user journal already uses for
// something else entirely — one records what happened, this decides what gets said.
// Separate from the AdminEvent* set even where the subject matches: "чей-то трафик
// закончился" and "ваш трафик закончился" are different messages to different people,
// and an operator watching their own alerts should not thereby be writing to
// customers.
const (
	UserNotifyExpiring      int64 = 1 << 0 // subscription runs out soon
	UserNotifyExpired       int64 = 1 << 1 // subscription has run out
	UserNotifyTrafficLow    int64 = 1 << 2 // most of the quota is spent
	UserNotifyLimited       int64 = 1 << 3 // traffic quota exhausted
	UserNotifyDeviceLimited int64 = 1 << 4 // too many devices at once
	UserNotifyDisabled      int64 = 1 << 5 // access switched off by an operator
	UserNotifyPayment       int64 = 1 << 6 // payment confirmed, plan activated
	UserNotifyRegistration  int64 = 1 << 7 // moderated signup approved or rejected
)

// TrafficWarnPercent is how much of the quota must be spent before the "running
// low" notice goes out. Fixed rather than configurable: the useful range is narrow,
// and one more knob to explain buys nothing.
const TrafficWarnPercent = 80

// UserNotifyCatalog is the stable key→flag mapping the settings API and UI iterate
// over. Appending is safe; renaming a key silently resets that toggle.
var UserNotifyCatalog = []struct {
	Key string
	Bit int64
}{
	{"expiring", UserNotifyExpiring},
	{"expired", UserNotifyExpired},
	{"traffic_low", UserNotifyTrafficLow},
	{"limited", UserNotifyLimited},
	{"device_limited", UserNotifyDeviceLimited},
	{"disabled", UserNotifyDisabled},
	{"payment", UserNotifyPayment},
	{"registration", UserNotifyRegistration},
}

// UserNotifyEnabled reports whether the given UserEvent* flag is enabled.
func (s *Settings) UserNotifyEnabled(bit int64) bool { return s.TGUserEvents&bit != 0 }

// ExpiringDays is the warning horizon, clamped to something a person would call a
// warning: zero would fire at the moment of expiry (which the expired notice already
// covers) and a huge value would warn about a subscription that is barely used.
func (s *Settings) ExpiringDays() int {
	switch {
	case s.TGUserExpiringDays < 1:
		return 1
	case s.TGUserExpiringDays > 30:
		return 30
	default:
		return s.TGUserExpiringDays
	}
}

// SupportLink is the t.me URL of the support bot, or "" when support is off or the
// bot's @username was never resolved. Callers render the entry point only for a
// non-empty result, so a half-configured relay shows no dead button — the same
// contract subWebAppURL has for the subscription Mini App.
func (s *Settings) SupportLink() string {
	if !s.TGSupportEnabled || s.TGSupportBotUsername == "" {
		return ""
	}
	return "https://t.me/" + s.TGSupportBotUsername
}

// RegMode is the normalised self-registration mode. It falls back to the legacy
// TGUserRegEnabled bool for rows written before the mode column existed.
func (s *Settings) RegMode() string {
	switch s.TGUserRegMode {
	case RegOpen, RegModeration, RegInvite, RegOff:
		return s.TGUserRegMode
	default:
		if s.TGUserRegEnabled {
			return RegOpen
		}
		return RegOff
	}
}

// RegistrationOpen reports whether new signups are accepted at all (any mode but
// off). RegistrationActivates reports whether a successful signup is active at once
// (open/invite) rather than held for moderation.
func (s *Settings) RegistrationOpen() bool { return s.RegMode() != RegOff }
func (s *Settings) RegistrationActivates() bool {
	m := s.RegMode()
	return m == RegOpen || m == RegInvite
}

// OperaCountries are the Opera VPN regions opera-proxy accepts.
var OperaCountries = []string{"EU", "AS", "AM"}

// OperaCountryOr returns the configured Opera VPN region, defaulting to "EU"
// for an empty or unknown value.
func (s *Settings) OperaCountryOr() string {
	for _, c := range OperaCountries {
		if s.OperaCountry == c {
			return c
		}
	}
	return "EU"
}

// OperaPortOr returns the local Opera proxy port, defaulting to 18080.
func (s *Settings) OperaPortOr() int {
	if s.OperaPort > 0 {
		return s.OperaPort
	}
	return 18080
}

// SubPathOr returns the subscription URL prefix, defaulting to "sub".
func (s *Settings) SubPathOr() string {
	if p := strings.TrimSpace(s.SubPath); p != "" {
		return p
	}
	return "sub"
}

// RealitySID returns the primary (first) REALITY shortId — the one embedded in
// share links and client configs (RealityShortID stores a comma-separated set
// the server accepts).
func (s *Settings) RealitySID() string {
	if i := strings.IndexByte(s.RealityShortID, ','); i >= 0 {
		return s.RealityShortID[:i]
	}
	return s.RealityShortID
}

// RealityServerNames returns the donor SNIs the REALITY inbound accepts
// (RealityDest stores a comma-separated set; the first is the primary).
func (s *Settings) RealityServerNames() []string {
	var out []string
	for _, d := range strings.Split(s.RealityDest, ",") {
		if d = strings.TrimSpace(d); d != "" {
			out = append(out, d)
		}
	}
	return out
}

// RealitySNI returns the primary (first) donor domain — used as the dialed dest
// and the sni= in share links.
func (s *Settings) RealitySNI() string {
	if ns := s.RealityServerNames(); len(ns) > 0 {
		return ns[0]
	}
	return strings.TrimSpace(s.RealityDest)
}

// fpOr returns fp, defaulting to "firefox" when empty.
func fpOr(fp string) string {
	if fp != "" {
		return fp
	}
	return "firefox"
}

// VLESSFP / TrojanFP / RealityFP return the per-connection uTLS fingerprint for
// share links, each defaulting to "firefox".
func (s *Settings) VLESSFP() string   { return fpOr(s.VLESSFp) }
func (s *Settings) TrojanFP() string  { return fpOr(s.TrojanFp) }
func (s *Settings) RealityFP() string { return fpOr(s.RealityFp) }

// ProtoLabel returns the display name for a protocol constant (ProtoVLESS, …):
// the admin-configured custom name when set, otherwise the constant itself. Used
// for the share-link node label and the sing-box/Clash node tag. A nil receiver
// falls back to the constant so link builders stay safe.
func (s *Settings) ProtoLabel(proto string) string {
	if s == nil {
		return proto
	}
	var custom string
	switch proto {
	case ProtoVLESS:
		custom = s.VLESSName
	case ProtoReality:
		custom = s.RealityName
	case ProtoTrojan:
		custom = s.TrojanName
	case ProtoHysteria:
		custom = s.HysteriaName
	}
	label := proto
	if custom = strings.TrimSpace(custom); custom != "" {
		label = custom
	}
	// Multi-node: prefix with the server name so a client shows "Нидерланды · VLESS"
	// — server first, then protocol.
	if s.NodeLabel != "" {
		return s.NodeLabel + " · " + label
	}
	return label
}

// Fingerprints are the uTLS ClientHello fingerprints offered in the UI.
var Fingerprints = []string{
	"firefox", "chrome", "safari", "edge", "ios", "android", "random", "randomized",
}

// ValidFingerprint reports whether fp is an offered uTLS fingerprint.
func ValidFingerprint(fp string) bool {
	for _, f := range Fingerprints {
		if f == fp {
			return true
		}
	}
	return false
}

// RoutingConfig is the structured routing configuration (Settings → Роутинг).
// Each field is a category of destinations handled the same way; domain entries
// are raw Xray matchers (plain host, "domain:", "keyword:", "regexp:",
// "geosite:", "ext:file:tag") and IP entries are CIDRs or "geoip:xx".
type RoutingConfig struct {
	BlockBittorrent bool     `json:"block_bittorrent"`
	BlockAds        bool     `json:"block_ads"` // block geosite:category-ads-all
	BlockIPs        []string `json:"block_ips"` // CIDRs or geoip:xx
	BlockDomains    []string `json:"block_domains"`
	WarpDomains     []string `json:"warp_domains"`  // routed through Cloudflare WARP
	WarpIPs         []string `json:"warp_ips"`      // CIDRs or geoip:xx, via WARP
	OperaDomains    []string `json:"opera_domains"` // routed through Opera VPN
	OperaIPs        []string `json:"opera_ips"`     // CIDRs or geoip:xx, via Opera VPN
	DirectDomains   []string `json:"direct_domains"`
	DirectIPs       []string `json:"direct_ips"`

	// RoutingOrder is the precedence of the egress lanes; first-match-wins. It is a
	// permutation of the built-in lanes ("warp"/"opera"/"direct") plus the ID of
	// every proxy lane in Lanes. The LAST lane is the catch-all ("everything else")
	// — its specific rules are subsumed by a final rule. A config saved before a
	// lane existed simply omits it; the generator back-fills any missing lane rather
	// than dropping it, and drops IDs of lanes that no longer exist.
	RoutingOrder []string `json:"routing_order"`

	// Lanes are the operator-defined proxy egress lanes. Each has its own upstream
	// proxies and its own match rules, so different destinations can leave through
	// different proxies (e.g. a ".ru" lane and a ".com" lane).
	Lanes []EgressLane `json:"lanes"`

	// ProxyRefreshMinutes is how often the URL-sourced proxy lists are re-fetched.
	// 0 means the default (30 min) — kept so configs saved before this was
	// selectable keep auto-refreshing; a negative value means "never".
	ProxyRefreshMinutes int `json:"proxy_refresh_minutes"`

	// Deprecated: the pre-lanes single proxy pool. Only read, never written —
	// MigrateLanes folds these into a Lanes entry on load. Kept so a config saved
	// by an older build still upgrades cleanly.
	ProxyURLs    []string `json:"proxy_urls,omitempty"`
	ProxyManual  []string `json:"proxy_manual,omitempty"`
	ProxyDomains []string `json:"proxy_domains,omitempty"`
	ProxyIPs     []string `json:"proxy_ips,omitempty"`
}

// EgressLane is one named proxy egress: a set of upstream proxies traffic is
// load-balanced across, plus the destinations that should take it. Traffic
// matching Domains/IPs leaves through this lane's proxies; a lane with no live
// proxies is skipped entirely, so its traffic falls through to the next lane.
type EgressLane struct {
	// ID is the stable slug the routing order references and the Xray outbound /
	// balancer tags are derived from. See ValidLaneID for the charset.
	ID      string   `json:"id"`
	Name    string   `json:"name"`    // display name ("Зона .ru")
	Enabled bool     `json:"enabled"` // off ⇒ the lane emits nothing at all
	URLs    []string `json:"urls"`    // proxy-list sources, one proxy per line
	Manual  []string `json:"manual"`  // "scheme://[user:pass@]host:port" entries
	Domains []string `json:"domains"` // destinations routed through this lane
	IPs     []string `json:"ips"`     // CIDRs or "geoip:xx"
}

// MaxEgressLanes caps how many lanes one config may define. Every active lane
// costs an Xray balancer plus an Observatory probe subject, so the ceiling keeps
// a hand-edited config from melting the box.
const MaxEgressLanes = 16

// LegacyProxyLaneID is the ID the pre-lanes proxy pool migrates into. It is
// deliberately the literal "proxy" — the string a pre-lanes RoutingOrder already
// uses for the pool — so a saved precedence keeps pointing at the same lane
// across the upgrade with no rewriting.
const LegacyProxyLaneID = "proxy"

// builtinLanes are the egress lanes that always exist and are not proxy lanes.
// Their names are reserved: a proxy lane may not take one as its ID.
var builtinLanes = []string{"warp", "opera", "direct"}

// BuiltinLanes returns the always-present egress lanes, in default precedence
// (the last one, "direct", is the default catch-all).
func BuiltinLanes() []string {
	return append([]string(nil), builtinLanes...)
}

// ValidLaneID reports whether id is usable as a lane ID: 1–16 lowercase
// alphanumerics, no dashes, and not a built-in lane name.
//
// The no-dash rule is load-bearing, not cosmetic. An Xray balancer selects its
// members by TAG PREFIX, and a lane's members are tagged "proxy-<id>-<n>". Were
// "-" allowed in an ID, lane "ru" (selector "proxy-ru-") would also select the
// members of lane "ru-x" (tagged "proxy-ru-x-0") and silently steal its proxies.
// Barring dashes from IDs makes the trailing "-" of the selector an unambiguous
// terminator.
func ValidLaneID(id string) bool {
	if len(id) == 0 || len(id) > 16 {
		return false
	}
	for _, b := range []byte(id) {
		if (b < 'a' || b > 'z') && (b < '0' || b > '9') {
			return false
		}
	}
	for _, r := range builtinLanes {
		if id == r {
			return false
		}
	}
	return true
}

// MigrateLanes upgrades a config saved before egress lanes existed: the single
// proxy pool becomes one lane (ID "proxy"), so its proxies, rules and place in
// the routing order all survive. It also clears the deprecated fields on a config
// that already has lanes, so they are never written back.
func (rc *RoutingConfig) MigrateLanes() {
	legacy := len(rc.ProxyURLs) + len(rc.ProxyManual) + len(rc.ProxyDomains) + len(rc.ProxyIPs)
	if len(rc.Lanes) == 0 && legacy > 0 {
		rc.Lanes = []EgressLane{{
			ID:      LegacyProxyLaneID,
			Name:    "Прокси",
			Enabled: true,
			URLs:    rc.ProxyURLs,
			Manual:  rc.ProxyManual,
			Domains: rc.ProxyDomains,
			IPs:     rc.ProxyIPs,
		}}
	}
	rc.ProxyURLs, rc.ProxyManual, rc.ProxyDomains, rc.ProxyIPs = nil, nil, nil, nil
}

// ValidateLanes checks the operator-supplied lanes before they are persisted.
// Messages are user-facing (shown in the panel).
func (rc *RoutingConfig) ValidateLanes() error {
	if len(rc.Lanes) > MaxEgressLanes {
		return fmt.Errorf("слишком много полос: максимум %d", MaxEgressLanes)
	}
	seen := make(map[string]struct{}, len(rc.Lanes))
	for _, l := range rc.Lanes {
		if !ValidLaneID(l.ID) {
			return fmt.Errorf("недопустимый идентификатор полосы %q: только латиница и цифры (до 16 символов), имена warp/opera/direct заняты", l.ID)
		}
		if _, dup := seen[l.ID]; dup {
			return fmt.Errorf("дублирующийся идентификатор полосы %q", l.ID)
		}
		seen[l.ID] = struct{}{}
		if strings.TrimSpace(l.Name) == "" {
			return fmt.Errorf("у полосы %q не задано название", l.ID)
		}
	}
	return nil
}

// LaneIDs returns the IDs of the configured proxy lanes, in config order.
func (rc *RoutingConfig) LaneIDs() []string {
	out := make([]string, 0, len(rc.Lanes))
	for _, l := range rc.Lanes {
		out = append(out, l.ID)
	}
	return out
}

// ProxyEndpoint is one outbound proxy in the pool (parsed from a "scheme://
// [user:pass@]host:port" line). Protocol is normalized to "socks" or "http".
type ProxyEndpoint struct {
	Protocol string
	Address  string
	Port     int
	User     string
	Pass     string
}
