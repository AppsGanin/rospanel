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
		v.Links = append(v.Links, namedLink{set.ProtoLabelFor(model.ProtoVLESS, &u), v.VLESS})
	}
	if set.RealityEnabled && access.AllowsBuiltin(model.LocalNodeID, model.LaneReality) {
		v.Reality = link.Reality(u, set)
		v.Links = append(v.Links, namedLink{set.ProtoLabelFor(model.ProtoReality, &u), v.Reality})
	}
	if set.HysteriaEnabled && access.AllowsBuiltin(model.LocalNodeID, model.LaneHysteria) {
		v.Hysteria2 = link.Hysteria2(u, set)
		v.Links = append(v.Links, namedLink{set.ProtoLabelFor(model.ProtoHysteria, &u), v.Hysteria2})
	}
	for _, in := range custom {
		if !access.AllowsInbound(in.ID) {
			continue
		}
		if in.Protocol == model.InbWireGuard {
			// No share-link form either: the config file, as for AmneziaWG below.
			v.Links = append(v.Links, namedLink{link.CustomLabelFor(in, u, set), sub.TurnConfURL(set, u.SubToken, in.ID)})
			continue
		}
		if l := link.Custom(u, in, set); l != "" {
			v.Links = append(v.Links, namedLink{link.CustomLabelFor(in, u, set), l})
		}
	}
	// AmneziaWG has no share-link form; what the card carries is the address of the
	// user's config file for this server, which the Amnezia apps import as is.
	if set.AWGEnabled && set.AWGPort != 0 && access.AllowsBuiltin(model.LocalNodeID, model.LaneAWG) {
		v.Links = append(v.Links, namedLink{set.ProtoLabelFor(model.ProtoAWG, &u), sub.AWGConfURL(set, u.SubToken, model.LocalNodeID)})
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
func (rt *Router) subSettings(local *model.Settings, nodes []*model.Settings) []*model.Settings {
	// The master server's config labels get its display name too (multi-node), so a
	// client can tell the master's entries from the nodes'.
	local.NodeLabel = local.MasterLabel
	local.ServerID = model.LocalNodeID
	local.ServerPlacement = local.MasterPlacement
	return append([]*model.Settings{local}, nodes...)
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
	shared := rt.sharedSubInputs()
	sets := rt.subSettings(local, shared.nodes)
	custom := shared.inbounds
	access, err := rt.mgr.Store().UserAccess(userID)
	if err != nil {
		return nil, err
	}
	// Ordered for THIS client: their country, each server's live load and the
	// operator's weights decide who comes first, and a full server can drop out.
	// Under the manual mode with no weights this is the old order, unchanged.
	servers := sub.Servers(sets, custom, access)
	ordered := sub.Order(servers, local.SubOrderMode, rt.mgr.CountryOfIP(clientIP),
		rt.mgr.OnlineByServer(), rt.mgr.ServersOverTrafficLimit())
	// External servers are the panel's, not any one server's, so they ride along on
	// whichever entry survived the ordering — the master by preference, since that is
	// where they have always appeared. It must not be *only* the master: the master
	// drops out like anything else once it is full with hide-when-full set, and
	// attaching to nothing at all silently took every external server down with it —
	// for a plan whose grants are all external, the whole subscription.
	if ext := shared.ext; len(ext) > 0 && len(ordered) > 0 {
		carrier := 0
		for i := range ordered {
			if ordered[i].Set.ServerID == model.LocalNodeID {
				carrier = i
				break
			}
		}
		ordered[carrier].External = ext
		// A relayed one rides the server it is relayed through, if that one is here.
		for i := range ordered {
			for _, e := range ext {
				if e.RelayLane != "" && e.RelayServerID == ordered[i].Set.ServerID {
					ordered[i].Relays = append(ordered[i].Relays, e)
				}
			}
		}
	}
	return ordered, nil
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
	// Every helper below puts the route behind the session check — so a new sensitive
	// route can't silently be added without auth — and additionally pins what the
	// caller must hold to reach it.
	//
	// Every one of them also routes the handler through rt.audited, which writes the
	// admin trail (see audit.go). It sits INSIDE the auth check, so the row already
	// knows who is acting; and it is applied here, once, rather than in each handler
	// — that is what makes "no mutating route ships unaudited" a property of the
	// router instead of a habit.
	//
	// on gates a route on the permissions it needs — holding any one of them opens it
	// (see model/perms.go). The owner holds every permission, so owner-only surfaces
	// (the roster and the roles) use authedOwner instead: no permission reaches them,
	// because a role that could edit roles could edit its own into everything.
	//
	// Every route is registered through one of these, so a new sensitive route cannot
	// ship without a permission: there is no helper that registers "signed in, and
	// nothing else" apart from authedAny, kept for the caller's own account.
	register := func(pattern string, gate func(http.HandlerFunc) http.HandlerFunc, h http.HandlerFunc) {
		rt.routes = append(rt.routes, pattern) // for the exhaustiveness test
		mux.HandleFunc(pattern, gate(rt.audited(pattern, h)))
	}
	on := func(perms ...string) func(string, http.HandlerFunc) {
		return func(pattern string, h http.HandlerFunc) {
			if rt.routePerms == nil {
				rt.routePerms = map[string][]string{}
			}
			rt.routePerms[pattern] = perms
			register(pattern, func(next http.HandlerFunc) http.HandlerFunc {
				return rt.requirePerm(perms, next)
			}, h)
		}
	}
	authedAny := func(pattern string, h http.HandlerFunc) { // any signed-in admin
		register(pattern, rt.requireAuth, h)
	}
	authedOwner := func(pattern string, h http.HandlerFunc) { // owner only
		register(pattern, rt.requireOwner, h)
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
	authedOwnerID := func(pattern string, h func(http.ResponseWriter, *http.Request, int64)) {
		authedOwner(pattern, withID(h))
	}
	canAPI := on(model.PermAPI)
	canAudit := on(model.PermAudit)
	canBillingManage := on(model.PermBillingManage)
	canBillingView := on(model.PermBillingView)
	canBroadcasts := on(model.PermBroadcasts)
	// The settings blob backs several screens — general settings, the servers page,
	// the security cards and the local backup schedule — so each of their view
	// permissions reads it.
	canDashboard := on(model.PermStatsView, model.PermUsersView, model.PermServersView)
	canConfigView := on(model.PermSettingsView, model.PermServersView, model.PermRoutingView, model.PermSecurityView)
	canGroupsManage := on(model.PermGroupsManage)
	canGroupsView := on(model.PermGroupsView)
	canLogs := on(model.PermLogs)
	canPayments := on(model.PermPayments)
	canPlansView := on(model.PermBillingView, model.PermUsersView)
	canStatsManage := on(model.PermStatsManage)
	canRoutingManage := on(model.PermRoutingManage)
	canRoutingView := on(model.PermRoutingView)
	canSecurityManage := on(model.PermSecurityManage)
	canSecurityView := on(model.PermSecurityView)
	canServersManage := on(model.PermServersManage)
	canServersOrRoutingView := on(model.PermRoutingView, model.PermServersView)
	canServersView := on(model.PermServersView)
	canSettingsManage := on(model.PermSettingsManage)
	canSettingsView := on(model.PermSettingsView)
	canStatsOrUsersView := on(model.PermStatsView, model.PermUsersView)
	canStatsView := on(model.PermStatsView)
	canUpdate := on(model.PermUpdate)
	canUsersDelete := on(model.PermUsersDelete)
	canUsersExport := on(model.PermUsersExport)
	canUsersManage := on(model.PermUsersManage)
	canUsersManageOrDelete := on(model.PermUsersManage, model.PermUsersDelete)
	canUsersOrGroupsView := on(model.PermUsersView, model.PermGroupsView)
	canGroupsList := on(model.PermGroupsView, model.PermUsersView, model.PermBillingView)
	canUsersView := on(model.PermUsersView)
	canWebhooks := on(model.PermWebhooks)
	mux.HandleFunc("POST /api/login", rt.login)
	mux.HandleFunc("POST /api/logout", rt.logout)
	// Branding reads are unauthenticated: the login screen (under the secret path)
	// renders the panel name/accent/logo before any session exists.
	mux.HandleFunc("GET /api/branding", rt.getBranding)
	mux.HandleFunc("GET /api/branding/logo", rt.brandingLogo)
	canSettingsManage("POST /api/settings/branding", rt.saveBranding)
	canSettingsManage("POST /api/settings/branding/logo", rt.uploadBrandingLogo)
	canSettingsManage("DELETE /api/settings/branding/logo", rt.deleteBrandingLogo)
	// Your own account: every role reaches these, whatever it holds — including
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
	authedAny("GET /api/changelog", rt.changelog)
	authedAny("GET /api/account/sessions", rt.listSessions)
	authedAny("DELETE /api/account/sessions/{id}", withID(rt.revokeSession))
	authedAny("POST /api/account/sessions/revoke-others", rt.revokeOtherSessions)
	// The admin trail: who signed in from where, who created or removed whom, who
	// changed what setting. The owner's, and any role granted it.
	canAudit("GET /api/admin-audit", rt.adminAudit)
	canAudit("GET /api/admin-audit/catalog", rt.adminAuditCatalog)
	canAudit("GET /api/admin-audit/export", rt.exportAdminAudit)
	authedOwner("GET /api/admins", rt.listAdmins)
	authedOwner("POST /api/admins", rt.createAdmin)
	authedOwnerID("POST /api/admins/{id}/role", rt.setAdminRole)
	authedOwnerID("POST /api/admins/{id}/password", rt.resetAdminPassword)
	authedOwnerID("DELETE /api/admins/{id}", rt.deleteAdmin)
	// Roles — what each admin may see and do. The owner's alone, like the roster.
	authedOwner("GET /api/roles", rt.listRoles)
	authedOwner("POST /api/roles", rt.createRole)
	authedOwner("POST /api/roles/{key}", rt.updateRole)
	authedOwner("DELETE /api/roles/{key}", rt.deleteRole)
	canUpdate("GET /api/update", rt.checkUpdate)
	canUpdate("POST /api/update", rt.applyUpdate)
	canSettingsManage("POST /api/setup/timezone", rt.setupTimezone)
	canSettingsManage("POST /api/setup/finish", rt.setupFinish)
	canConfigView("GET /api/settings", rt.getSettings)
	canSettingsManage("POST /api/settings/secret", rt.regenSecret)
	// The decoy is per server (the master's here; a node's rides PATCH /api/nodes/{id}),
	// so it is the servers' like the rest of a server card.
	canServersManage("POST /api/settings/decoy", rt.setDecoyTemplate)
	canSettingsManage("POST /api/settings/subscription", rt.saveSubSettings)
	canSettingsView("GET /api/settings/sub-rules", rt.getSubRules)
	canSettingsManage("POST /api/settings/sub-rules", rt.saveSubRules)
	canSettingsView("GET /api/settings/sub-templates", rt.getSubTemplates)
	canSettingsManage("POST /api/settings/sub-templates", rt.saveSubTemplates)
	canSettingsManage("POST /api/settings/sub-dpi", rt.saveSubDPI)
	canSettingsManage("POST /api/settings/hwid", rt.saveHWIDSettings)
	canSettingsManage("POST /api/settings/maintenance", rt.saveMaintenance)
	canSecurityManage("POST /api/settings/probe-detect", rt.saveProbeDetect)
	canSecurityManage("POST /api/settings/probe-block", rt.saveProbeBlock)
	canSettingsManage("POST /api/settings/watchdog", rt.saveWatchdog)
	canSecurityView("GET /api/security/probes", rt.listProbes)
	// Where clients may connect from (panel_connpolicy.go).
	canSecurityView("GET /api/security/conn-policy", rt.getConnPolicy)
	canSecurityManage("POST /api/security/conn-policy", rt.saveConnPolicy)
	canSecurityView("GET /api/security/trusted", rt.getTrustedNets)
	canSecurityManage("POST /api/security/trusted", rt.saveTrustedNets)
	canSecurityManage("POST /api/security/unblock", rt.unblockIP)
	// Bans by hand and every ban the panel holds (panel_bans.go).
	canSecurityView("GET /api/security/bans", rt.listBans)
	canSecurityManage("POST /api/security/bans", rt.banIP)
	canSecurityManage("POST /api/security/unban", rt.unbanIP)
	canSettingsView("GET /api/settings/status-page", rt.getStatusPage)
	canSettingsManage("POST /api/settings/status-page", rt.saveStatusPage)
	canRoutingManage("POST /api/settings/dns", rt.setXrayDNS)
	authedOwner("POST /api/settings/local-backup", rt.setLocalBackup)
	canSettingsManage("POST /api/settings/autodelete", rt.setUserAutoDelete)
	// External subscriptions: other people's servers handed on to users.
	canServersView("GET /api/external", rt.listExternal)
	canServersManage("POST /api/external", rt.createExternal)
	canServersManage("DELETE /api/external/{id}", withID(rt.deleteExternal))
	canServersManage("POST /api/external/{id}/source", withID(rt.updateExternalSource))
	canServersManage("POST /api/external/{id}/sync", withID(rt.syncExternal))
	canServersManage("POST /api/external/{id}/enabled", withID(rt.setExternalEnabled))
	canServersManage("POST /api/external/{id}/relay", withID(rt.setExternalRelay))
	canServersManage("POST /api/external/{id}/servers", withID(rt.setExternalServersEnabled))
	canServersManage("POST /api/external/servers/{id}/enabled", withID(rt.setExternalServerEnabled))
	canSecurityView("GET /api/settings/abuse", rt.getAbuseSettings)
	canSecurityManage("POST /api/settings/abuse", rt.saveAbuseSettings)
	canSecurityManage("POST /api/settings/abuse/refresh", rt.refreshAbuse)
	canRoutingView("GET /api/geo/categories", rt.geoCategories)
	canServersOrRoutingView("GET /api/geo", rt.geoStatus)
	canRoutingManage("POST /api/geo/update", rt.updateGeo)
	canRoutingManage("POST /api/geo/lists/update", rt.updateIPLists)
	canRoutingManage("POST /api/geo/lists/cadence", rt.setIPListCadence)
	canRoutingManage("POST /api/geo/cadence", rt.setGeoCadence)
	canRoutingView("GET /api/routing", rt.getRouting)
	canRoutingManage("POST /api/routing", rt.saveRouting)
	canServersView("GET /api/config/snapshots", rt.listConfigSnapshots)
	canServersManage("POST /api/config/snapshots", rt.createConfigSnapshot)
	canServersManage("POST /api/config/snapshots/{id}/rollback", withID(rt.rollbackConfigSnapshot))
	canServersManage("DELETE /api/config/snapshots/{id}", withID(rt.deleteConfigSnapshot))
	// The dashboard: user counts, online, throughput — the figures stats, users and
	// servers each show part of. Health is the servers' diagnostics.
	canDashboard("GET /api/system/stream", rt.systemStream)
	canServersView("GET /api/health", rt.health)
	canServersManage("GET /api/xray/config", rt.xrayConfig)
	canServersView("GET /api/xray/status", rt.xrayStatus)
	canServersManage("POST /api/xray/restart", rt.xrayRestart)
	canLogs("GET /api/xray/logs/stream", rt.xrayLogs)
	canLogs("GET /api/logs/stream", rt.appLogs)
	// Backups and restore are the owner's: a backup is the whole database (the owner's
	// password hash and second factor included) and a restore replaces the admin
	// roster, so either reaches past any permission. The factory reset with them.
	authedOwner("GET /api/backup", rt.downloadBackup)
	authedOwner("GET /api/backup/info", rt.backupInfo)
	authedOwner("POST /api/backup/inspect", rt.inspectBackup)
	authedOwner("POST /api/restore", rt.uploadRestore)
	authedOwner("POST /api/reset", rt.factoryReset)
	canUpdate("POST /api/panel/restart", rt.restartPanel)
	canServersView("GET /api/connections", rt.connections)
	canServersManage("POST /api/connections", rt.applyConnections)
	canServersManage("POST /api/connections/reset", rt.resetConnections)
	// User groups: which connections a member may use. The user card reads the list
	// too, and putting a user into groups from the card is a user-management action.
	// Read by the users section and by the plan editor too: a plan grants groups.
	canGroupsList("GET /api/groups", rt.listGroups)
	canGroupsView("GET /api/groups/targets", rt.groupTargets)
	canGroupsManage("POST /api/groups", rt.createGroup)
	canGroupsManage("POST /api/groups/{id}", withID(rt.updateGroup))
	canGroupsManage("DELETE /api/groups/{id}", withID(rt.deleteGroup))
	canGroupsManage("POST /api/groups/{id}/members", withID(rt.setGroupMembers))
	canUsersManage("POST /api/users/{id}/groups", withID(rt.setUserGroups))
	// End users and their journal.
	canUsersView("GET /api/users", rt.listUsers)
	canUsersView("GET /api/users/page", rt.listUsersPage)
	canUsersOrGroupsView("GET /api/users/brief", rt.listUsersBrief)
	canUsersView("GET /api/users/{id}", withID(rt.getUser))
	canUsersManage("POST /api/users", rt.createUser)
	canUsersManageOrDelete("POST /api/users/bulk", rt.bulkUsers) // per action, see bulkUsers
	canUsersDelete("DELETE /api/users/{id}", withID(rt.deleteUser))
	canUsersManage("POST /api/users/{id}/reset", withID(rt.resetUserTraffic))
	canUsersManage("POST /api/users/{id}/limits", withID(rt.setUserLimits))
	canUsersManage("POST /api/users/{id}/enabled", withID(rt.setUserEnabled))
	canUsersManage("POST /api/users/{id}/name", withID(rt.renameUser))
	canUsersManage("POST /api/users/{id}/note", withID(rt.setUserNote))
	canUsersManage("POST /api/users/{id}/tags", withID(rt.setUserTags))
	canUsersView("GET /api/users/tags", rt.userTags)
	// Import from another panel (see panel_import.go): inspect reads, import writes.
	canUsersManage("POST /api/users/import/inspect", rt.importInspect)
	canUsersManage("POST /api/users/import", rt.importUsers)
	// The export is its own permission: one file with every credential (see exportUsers).
	canUsersExport("GET /api/users/export", rt.exportUsers)
	canUsersView("GET /api/users/{id}/connections", withID(rt.userConnections))
	canUsersView("GET /api/users/{id}/devices", withID(rt.userDevices))
	canUsersManage("POST /api/users/{id}/devices/unbind", withID(rt.unbindUserDevice))
	canUsersView("GET /api/users/{id}/abuse", withID(rt.userAbuse))
	canUsersManage("POST /api/users/{id}/rotate-sub", withID(rt.rotateSubToken))
	canUsersView("GET /api/users/{id}/happ-link", withID(rt.userHappLink))
	canUsersManage("POST /api/users/{id}/telegram/unlink", withID(rt.unlinkUserTelegram))
	canUsersManage("POST /api/users/{id}/telegram/link", withID(rt.genUserTelegramLink))
	canUsersManage("POST /api/users/{id}/telegram/message", withID(rt.messageUser))
	canUsersManage("POST /api/users/{id}/reset-period", withID(rt.setResetPeriod))
	canUsersManage("POST /api/users/{id}/plan", withID(rt.setUserPlan))
	canUsersView("GET /api/users/{id}/events", withID(rt.userEvents))
	// Moderated self-registration queue (empty unless the user bot's mode is moderation).
	canUsersView("GET /api/registrations", rt.listRegistrations)
	canUsersManage("POST /api/registrations/{id}/approve", withID(rt.approveRegistration))
	canUsersManage("POST /api/registrations/{id}/reject", withID(rt.rejectRegistration))
	canUsersView("GET /api/events", rt.events)
	canUsersView("GET /api/events/catalog", rt.eventCatalog)
	// Read-only: the user card lists the plans it can assign. The billing *settings*
	// (POST below) and the payment provider keys need permissions of their own.
	canPlansView("GET /api/billing", rt.getBilling)
	canBillingManage("POST /api/billing", rt.saveBilling)
	canBillingManage("POST /api/billing/plans", rt.saveTariffPlan)
	canBillingManage("DELETE /api/billing/plans/{id}", withID(rt.deleteTariffPlan))
	canBillingManage("POST /api/billing/plans/{id}/migrate", withID(rt.migratePlanUsers))
	canBillingView("GET /api/billing/orders", rt.listPaymentOrders)
	canBillingManage("POST /api/billing/orders/{id}/confirm", withID(rt.confirmPaymentOrder))
	canBillingManage("POST /api/billing/orders/{id}/cancel", withID(rt.cancelPaymentOrder))
	canPayments("GET /api/payments", rt.getPayments)
	canPayments("POST /api/payments", rt.savePayments)
	canBillingView("GET /api/payments/stats", rt.paymentStats)
	canStatsOrUsersView("GET /api/stats/series", rt.statsSeries)
	canStatsOrUsersView("GET /api/stats/nodes", rt.statsNodes)
	canStatsView("GET /api/stats/users", rt.statsByUser)
	canStatsView("GET /api/stats/abuse", rt.statsAbuse)
	canStatsView("GET /api/stats/countries", rt.statsCountries)
	canStatsView("GET /api/stats/asns", rt.statsASNs)
	canStatsManage("POST /api/stats/reset", rt.statsReset)
	canServersView("GET /api/tls", rt.tlsStatus)
	canServersManage("POST /api/tls", rt.setACME)
	canAPI("GET /api/apikeys", rt.listAPIKeys)
	canAPI("POST /api/apikeys", rt.createAPIKey)
	canAPI("DELETE /api/apikeys/{id}", withID(rt.revokeAPIKey))
	canAPI("POST /api/settings/api-path", rt.setAPIPathSettings)
	canServersOrRoutingView("GET /api/nodes", rt.listNodes)
	canServersManage("POST /api/nodes/master-name", rt.setMasterName)
	canServersManage("POST /api/nodes/master-placement", rt.setMasterPlacement)
	canServersManage("POST /api/nodes/master-protocols", rt.setMasterProtocols)
	canServersManage("POST /api/nodes/master-reality", rt.setMasterReality)
	canServersManage("POST /api/nodes", rt.createNode)
	canServersManage("PATCH /api/nodes/{id}", withID(rt.updateNode))
	canRoutingManage("POST /api/nodes/{id}/routing", withID(rt.setNodeRouting))
	canRoutingManage("POST /api/nodes/{id}/dns", withID(rt.setNodeDNS))
	// System proxy, per server: {id} 0 is the master, anything else a node.
	canRoutingManage("POST /api/nodes/{id}/proxy", withID(rt.setServerProxy))
	canServersManage("POST /api/nodes/{id}/reality", withID(rt.setNodeReality))
	canServersView("GET /api/nodes/{id}/connections", withID(rt.nodeConnections))
	canServersManage("POST /api/nodes/{id}/connections", withID(rt.applyNodeConnections))
	canServersManage("POST /api/nodes/{id}/connections/reset", withID(rt.resetNodeConnections))
	// Custom inbounds. The list/create routes are keyed by SERVER id (0 = master);
	// edit/delete are keyed by the inbound's own id, which already implies its server.
	canServersView("GET /api/inbounds/catalog", rt.inboundCatalog)
	canServersView("GET /api/servers/{id}/inbounds", withID(rt.serverInbounds))
	canServersManage("POST /api/servers/{id}/inbounds", withID(rt.createServerInbound))
	canServersManage("POST /api/inbounds/{id}", withID(rt.updateInbound))
	canServersManage("DELETE /api/inbounds/{id}", withID(rt.deleteInbound))
	canServersManage("POST /api/inbounds/{id}/regen-reality", withID(rt.regenInboundReality))
	canServersView("GET /api/nodes/{id}/tls", withID(rt.nodeTLS))
	canServersManage("POST /api/nodes/{id}/tls", withID(rt.setNodeACME))
	canServersOrRoutingView("GET /api/nodes/{id}/geo", withID(rt.nodeGeoInfo))
	canRoutingManage("POST /api/nodes/{id}/geo-refresh", withID(rt.nodeGeoRefresh))
	canRoutingManage("POST /api/nodes/{id}/geo-cadence", withID(rt.nodeGeoCadence))
	canLogs("GET /api/nodes/{id}/logs", withID(rt.nodeLogs))
	canServersManage("GET /api/nodes/{id}/xray-config", withID(rt.nodeXrayConfig))
	canServersView("GET /api/nodes/{id}/health", withID(rt.nodeHealth))
	canServersManage("DELETE /api/nodes/{id}", withID(rt.deleteNode))
	canServersManage("POST /api/nodes/{id}/enabled", withID(rt.setNodeEnabled))
	canServersManage("POST /api/nodes/{id}/regen-join", withID(rt.regenNodeJoin))
	canUpdate("POST /api/nodes/{id}/update", withID(rt.updateNodeVersion))
	canServersManage("POST /api/nodes/{id}/xray-restart", withID(rt.nodeXrayRestart))
	canUpdate("POST /api/nodes/update-all", rt.updateAllNodes)
	canServersManage("POST /api/nodes/{id}/provision", withID(rt.provisionNode))
	canWebhooks("GET /api/webhooks", rt.listWebhooks)
	canWebhooks("POST /api/webhooks", rt.createWebhook)
	canWebhooks("POST /api/webhooks/{id}", withID(rt.updateWebhook))
	canWebhooks("DELETE /api/webhooks/{id}", withID(rt.deleteWebhook))
	canWebhooks("POST /api/webhooks/{id}/test", withID(rt.testWebhook))
	// The bots are the owner's: the admin bot's linked chat receives every admin's
	// sign-in alerts and can end any admin's sessions, and its token and proxy decide
	// where full backups go — no permission reaches that far (see model.PermImplies).
	authedOwner("GET /api/telegram", rt.getTelegram)
	authedOwner("POST /api/telegram", rt.saveTelegram)
	authedOwner("POST /api/telegram/link", rt.genTelegramLink)
	authedOwner("GET /api/telegram/link/status", rt.telegramLinkStatus)
	authedOwner("POST /api/telegram/link/cancel", rt.cancelTelegramLink)
	authedOwner("POST /api/telegram/unlink", rt.unlinkTelegram)
	authedOwner("POST /api/telegram/test-backup", rt.testTelegramBackup)
	authedOwner("GET /api/telegram/support/groups", rt.listSupportGroups)
	authedOwner("POST /api/telegram/support/check", rt.checkTelegramSupport)
	// Mass broadcasts through the user bot — their own permission: one reaches every
	// subscriber.
	canBroadcasts("GET /api/broadcasts", rt.listBroadcasts)
	canBroadcasts("POST /api/broadcasts", rt.createBroadcast)
	canBroadcasts("GET /api/broadcasts/audience", rt.broadcastAudience)
	canBroadcasts("POST /api/broadcasts/test", rt.testBroadcast)
	canBroadcasts("GET /api/broadcasts/{id}", withID(rt.getBroadcast))
	canBroadcasts("POST /api/broadcasts/{id}/pause", withID(rt.pauseBroadcast))
	canBroadcasts("POST /api/broadcasts/{id}/resume", withID(rt.resumeBroadcast))
	canBroadcasts("POST /api/broadcasts/{id}/cancel", withID(rt.cancelBroadcast))
	canBroadcasts("POST /api/broadcasts/{id}/retry", withID(rt.retryBroadcast))
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
	return rt.notingWrites(mux)
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
		"perms":                callerPerms(r).List(),
		"setup_done":           true,
		"timezone":             "",
		"version":              version.Version,
		"must_change_password": a.MustChangePassword,
	}
	// Whether this admin has a second factor. A UI hint only — it decides whether the
	// destructive-action dialogs ask for a code — and the server checks for real
	// regardless, so an error here degrades towards ASKING rather than towards a
	// dialog with no field to type the required code into.
	totp, err := rt.mgr.Store().AdminTOTPByID(a.ID)
	resp["totp_enabled"] = err != nil || totp.Enabled()
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

// stepUpBody is how an irreversible action carries its credentials. A JSON body even
// on DELETE, because an HTTP header cannot hold them: the Fetch API restricts header
// values to ISO-8859-1, so a browser refuses outright to send a Cyrillic password and
// silently mangles an accented one into a byte string that can never match the stored
// hash — a correct password answered "wrong password", with nothing to explain it.
// Passwords have no charset restriction, so this is not a corner case.
type stepUpBody struct {
	CurrentPassword string `json:"current_password"`
	Code            string `json:"code"`
}

// verifyStepUpTOTP is verifyStepUp plus a FRESH second factor, for the handful of
// actions that destroy something no backup taken afterwards can bring back.
//
// The password alone is the wrong bar for those: it is the credential most likely to
// be reused elsewhere, and an admin who has bound an authenticator has already said
// they want a second one. The code is required only when that admin actually has 2FA
// — turning it on must not become a prerequisite for operating the panel.
//
// "Fresh" is load-bearing. The step is claimed through MarkAdminTOTPStep exactly as
// login claims it, so the code that just signed the admin in cannot also authorise
// the deletion: an attacker who watched one code over a shoulder gets one action, not
// every action inside the same 30 seconds. The cost is real and deliberate — two
// destructive actions in one window need two codes — so the refusal says so.
func (rt *Router) verifyStepUpTOTP(w http.ResponseWriter, r *http.Request, password, code string) bool {
	// Deliberately NOT verifyStepUp: that one waives re-authentication entirely while
	// the first-run wizard is unfinished, and "unfinished" is a state a panel can sit
	// in forever — the wizard clears the forced password change several steps before it
	// marks setup done, so an abandoned wizard leaves a working panel with the shortcut
	// open. There is nothing to factory-reset or delete during first run anyway, so
	// asking costs nothing and closes the window.
	if !rt.verifyAdminPassword(w, r, password) {
		return false
	}
	id, ok := rt.adminID(r)
	if !ok {
		writeErrCode(w, http.StatusUnauthorized, "err.unauthorized", "не авторизован")
		return false
	}
	totp, err := rt.mgr.Store().AdminTOTPByID(id)
	if err != nil {
		// A secret that will not decrypt must never read as "no second factor" — that
		// would turn a broken encryption key into an open door.
		writeErrCode(w, http.StatusInternalServerError, "err.internal", "внутренняя ошибка сервера")
		return false
	}
	if !totp.Enabled() {
		return true
	}
	ip := clientIP(r)
	if rt.stepUp.blocked(ip, "") {
		// The attacker this counts is already past the password and holds a session, so
		// nothing but a lockout on THIS endpoint slows the walk through a six-digit
		// space. Checked before the code is even read.
		writeErrCode(w, http.StatusTooManyRequests, "err.tooManyAttempts", "слишком много попыток, попробуйте позже")
		return false
	}
	code = strings.TrimSpace(code)
	if code == "" {
		writeErrCode(w, http.StatusForbidden, "err.totpRequired", "введите код из приложения")
		return false
	}
	// Verified WITHOUT the replay marker, then compared against it separately. Folding
	// the two together (as the login does) collapses "wrong code" and "the code you
	// just signed in with" into one message — and here the second is the likely one,
	// because the admin often reaches a destructive action seconds after logging in.
	// Security is unchanged: the step still has to beat the marker, and the atomic
	// claim below is what actually settles a race between two requests.
	step, ok := auth.VerifyTOTP(totp.Secret, code, time.Now(), 0)
	if !ok {
		// Counted against the lockout for the same reason the login counts it: six
		// digits is a small space, and whoever is guessing here has already got past
		// the password. The legitimate case costs nothing — a correct code never
		// reaches this branch.
		rt.stepUp.fail(ip, "")
		writeErrCode(w, http.StatusForbidden, "err.totpInvalid", "неверный код")
		return false
	}
	if step <= totp.LastStep {
		writeErrCode(w, http.StatusForbidden, "err.totpUsed", "этот код уже использован — дождитесь следующего")
		return false
	}
	rt.stepUp.success(ip, "")
	// Claim the step before acting, so a replayed code cannot drive the action twice.
	claimed, err := rt.mgr.Store().MarkAdminTOTPStep(id, step)
	if err != nil {
		writeErrCode(w, http.StatusInternalServerError, "err.internal", "внутренняя ошибка сервера")
		return false
	}
	if !claimed {
		writeErrCode(w, http.StatusForbidden, "err.totpUsed", "этот код уже использован — дождитесь следующего")
		return false
	}
	return true
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

// requirePerm is requireAuth plus the permissions a route needs: the caller must hold
// at least one of perms (see model.PermSet.Any). The set was resolved from the
// admin's role in the same session lookup, so a role edited a moment ago already
// counts; a role key nothing answers to resolves to no permissions at all.
//
// A caller without the permission gets 403, never 401: their session is perfectly
// valid, so the SPA must show "not enough permissions" rather than bounce them to
// the login screen.
func (rt *Router) requirePerm(perms []string, next http.HandlerFunc) http.HandlerFunc {
	return rt.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		a, ok := sessionAdminFrom(r.Context())
		if !ok || !a.Perms.Any(perms...) {
			slog.Warn("panel: permission check failed",
				"admin", a.Username, "role", a.Role, "need", strings.Join(perms, "|"), "path", r.URL.Path)
			writeErrCode(w, http.StatusForbidden, "err.forbidden", "недостаточно прав")
			return
		}
		next(w, r)
	})
}

// requireOwner is requireAuth for the owner alone — the roster and the roles, which
// no permission reaches.
func (rt *Router) requireOwner(next http.HandlerFunc) http.HandlerFunc {
	return rt.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		a, ok := sessionAdminFrom(r.Context())
		if !ok || a.Role != model.RoleOwner {
			slog.Warn("panel: owner check failed", "admin", a.Username, "role", a.Role, "path", r.URL.Path)
			writeErrCode(w, http.StatusForbidden, "err.forbidden", "недостаточно прав")
			return
		}
		next(w, r)
	})
}

// callerPerms is what the signed-in admin holds — for a handler that shapes its
// answer by permission rather than being gated by one.
func callerPerms(r *http.Request) model.PermSet {
	a, ok := sessionAdminFrom(r.Context())
	if !ok || a.Perms == nil {
		return model.PermSet{}
	}
	return a.Perms
}
