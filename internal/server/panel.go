package server

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/actor"
	"github.com/AppsGanin/rospanel/internal/auth"
	"github.com/AppsGanin/rospanel/internal/core"
	"github.com/AppsGanin/rospanel/internal/link"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/sub"
	"github.com/AppsGanin/rospanel/internal/telegram"
	"github.com/AppsGanin/rospanel/internal/version"
)

// userView is a user plus its derived share links (one credential set, three
// protocols).
type userView struct {
	model.User
	SystemEmail string `json:"system_email"` // Xray client id "u<id>" (logs/stats/links)
	SubURL      string `json:"sub_url"`
	VLESS       string `json:"vless"`
	Hysteria2   string `json:"hysteria2"`
	Reality     string `json:"reality"`
	// Links is every lane this user has on THIS server, built-in and custom, each
	// with the name the client will show. The three fields above are the built-in
	// lanes kept as their own keys for integrations that were written against them;
	// a custom inbound can only appear here, since it has no fixed name to have a
	// field of its own.
	Links []namedLink `json:"links"`
	// Groups the user belongs to (empty ⇒ access to everything). Shown as chips in the
	// user list and detail; editing membership is a separate endpoint.
	Groups           []model.GroupRef `json:"groups"`
	TelegramLinked   bool             `json:"telegram_linked"`
	TelegramLink     string           `json:"telegram_link"`      // public user bot URL
	TelegramDeepLink string           `json:"telegram_deep_link"` // bind this (panel-created) account
}

// namedLink is one share link with the node name a client displays for it.
type namedLink struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// makeUserView builds the API view for a user. userBotUsername is the resolved
// @username of the public user bot ("" when disabled/unresolved) and custom is the
// local server's custom inbounds — both passed in so the caller resolves them once
// per request rather than per user.
func makeUserView(u model.User, set *model.Settings, userBotUsername string, custom []model.Inbound, groups []model.GroupRef, access model.Access) userView {
	v := userView{
		User:           u,
		SystemEmail:    model.UserEmail(u.ID),
		SubURL:         sub.URL(set, u.SubToken),
		Groups:         groups,
		TelegramLinked: u.TgChatID != 0,
	}
	if v.Groups == nil {
		v.Groups = []model.GroupRef{}
	}
	// The bind deep link is no longer embedded here (it carried the permanent
	// sub-token). It's now minted on demand as a one-time code via
	// POST /api/users/{id}/telegram/link.
	if set.TGUserBotEnabled && userBotUsername != "" && u.TgChatID == 0 {
		v.TelegramLink = telegram.UserBotLink(userBotUsername)
	}
	// A lane switched off in the Connections panel drops out of the user's links, and
	// so does one the user's groups don't grant — the admin view mirrors what the user
	// actually gets, not just what exists.
	if set.VLESSEnabled && access.AllowsBuiltin(model.LocalNodeID, model.LaneVLESS) {
		v.VLESS = link.VLESS(u, set)
		v.Links = append(v.Links, namedLink{set.ProtoLabel(model.ProtoVLESS), v.VLESS})
	}
	if set.RealityEnabled && access.AllowsBuiltin(model.LocalNodeID, model.LaneReality) {
		v.Reality = link.Reality(u, set)
		v.Links = append(v.Links, namedLink{set.ProtoLabel(model.ProtoReality), v.Reality})
	}
	if set.HysteriaEnabled && access.AllowsBuiltin(model.LocalNodeID, model.LaneHysteria) {
		v.Hysteria2 = link.Hysteria2(u, set)
		v.Links = append(v.Links, namedLink{set.ProtoLabel(model.ProtoHysteria), v.Hysteria2})
	}
	for _, in := range custom {
		if !access.AllowsInbound(in.ID) {
			continue
		}
		if l := link.Custom(u, in, set); l != "" {
			v.Links = append(v.Links, namedLink{link.CustomLabel(in, set), l})
		}
	}
	// AmneziaWG has no share-link form; what the card carries is the address of the
	// user's config file for this server, which the Amnezia apps import as is.
	if set.AWGEnabled && set.AWGPort != 0 && access.AllowsBuiltin(model.LocalNodeID, model.LaneAWG) {
		v.Links = append(v.Links, namedLink{set.ProtoLabel(model.ProtoAWG), sub.AWGConfURL(set, u.SubToken, model.LocalNodeID)})
	}
	return v
}

// userViewFor builds a user's view, resolving their groups and access. For one user
// (the create/patch/get handlers); the list handlers use the batch maps instead of a
// query per row.
func (rt *Router) userViewFor(u model.User, set *model.Settings, bot string) userView {
	groups, _ := rt.mgr.GroupsForUser(u.ID)
	access, err := rt.mgr.Store().UserAccess(u.ID)
	if err != nil {
		access = model.UnrestrictedAccess()
	}
	return makeUserView(u, set, bot, rt.localInbounds(), groups, access)
}

// applyTLSHints fills the per-request TLS fields used by link/sub generation. When
// the active cert isn't CA-trusted (a self-signed fallback), it flags TLSInsecure
// and attaches the cert pin so Xray links can pin it (pinnedPeerCertSha256); a
// trusted CA cert leaves verification on.
func (rt *Router) applyTLSHints(set *model.Settings) {
	if rt.mgr.HasValidCert() {
		return
	}
	set.TLSInsecure = true
	set.TLSPinSHA256 = rt.mgr.CertPinSHA256()
}

// subSettings returns the list of servers a subscription spans: the local server
// (with its TLS hints already applied by the caller) first, then each enabled,
// connected node. With no nodes it returns just the local set, so single-server
// output is unchanged. `local` must already have applyTLSHints called on it.
func (rt *Router) subSettings(local *model.Settings) []*model.Settings {
	// The master server's config labels get its display name too (multi-node), so a
	// client can tell the master's entries from the nodes'.
	local.NodeLabel = local.MasterLabel
	local.ServerID = model.LocalNodeID
	local.ServerPlacement = local.MasterPlacement
	sets := []*model.Settings{local}
	if nodes, err := rt.mgr.NodeLinkSettings(); err == nil {
		sets = append(sets, nodes...)
	}
	return sets
}

// subServers is subSettings paired with each server's custom inbounds and the
// requesting user's access — the shape every subscription builder consumes.
//
// A custom-inbound read failure degrades to built-in lanes only rather than failing the
// whole subscription: that direction hands out LESS than the user is entitled to, and a
// user who can't fetch a config has no way back in.
//
// An access read failure is the opposite direction and is refused. Degrading to
// unrestricted used to look like the same kindness, but it hands a restricted user the
// addresses of every lane on every server — while config generation treats the identical
// failure as fatal (see genOptsFor), so the credential is withheld and the links cannot
// work anyway. Failing the fetch locks nobody out: a client that cannot refresh keeps the
// config it already has and tries again later.
func (rt *Router) subServers(local *model.Settings, userID int64, clientIP string) ([]sub.Server, error) {
	sets := rt.subSettings(local)
	custom, err := rt.mgr.Store().AllInbounds()
	if err != nil {
		custom = nil
	}
	access, err := rt.mgr.Store().UserAccess(userID)
	if err != nil {
		return nil, err
	}
	// Ordered for THIS client: their country, each server's live load and the
	// operator's weights decide who comes first, and a full server can drop out.
	// Under the manual mode with no weights this is the old order, unchanged.
	servers := sub.Servers(sets, custom, access)
	return sub.Order(servers, local.SubOrderMode, rt.mgr.CountryOfIP(clientIP), rt.mgr.OnlineByServer()), nil
}

// localInbounds is the master's own custom inbounds, or none when they can't be
// read (the user views degrade to the built-in lanes rather than erroring).
func (rt *Router) localInbounds() []model.Inbound {
	list, err := rt.mgr.Store().EnabledInbounds(model.LocalNodeID)
	if err != nil {
		return nil
	}
	return list
}

func (rt *Router) panelMux() http.Handler {
	mux := http.NewServeMux()
	// Route tiers. Every helper below puts the route behind the session check — so a
	// new sensitive route can't silently be added without auth — and additionally
	// pins the minimum role that may call it.
	//
	// authed (admin and up) is the default on purpose: a route added later without a
	// second thought lands closed to operators rather than open to them. Opening one
	// up to operators is then a deliberate act — authedOp — visible in this list.
	//
	// Every one of them also routes the handler through rt.audited, which writes the
	// admin trail (see audit.go). It sits INSIDE the auth check, so the row already
	// knows who is acting; and it is applied here, once, rather than in each handler
	// — that is what makes "no mutating route ships unaudited" a property of the
	// router instead of a habit.
	register := func(tier, pattern string, h http.HandlerFunc) {
		rt.routes = append(rt.routes, pattern) // for the exhaustiveness test
		mux.HandleFunc(pattern, rt.requireRole(tier, rt.audited(pattern, h)))
	}
	authedAny := func(pattern string, h http.HandlerFunc) { // any signed-in admin
		rt.routes = append(rt.routes, pattern)
		mux.HandleFunc(pattern, rt.requireAuth(rt.audited(pattern, h)))
	}
	authedOp := func(pattern string, h http.HandlerFunc) { // operator and up
		register(model.RoleOperator, pattern, h)
	}
	authed := func(pattern string, h http.HandlerFunc) { // admin and up
		register(model.RoleAdmin, pattern, h)
	}
	authedOwner := func(pattern string, h http.HandlerFunc) { // owner only
		register(model.RoleOwner, pattern, h)
	}
	// withID adapts a handler for routes carrying an {id} segment: it parses (and
	// validates) the id once, so the handler receives it directly instead of
	// repeating the pathID/ok dance.
	withID := func(h func(http.ResponseWriter, *http.Request, int64)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if id, ok := pathID(w, r); ok {
				h(w, r, id)
			}
		}
	}
	authedID := func(pattern string, h func(http.ResponseWriter, *http.Request, int64)) {
		authed(pattern, withID(h))
	}
	authedOpID := func(pattern string, h func(http.ResponseWriter, *http.Request, int64)) {
		authedOp(pattern, withID(h))
	}
	authedOwnerID := func(pattern string, h func(http.ResponseWriter, *http.Request, int64)) {
		authedOwner(pattern, withID(h))
	}
	mux.HandleFunc("POST /api/login", rt.login)
	mux.HandleFunc("POST /api/logout", rt.logout)
	// Branding reads are unauthenticated: the login screen (under the secret path)
	// renders the panel name/accent/logo before any session exists.
	mux.HandleFunc("GET /api/branding", rt.getBranding)
	mux.HandleFunc("GET /api/branding/logo", rt.brandingLogo)
	authed("POST /api/settings/branding", rt.saveBranding)
	authed("POST /api/settings/branding/logo", rt.uploadBrandingLogo)
	authed("DELETE /api/settings/branding/logo", rt.deleteBrandingLogo)
	// Your own account: every role reaches these, whatever their tier — including
	// while gated on a forced password change (see mustChangeAllowed), which is the
	// only way out of that state.
	authedAny("GET /api/me", rt.me)
	authedAny("POST /api/setup/password", rt.setupPassword)
	authedAny("POST /api/account/credentials", rt.updateCredentials)
	// The caller's own second factor (no id in the path — see panel_totp.go).
	authedAny("GET /api/account/totp", rt.totpStatus)
	authedAny("POST /api/account/totp/start", rt.totpStart)
	authedAny("POST /api/account/totp/enable", rt.totpEnable)
	authedAny("POST /api/account/totp/disable", rt.totpDisable)
	// The caller's own open sessions (see panel_sessions.go). Same shape as 2FA: no
	// id of another admin anywhere in the path.
	authedAny("GET /api/account/sessions", rt.listSessions)
	authedAny("DELETE /api/account/sessions/{id}", withID(rt.revokeSession))
	authedAny("POST /api/account/sessions/revoke-others", rt.revokeOtherSessions)
	// The admin roster and its trail — owner only. Who signed in from where, who
	// created or removed whom, who changed what setting: same tier as the roster
	// itself.
	authedOwner("GET /api/admin-audit", rt.adminAudit)
	authedOwner("GET /api/admin-audit/catalog", rt.adminAuditCatalog)
	authedOwner("GET /api/admin-audit/export", rt.exportAdminAudit)
	authedOwner("GET /api/admins", rt.listAdmins)
	authedOwner("POST /api/admins", rt.createAdmin)
	authedOwnerID("POST /api/admins/{id}/role", rt.setAdminRole)
	authedOwnerID("POST /api/admins/{id}/password", rt.resetAdminPassword)
	authedOwnerID("DELETE /api/admins/{id}", rt.deleteAdmin)
	authed("GET /api/update", rt.checkUpdate)
	authed("POST /api/update", rt.applyUpdate)
	authed("POST /api/setup/timezone", rt.setupTimezone)
	authed("POST /api/setup/finish", rt.setupFinish)
	authed("GET /api/settings", rt.getSettings)
	authed("POST /api/settings/secret", rt.regenSecret)
	authed("POST /api/settings/decoy", rt.setDecoyTemplate)
	authed("POST /api/settings/subscription", rt.saveSubSettings)
	authed("GET /api/settings/sub-rules", rt.getSubRules)
	authed("POST /api/settings/sub-rules", rt.saveSubRules)
	authed("POST /api/settings/sub-dpi", rt.saveSubDPI)
	authed("POST /api/settings/hwid", rt.saveHWIDSettings)
	authed("POST /api/settings/maintenance", rt.saveMaintenance)
	authed("POST /api/settings/probe-detect", rt.saveProbeDetect)
	authed("POST /api/settings/probe-block", rt.saveProbeBlock)
	authed("POST /api/settings/watchdog", rt.saveWatchdog)
	authed("GET /api/security/probes", rt.listProbes)
	// Where clients may connect from (panel_connpolicy.go).
	authed("GET /api/security/conn-policy", rt.getConnPolicy)
	authed("POST /api/security/conn-policy", rt.saveConnPolicy)
	authed("POST /api/security/unblock", rt.unblockIP)
	authed("GET /api/settings/status-page", rt.getStatusPage)
	authed("POST /api/settings/status-page", rt.saveStatusPage)
	authed("POST /api/settings/dns", rt.setXrayDNS)
	authed("POST /api/settings/local-backup", rt.setLocalBackup)
	authed("POST /api/settings/autodelete", rt.setUserAutoDelete)
	authed("GET /api/settings/abuse", rt.getAbuseSettings)
	authed("POST /api/settings/abuse", rt.saveAbuseSettings)
	authed("POST /api/settings/abuse/refresh", rt.refreshAbuse)
	authed("GET /api/geo/categories", rt.geoCategories)
	authed("GET /api/geo", rt.geoStatus)
	authed("POST /api/geo/update", rt.updateGeo)
	authed("POST /api/geo/lists/update", rt.updateIPLists)
	authed("POST /api/geo/lists/cadence", rt.setIPListCadence)
	authed("POST /api/geo/cadence", rt.setGeoCadence)
	authed("GET /api/routing", rt.getRouting)
	authed("POST /api/routing", rt.saveRouting)
	authed("GET /api/config/snapshots", rt.listConfigSnapshots)
	authed("POST /api/config/snapshots", rt.createConfigSnapshot)
	authedID("POST /api/config/snapshots/{id}/rollback", rt.rollbackConfigSnapshot)
	authedID("DELETE /api/config/snapshots/{id}", rt.deleteConfigSnapshot)
	authedOp("GET /api/system/stream", rt.systemStream)
	authedOp("GET /api/health", rt.health)
	authed("GET /api/xray/config", rt.xrayConfig)
	authed("GET /api/xray/status", rt.xrayStatus)
	authed("POST /api/xray/restart", rt.xrayRestart)
	authed("GET /api/xray/logs/stream", rt.xrayLogs)
	authed("GET /api/logs/stream", rt.appLogs)
	authed("GET /api/backup", rt.downloadBackup)
	authed("GET /api/backup/info", rt.backupInfo)
	authed("POST /api/backup/inspect", rt.inspectBackup)
	authed("POST /api/restore", rt.uploadRestore)
	authed("POST /api/reset", rt.factoryReset)
	authed("POST /api/panel/restart", rt.restartPanel)
	authed("GET /api/connections", rt.connections)
	authed("POST /api/connections", rt.applyConnections)
	// User groups: which connections a member may use. Managed by operators, same tier
	// as users (assigning a user to a group is a user-management action).
	authedOp("GET /api/groups", rt.listGroups)
	authedOp("GET /api/groups/targets", rt.groupTargets)
	authedOp("POST /api/groups", rt.createGroup)
	authedOpID("POST /api/groups/{id}", rt.updateGroup)
	authedOpID("DELETE /api/groups/{id}", rt.deleteGroup)
	authedOpID("POST /api/groups/{id}/members", rt.setGroupMembers)
	authedOpID("POST /api/users/{id}/groups", rt.setUserGroups)
	// End users, the journal and stats are the operator's job — everything below is
	// open from RoleOperator up.
	authedOp("GET /api/users", rt.listUsers)
	authedOp("POST /api/users", rt.createUser)
	authedOp("POST /api/users/bulk", rt.bulkUsers)
	authedOpID("DELETE /api/users/{id}", rt.deleteUser)
	authedOpID("POST /api/users/{id}/reset", rt.resetUserTraffic)
	authedOpID("POST /api/users/{id}/limits", rt.setUserLimits)
	authedOpID("POST /api/users/{id}/enabled", rt.setUserEnabled)
	authedOpID("POST /api/users/{id}/name", rt.renameUser)
	authedOpID("POST /api/users/{id}/note", rt.setUserNote)
	authedOpID("POST /api/users/{id}/tags", rt.setUserTags)
	authedOp("GET /api/users/tags", rt.userTags)
	// Import from another panel (see panel_import.go): inspect reads, import writes.
	authedOp("POST /api/users/import/inspect", rt.importInspect)
	authedOp("POST /api/users/import", rt.importUsers)
	// The export is admin-level: one file with every credential (see exportUsers).
	authed("GET /api/users/export", rt.exportUsers)
	authedOpID("GET /api/users/{id}/connections", rt.userConnections)
	authedOpID("GET /api/users/{id}/devices", rt.userDevices)
	authedOpID("POST /api/users/{id}/devices/unbind", rt.unbindUserDevice)
	authedOpID("GET /api/users/{id}/abuse", rt.userAbuse)
	authedOpID("POST /api/users/{id}/rotate-sub", rt.rotateSubToken)
	authedOpID("POST /api/users/{id}/telegram/unlink", rt.unlinkUserTelegram)
	authedOpID("POST /api/users/{id}/telegram/link", rt.genUserTelegramLink)
	authedOpID("POST /api/users/{id}/telegram/message", rt.messageUser)
	authedOpID("POST /api/users/{id}/reset-period", rt.setResetPeriod)
	authedOpID("POST /api/users/{id}/plan", rt.setUserPlan)
	authedOpID("GET /api/users/{id}/events", rt.userEvents)
	// Moderated self-registration queue (empty unless the user bot's mode is moderation).
	authedOp("GET /api/registrations", rt.listRegistrations)
	authedOpID("POST /api/registrations/{id}/approve", rt.approveRegistration)
	authedOpID("POST /api/registrations/{id}/reject", rt.rejectRegistration)
	authedOp("GET /api/events", rt.events)
	authedOp("GET /api/events/catalog", rt.eventCatalog)
	// Read-only: the user card lists the plans it can assign. The billing *settings*
	// (POST below) and the payment provider keys stay admin-only.
	authedOp("GET /api/billing", rt.getBilling)
	authed("POST /api/billing", rt.saveBilling)
	authed("POST /api/billing/plans", rt.saveTariffPlan)
	authedID("DELETE /api/billing/plans/{id}", rt.deleteTariffPlan)
	authedID("POST /api/billing/plans/{id}/migrate", rt.migratePlanUsers)
	authed("GET /api/billing/orders", rt.listPaymentOrders)
	authedID("POST /api/billing/orders/{id}/confirm", rt.confirmPaymentOrder)
	authedID("POST /api/billing/orders/{id}/cancel", rt.cancelPaymentOrder)
	authed("GET /api/payments", rt.getPayments)
	authed("POST /api/payments", rt.savePayments)
	authed("GET /api/payments/stats", rt.paymentStats)
	authedOp("GET /api/stats/series", rt.statsSeries)
	authedOp("GET /api/stats/nodes", rt.statsNodes)
	authedOp("GET /api/stats/users", rt.statsByUser)
	authedOp("GET /api/stats/abuse", rt.statsAbuse)
	authedOp("GET /api/stats/countries", rt.statsCountries)
	authedOp("GET /api/stats/asns", rt.statsASNs)
	authed("POST /api/stats/reset", rt.statsReset)
	authed("GET /api/tls", rt.tlsStatus)
	authed("POST /api/tls", rt.setACME)
	authed("GET /api/apikeys", rt.listAPIKeys)
	authed("POST /api/apikeys", rt.createAPIKey)
	authedID("DELETE /api/apikeys/{id}", rt.revokeAPIKey)
	authed("POST /api/settings/api-path", rt.setAPIPathSettings)
	authed("GET /api/nodes", rt.listNodes)
	authed("POST /api/nodes/master-name", rt.setMasterName)
	authed("POST /api/nodes/master-placement", rt.setMasterPlacement)
	authed("POST /api/nodes/master-protocols", rt.setMasterProtocols)
	authed("POST /api/nodes/master-reality", rt.setMasterReality)
	authed("POST /api/nodes", rt.createNode)
	authedID("PATCH /api/nodes/{id}", rt.updateNode)
	authedID("POST /api/nodes/{id}/routing", rt.setNodeRouting)
	authedID("POST /api/nodes/{id}/dns", rt.setNodeDNS)
	// System proxy, per server: {id} 0 is the master, anything else a node.
	authedID("POST /api/nodes/{id}/proxy", rt.setServerProxy)
	authedID("POST /api/nodes/{id}/reality", rt.setNodeReality)
	authedID("GET /api/nodes/{id}/connections", rt.nodeConnections)
	authedID("POST /api/nodes/{id}/connections", rt.applyNodeConnections)
	// Custom inbounds. The list/create routes are keyed by SERVER id (0 = master);
	// edit/delete are keyed by the inbound's own id, which already implies its server.
	authed("GET /api/inbounds/catalog", rt.inboundCatalog)
	authedID("GET /api/servers/{id}/inbounds", rt.serverInbounds)
	authedID("POST /api/servers/{id}/inbounds", rt.createServerInbound)
	authedID("POST /api/inbounds/{id}", rt.updateInbound)
	authedID("DELETE /api/inbounds/{id}", rt.deleteInbound)
	authedID("POST /api/inbounds/{id}/regen-reality", rt.regenInboundReality)
	authedID("GET /api/nodes/{id}/tls", rt.nodeTLS)
	authedID("POST /api/nodes/{id}/tls", rt.setNodeACME)
	authedID("GET /api/nodes/{id}/geo", rt.nodeGeoInfo)
	authedID("POST /api/nodes/{id}/geo-refresh", rt.nodeGeoRefresh)
	authedID("POST /api/nodes/{id}/geo-cadence", rt.nodeGeoCadence)
	authedID("GET /api/nodes/{id}/logs", rt.nodeLogs)
	authedID("GET /api/nodes/{id}/xray-config", rt.nodeXrayConfig)
	authedID("GET /api/nodes/{id}/health", rt.nodeHealth)
	authedID("DELETE /api/nodes/{id}", rt.deleteNode)
	authedID("POST /api/nodes/{id}/enabled", rt.setNodeEnabled)
	authedID("POST /api/nodes/{id}/regen-join", rt.regenNodeJoin)
	authedID("POST /api/nodes/{id}/update", rt.updateNodeVersion)
	authedID("POST /api/nodes/{id}/xray-restart", rt.nodeXrayRestart)
	authed("POST /api/nodes/update-all", rt.updateAllNodes)
	authedID("POST /api/nodes/{id}/provision", rt.provisionNode)
	authed("GET /api/webhooks", rt.listWebhooks)
	authed("POST /api/webhooks", rt.createWebhook)
	authedID("POST /api/webhooks/{id}", rt.updateWebhook)
	authedID("DELETE /api/webhooks/{id}", rt.deleteWebhook)
	authedID("POST /api/webhooks/{id}/test", rt.testWebhook)
	authed("GET /api/telegram", rt.getTelegram)
	authed("POST /api/telegram", rt.saveTelegram)
	authed("POST /api/telegram/link", rt.genTelegramLink)
	authed("GET /api/telegram/link/status", rt.telegramLinkStatus)
	authed("POST /api/telegram/link/cancel", rt.cancelTelegramLink)
	authed("POST /api/telegram/unlink", rt.unlinkTelegram)
	authed("POST /api/telegram/test-backup", rt.testTelegramBackup)
	authed("GET /api/telegram/support/groups", rt.listSupportGroups)
	authed("POST /api/telegram/support/check", rt.checkTelegramSupport)
	// Mass broadcasts through the user bot (admin tier: it reaches every subscriber).
	authed("GET /api/broadcasts", rt.listBroadcasts)
	authed("POST /api/broadcasts", rt.createBroadcast)
	authed("GET /api/broadcasts/audience", rt.broadcastAudience)
	authed("POST /api/broadcasts/test", rt.testBroadcast)
	authedID("GET /api/broadcasts/{id}", rt.getBroadcast)
	authedID("POST /api/broadcasts/{id}/pause", rt.pauseBroadcast)
	authedID("POST /api/broadcasts/{id}/resume", rt.resumeBroadcast)
	authedID("POST /api/broadcasts/{id}/cancel", rt.cancelBroadcast)
	authedID("POST /api/broadcasts/{id}/retry", rt.retryBroadcast)
	// Content-hashed build assets (JS/CSS/fonts) never change for a given URL → cache forever.
	mux.Handle("GET /assets/", cacheControl(rt.assets, "public, max-age=31536000, immutable"))
	// No /favicon.* routes: the build has no such files. Vite emits only what is
	// imported, under a hashed name in assets/, and the tab's icon comes from
	// api/branding/logo — which index.html names, so the browser never falls back to
	// the conventional root name. The three routes here answered 404 for as long as
	// they existed.
	// One catch-all for every method, not just GET. With "GET /" alone, a request
	// this mux has no route for but whose METHOD differs — a stale tab still PATCHing
	// an endpoint that has been removed — fell through to net/http's own 405, which
	// answers text/plain "Method Not Allowed". That is the same failure as issue #70
	// wearing a different status code: a caller expecting JSON gets prose.
	mux.HandleFunc("/", rt.fallback)
	return mux
}

// cookiePath scopes the session cookie to the secret path so it never leaks on
// decoy requests.
func (rt *Router) cookiePath() string { return "/" + rt.currentSecret() + "/" }

func (rt *Router) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		// Code is the second factor. Absent on the first attempt: the panel asks for it
		// only after the password checks out, so the field never tells an attacker
		// whether an account exists or has 2FA.
		Code string `json:"code"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	ip := clientIP(r)
	username := strings.TrimSpace(req.Username)
	if rt.limiter.blocked(ip, username) {
		slog.Warn("login: rate-limited", "ip", ip)
		writeErrCode(w, http.StatusTooManyRequests, "err.tooManyAttempts", "слишком много попыток, повторите позже")
		return
	}

	// Sign-ins are audited here rather than by the audit middleware: a failed one —
	// the row actually worth having — never reaches a success path, and neither
	// attempt has a session for the middleware to read an actor from. The attempted
	// login is recorded as the target; it is not a secret, and "someone tried to sign
	// in as owner from 1.2.3.4, twelve times" is the whole point of the row.
	auditLogin := func(action string) {
		rt.mgr.AddAdminAudit(model.AdminAudit{
			Action: action, Target: username,
			ActorKind: model.ActorAdmin, ActorName: username, IP: ip,
		})
	}

	id, hash, role, err := rt.mgr.Store().GetAdminAuth(username)
	if err != nil {
		// Unknown user: equalize timing against the real verify path.
		auth.DummyVerify()
		rt.limiter.fail(ip, username)
		slog.Warn("login: unknown user", "ip", ip)
		auditLogin(model.AuditLoginFailed)
		writeErrCode(w, http.StatusUnauthorized, "err.badCredentials", "неверный логин или пароль")
		return
	}
	if !auth.VerifyPassword(hash, req.Password) {
		rt.limiter.fail(ip, username)
		slog.Warn("login: bad password", "user", username, "ip", ip)
		auditLogin(model.AuditLoginFailed)
		writeErrCode(w, http.StatusUnauthorized, "err.badCredentials", "неверный логин или пароль")
		return
	}
	// Second factor, when this admin has one. Everything up to here has already
	// established that the password is right; what remains is proving possession.
	totp, err := rt.mgr.Store().AdminTOTPByID(id)
	if err != nil {
		slog.Error("login: could not read the second factor", "user", username, "err", err)
		writeErrCode(w, http.StatusInternalServerError, "err.internal", "внутренняя ошибка сервера")
		return
	}
	if totp.Enabled() {
		if strings.TrimSpace(req.Code) == "" {
			// Not a failure in spirit — the password was right and the client simply has
			// not been asked for a code yet — but it is still counted. Reaching here costs
			// a full password hash, and this is exactly the attacker 2FA is for: one who
			// HAS the password and is stopped by the code. Left uncounted, that attacker
			// gets an unbounded loop of expensive hashes, which is a way to take the panel
			// down rather than into. The legitimate flow pays nothing: the very next
			// attempt carries the code, and a successful sign-in clears the counters.
			rt.limiter.fail(ip, username)
			writeErrCode(w, http.StatusUnauthorized, "err.totpRequired", "введите код из приложения")
			return
		}
		step, ok := auth.VerifyTOTP(totp.Secret, req.Code, time.Now(), totp.LastStep)
		if !ok {
			// A wrong code counts against the lockout: six digits is a space worth
			// brute-forcing when the password is already known.
			rt.limiter.fail(ip, username)
			slog.Warn("login: bad second factor", "user", username, "ip", ip)
			auditLogin(model.AuditLoginFailed)
			writeErrCode(w, http.StatusUnauthorized, "err.totpInvalid", "неверный код")
			return
		}
		// Claim the code BEFORE anything is recorded as a success. Verification only
		// READ the marker, so two requests holding the same code can both get this far;
		// the row claim is what actually makes a code one-time, and whoever loses it is
		// refused. Claiming before the session also means a crash between the two costs
		// one sign-in — the other order would leave a spent code still working.
		won, err := rt.mgr.Store().MarkAdminTOTPStep(id, step)
		if err != nil {
			slog.Error("login: could not record the second-factor step", "user", username, "err", err)
			writeErrCode(w, http.StatusInternalServerError, "err.internal", "внутренняя ошибка сервера")
			return
		}
		if !won {
			rt.limiter.fail(ip, username)
			slog.Warn("login: second-factor code already spent", "user", username, "ip", ip)
			auditLogin(model.AuditLoginFailed)
			writeErrCode(w, http.StatusUnauthorized, "err.totpInvalid", "неверный код")
			return
		}
	}

	rt.limiter.success(ip, username)
	// Judged before this sign-in writes its own row, or every sign-in would find
	// itself and read as known.
	newAddress := rt.mgr.AdminLoginIsNew(username, ip)
	auditLogin(model.AuditLogin)

	token, err := rt.mgr.Store().CreateSessionFrom(id, sessionTTLSec*time.Second, ip, r.UserAgent())
	if err != nil {
		slog.Error("login: session creation failed", "err", err)
		writeErrCode(w, http.StatusInternalServerError, "err.sessionCreateFailed", "не удалось создать сессию")
		return
	}
	// Best-effort: the roster shows it, nothing depends on it, and a failed write
	// must not cost an otherwise valid login.
	if err := rt.mgr.Store().TouchAdminLogin(id); err != nil {
		slog.Warn("login: could not record last-login", "user", username, "err", err)
	}
	slog.Info("login: authenticated", "user", req.Username, "role", role, "ip", ip)
	if newAddress {
		// Off the request path: Telegram may be slow or blocked, and the sign-in is
		// already done.
		go rt.mgr.NotifyAdminLogin(core.LoginAlert{
			AdminID: id, Username: username, IP: ip, Client: core.ClientLabel(r.UserAgent()),
		})
	}
	rt.setSessionCookie(w, r, token, rt.cookiePath())
	writeOK(w)
}

// setSessionCookie writes the session cookie scoped to the given panel path.
// Secure is set unconditionally: the panel is only ever reached over Xray's
// TLS-terminated :443, even though r.TLS is nil here (the request arrives over the
// plaintext loopback fallback after Xray terminated TLS). Keying Secure off r.TLS
// would wrongly drop the flag and let the session ride an accidental plaintext path.
func (rt *Router) setSessionCookie(w http.ResponseWriter, r *http.Request, token, path string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     path,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   sessionTTLSec,
	})
}

func (rt *Router) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		// Resolve who is leaving before the session is destroyed — afterwards there is
		// nothing left to attribute the row to.
		if a, ok := rt.mgr.Store().LookupSession(c.Value); ok {
			rt.mgr.AddAdminAudit(model.AdminAudit{
				Action:    model.AuditLogout,
				ActorKind: model.ActorAdmin,
				ActorName: a.Username,
				IP:        clientIP(r),
			})
		}
		_ = rt.mgr.Store().DeleteSession(c.Value)
	}
	// Match every attribute the session cookie was set with (see setSessionCookie),
	// not just the name and path: a browser only overwrites a cookie when Secure and
	// SameSite line up too, so a bare deletion can leave the Secure cookie in place —
	// and it keeps this expiry off any accidental plaintext path all the same.
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: rt.cookiePath(),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
	writeOK(w)
}

func (rt *Router) me(w http.ResponseWriter, r *http.Request) {
	// requireAuth already resolved the session; reading it back off the context keeps
	// this from being the one place that could disagree with what the gate saw.
	a, _ := sessionAdminFrom(r.Context())
	resp := map[string]any{
		"username":             a.Username,
		"role":                 a.Role,
		"setup_done":           true,
		"timezone":             "",
		"version":              version.Version,
		"must_change_password": a.MustChangePassword,
	}
	if set, err := rt.mgr.Store().GetSettings(); err == nil {
		resp["setup_done"] = set.SetupDone
		resp["timezone"] = set.Timezone
		resp["billing_enabled"] = set.BillingEnabled
		// Broadcasts and per-user messages both go through the user bot, so the SPA
		// hides those surfaces entirely when it is off rather than offering an action
		// the server would refuse.
		resp["user_bot_enabled"] = set.TGUserBotEnabled
	}
	writeJSON(w, http.StatusOK, resp)
}

// ctxKeyAdmin carries the resolved session admin down the request. requireAuth is
// the only writer, so a handler that reads it is looking at the same account the
// auth gate and the role check just approved.
type ctxKeyAdmin struct{}

func sessionAdminFrom(ctx context.Context) (store.SessionAdmin, bool) {
	a, ok := ctx.Value(ctxKeyAdmin{}).(store.SessionAdmin)
	return a, ok
}

// adminID returns the authenticated admin's id.
func (rt *Router) adminID(r *http.Request) (int64, bool) {
	a, ok := sessionAdminFrom(r.Context())
	return a.ID, ok
}

// verifyStepUp re-checks the admin password before a sensitive operation. It is
// skipped while the first-run wizard is still in progress (!SetupDone) — the
// session was only just issued and the operator is completing guided setup.
// On failure it writes the error response and returns false.
func (rt *Router) verifyStepUp(w http.ResponseWriter, r *http.Request, password string) bool {
	set, err := rt.mgr.Store().GetSettings()
	if err != nil {
		writeErrCode(w, http.StatusInternalServerError, "err.internal", "внутренняя ошибка сервера")
		return false
	}
	if !set.SetupDone {
		return true
	}
	return rt.verifyAdminPassword(w, r, password)
}

// verifyAdminPassword checks the current admin password (step-up for sensitive
// ops). On failure it writes the error response and returns false.
//
// A missing/expired session is 401 (the SPA treats that as "session gone" and
// drops to the login screen). A WRONG step-up password, though, must NOT be 401:
// the session is still valid, only this one action is refused — so it returns 403
// and the SPA shows the error inline instead of logging the admin out.
func (rt *Router) verifyAdminPassword(w http.ResponseWriter, r *http.Request, password string) bool {
	id, ok := rt.adminID(r)
	if !ok {
		writeErrCode(w, http.StatusUnauthorized, "err.unauthorized", "не авторизован")
		return false
	}
	hash, err := rt.mgr.Store().GetAdminHash(id)
	if err != nil {
		writeErrCode(w, http.StatusInternalServerError, "err.internal", "внутренняя ошибка сервера")
		return false
	}
	if !auth.VerifyPassword(hash, password) {
		writeErrCode(w, http.StatusForbidden, "err.wrongPassword", "неверный пароль")
		return false
	}
	return true
}

// mustChangeAllowed lists the only panel paths reachable while the admin still has
// the default password (must_change_password). They let the operator get OUT of
// that state — change the password (wizard / account settings) or restore a backup
// (which replaces the credentials wholesale) — and nothing else, so a panel whose
// secret path leaks before first setup can't be driven with admin/admin (no user
// management, settings, backup download, or factory reset). Paths are matched after
// the secret prefix is stripped, e.g. "/api/setup/password".
var mustChangeAllowed = map[string]bool{
	"/api/me":                  true,
	"/api/logout":              true,
	"/api/setup/password":      true,
	"/api/account/credentials": true,
	"/api/backup/info":         true,
	"/api/backup/inspect":      true,
	"/api/restore":             true,
	// The first-run wizard reads TLS status on mount (before the password is
	// changed, so must_change is still set) to show the correct address step —
	// "already on domain <host>" vs "over IP". Without this it 403s, the wizard
	// silently falls back to the IP wording and claims a self-signed cert even when
	// a real domain cert is live. Read-only; the wizard's POST /api/tls (issue cert)
	// runs later, after the password step has already cleared must_change.
	"/api/tls": true,
}

// requireAuth rejects requests without a valid session. Because this only runs
// under the secret path, a 401 here never reveals the panel to outsiders. While the
// admin still carries a password someone else picked, it also blocks everything but
// the password-change / restore endpoints (see mustChangeAllowed).
//
// The gate is per-account: a colleague who has not yet replaced the temporary
// password the owner handed them is locked to the password screen, while everyone
// else keeps working.
func (rt *Router) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			writeErrCode(w, http.StatusUnauthorized, "err.unauthorized", "не авторизован")
			return
		}
		a, ok := rt.mgr.Store().LookupSession(c.Value)
		if !ok {
			writeErrCode(w, http.StatusUnauthorized, "err.unauthorized", "не авторизован")
			return
		}
		if !mustChangeAllowed[r.URL.Path] && a.MustChangePassword {
			writeErrCode(w, http.StatusForbidden, "err.mustChangePassword", "смените пароль, прежде чем пользоваться панелью")
			return
		}
		// Keep the session's "last used from" current, at most once a minute: the
		// account screen lists open sessions by it, and a cookie used from a new
		// address is what that list exists to show. Best-effort — a failed stamp
		// must not cost a valid request.
		if now := time.Now().Unix(); now-a.LastSeenAt >= int64(store.SessionTouchInterval.Seconds()) {
			if err := rt.mgr.Store().TouchSession(a.SessionID, now, clientIP(r)); err != nil {
				slog.Warn("session: could not stamp last-seen", "admin", a.Username, "err", err)
			}
		}
		// Stamp the acting admin onto the context so the audit log can attribute every
		// mutation this request makes, without each handler re-reading the cookie, and
		// carry the resolved session for the role check and the handlers.
		ctx := actor.With(r.Context(), actor.Admin(a.Username))
		next(w, r.WithContext(context.WithValue(ctx, ctxKeyAdmin{}, a)))
	}
}

// requireRole is requireAuth plus a floor on the caller's role. Roles are a ladder
// (operator < admin < owner), so the check is a rank comparison — see model.RoleAtLeast.
//
// A caller below the tier gets 403, never 401: their session is perfectly valid, so
// the SPA must show "not enough permissions" rather than bounce them to the login screen.
func (rt *Router) requireRole(tier string, next http.HandlerFunc) http.HandlerFunc {
	return rt.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		a, ok := sessionAdminFrom(r.Context())
		if !ok || !model.RoleAtLeast(a.Role, tier) {
			slog.Warn("panel: role check failed",
				"admin", a.Username, "role", a.Role, "need", tier, "path", r.URL.Path)
			writeErrCode(w, http.StatusForbidden, "err.forbidden", "недостаточно прав")
			return
		}
		next(w, r)
	})
}
