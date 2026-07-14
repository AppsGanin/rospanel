package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/AppsGanin/rospanel/internal/actor"
	"github.com/AppsGanin/rospanel/internal/model"
)

// The admin audit trail is written HERE, in the router, rather than inside the ~30
// handlers that mutate the panel. One table of route → action means a new settings
// endpoint cannot quietly ship unaudited: auditActions is exhaustive over the
// mutating routes, and TestEveryMutatingRouteIsAudited fails the build if it isn't.
//
// What is NOT recorded: request bodies. They carry bot tokens, payment provider
// keys and passwords, and an audit log that leaks the secrets it is meant to police
// is worse than none. A row says who did what, to what, from where — never with
// which value. Handlers that know a meaningful, non-secret target (an admin login, a
// key name) attach it explicitly via auditTarget.

// auditRoute is what one route records: the action key, and — for the settings
// routes, which all share a single action — the section it touched, which lands in
// the row's target so the journal reads "Изменение настроек · Маршрутизация".
type auditRoute struct {
	action  string
	section string
}

// skip marks a route deliberately left out of this trail.
var skip = auditRoute{}

// set builds a settings row: one action for every section of the settings.
func set(section string) auditRoute {
	return auditRoute{action: model.AuditSettings, section: section}
}

func act(action string) auditRoute { return auditRoute{action: action} }

// auditActions maps a panel route pattern — exactly as registered in panelMux — to
// what it records.
//
// A zero value (skip) means "deliberately not audited here": the user routes already
// write a richer, user-scoped row into the user journal (see manager_events.go), and
// duplicating them into the admin trail would double every bulk operation.
var auditActions = map[string]auditRoute{
	// Your own account.
	"POST /api/setup/password":      act(model.AuditPasswordChanged),
	"POST /api/account/credentials": act(model.AuditCredentialsChanged),

	// The roster.
	"POST /api/admins":               act(model.AuditAdminCreated),
	"POST /api/admins/{id}/role":     act(model.AuditAdminRoleChanged),
	"POST /api/admins/{id}/password": act(model.AuditAdminPasswordReset),
	"DELETE /api/admins/{id}":        act(model.AuditAdminDeleted),

	// Settings — one action, the section in the target.
	"POST /api/settings/branding":        set("Брендинг"),
	"POST /api/settings/branding/logo":   set("Брендинг · логотип"),
	"DELETE /api/settings/branding/logo": set("Брендинг · логотип удалён"),
	"POST /api/settings/secret":          set("Секретный путь"),
	"POST /api/settings/decoy":           set("Заглушка"),
	"POST /api/settings/subscription":    set("Подписки"),
	"POST /api/settings/dns":             set("DNS"),
	"POST /api/settings/proxy-mode":      set("Режим прокси"),
	"POST /api/settings/local-backup":    set("Локальные бэкапы"),
	"POST /api/settings/autodelete":      set("Автоудаление истёкших"),
	"POST /api/settings/api-path":        set("Адрес API"),
	"POST /api/setup/timezone":           set("Часовой пояс"),
	"POST /api/setup/finish":             set("Первичная настройка"),
	"POST /api/routing":                  set("Маршрутизация"),
	"POST /api/connections":              set("Подключения"),
	"POST /api/geo/update":               set("Geo-базы"),
	"POST /api/geo/cadence":              set("Geo-базы · автообновление"),
	"POST /api/tls":                      set("TLS-сертификат"),
	"POST /api/telegram":                 set("Telegram"),
	"POST /api/telegram/link":            set("Telegram · привязка"),
	"POST /api/telegram/link/cancel":     set("Telegram · привязка отменена"),
	"POST /api/telegram/unlink":          set("Telegram · отвязка"),
	"POST /api/telegram/test-backup":     set("Telegram · тестовый бэкап"),
	"POST /api/billing":                  set("Биллинг"),
	"POST /api/payments":                 set("Приём платежей"),

	// Tariff plans keep their own actions: they are objects with a lifecycle, not a
	// settings form — "тариф удалён" is a different question from "кто трогал настройки".
	"POST /api/billing/plans":              act(model.AuditPlanSaved),
	"DELETE /api/billing/plans/{id}":       act(model.AuditPlanDeleted),
	"POST /api/billing/plans/{id}/migrate": act(model.AuditPlanMigrated),
	// Orders are user-scoped and already land in that user's journal, same actor.
	"POST /api/billing/orders/{id}/confirm": skip,
	"POST /api/billing/orders/{id}/cancel":  skip,

	// API keys and webhooks: credentials and outbound endpoints, each with its own
	// lifecycle — worth their own rows, not folded into "настройки".
	"POST /api/apikeys":            act(model.AuditAPIKeyCreated),
	"DELETE /api/apikeys/{id}":     act(model.AuditAPIKeyRevoked),
	"POST /api/webhooks":           act(model.AuditWebhookCreated),
	"POST /api/webhooks/{id}":      act(model.AuditWebhookUpdated),
	"DELETE /api/webhooks/{id}":    act(model.AuditWebhookDeleted),
	"POST /api/webhooks/{id}/test": skip, // a test delivery changes nothing

	// Nodes: each is a managed server with its own lifecycle. One section-style
	// action; the node is the target. regen-join mints a fresh install credential.
	"POST /api/nodes":                  set("Нода добавлена"),
	"POST /api/nodes/master-name":      set("Имя мастера в конфигах"),
	"POST /api/nodes/master-protocols": set("Протоколы мастера"),
	"POST /api/nodes/master-reality":   set("REALITY мастера"),
	"PATCH /api/nodes/{id}":            set("Нода изменена"),
	"POST /api/nodes/{id}/routing":     set("Нода · роутинг"),
	"POST /api/nodes/{id}/dns":         set("Нода · DNS"),
	"POST /api/nodes/{id}/reality":     set("Нода · REALITY"),
	"POST /api/nodes/{id}/connections": set("Нода · подключения"),
	"POST /api/nodes/{id}/tls":         set("Нода · домен/TLS"),
	"POST /api/nodes/{id}/geo-refresh": set("Нода · обновление geo"),
	"POST /api/nodes/{id}/geo-cadence": set("Нода · автообновление geo"),
	"DELETE /api/nodes/{id}":           set("Нода удалена"),
	"POST /api/nodes/{id}/enabled":     set("Нода вкл/выкл"),
	"POST /api/nodes/{id}/regen-join":  set("Нода · новый токен установки"),
	"POST /api/nodes/{id}/update":      set("Нода · обновление"),
	"POST /api/nodes/update-all":       set("Обновление всех нод"),
	"POST /api/nodes/{id}/provision":   set("Нода · установка по SSH"),

	// The panel itself. The backup download is a GET, but it hands over a file
	// containing every secret the panel holds — that is worth a row.
	"GET /api/backup":           act(model.AuditBackupTaken),
	"POST /api/backup/inspect":  skip, // read-only: inspects an uploaded file, changes nothing
	"POST /api/restore":         act(model.AuditRestored),
	"POST /api/reset":           act(model.AuditFactoryReset),
	"POST /api/update":          act(model.AuditUpdated),
	"POST /api/xray/restart":    act(model.AuditXrayRestarted),
	"POST /api/stats/reset":     act(model.AuditStatsReset),
	"POST /api/health/selftest": skip, // a read-only probe: spawns a throwaway client, changes nothing

	// End users: audited in the user journal instead, per user, with details this
	// trail could not carry. Listed explicitly so the exhaustiveness test sees a
	// decision rather than an omission.
	"POST /api/users":                      skip,
	"POST /api/users/bulk":                 skip,
	"DELETE /api/users/{id}":               skip,
	"POST /api/users/{id}/reset":           skip,
	"POST /api/users/{id}/limits":          skip,
	"POST /api/users/{id}/enabled":         skip,
	"POST /api/users/{id}/name":            skip,
	"POST /api/users/{id}/rotate-sub":      skip,
	"POST /api/users/{id}/telegram/unlink": skip,
	"POST /api/users/{id}/telegram/link":   skip,
	"POST /api/users/{id}/reset-period":    skip,
	"POST /api/users/{id}/plan":            skip,

	// Sessions are audited inside their handlers: login has no session to read an
	// actor from, and a FAILED login — the row worth having — never reaches a
	// handler-success path.
	"POST /api/login":  skip,
	"POST /api/logout": skip,
}

// auditDetail is the mutable slot a handler fills in to say WHAT it acted on. The
// middleware puts an empty one on the context before the handler runs and reads it
// back after — so a handler can name its target (an admin login, a key name) without
// the middleware having to guess it from the URL.
type auditDetail struct {
	target  string
	details map[string]any
}

type ctxKeyAudit struct{}

// auditTarget names the thing this request acted on, for the audit row.
func auditTarget(r *http.Request, target string) {
	if d, ok := r.Context().Value(ctxKeyAudit{}).(*auditDetail); ok {
		d.target = target
	}
}

// auditDetails attaches extra non-secret fields to the audit row.
func auditDetails(r *http.Request, kv map[string]any) {
	if d, ok := r.Context().Value(ctxKeyAudit{}).(*auditDetail); ok {
		d.details = kv
	}
}

// auditStatus wraps a ResponseWriter to remember the status code, so only requests
// that actually succeeded get a row. A refused step-up or a validation error is not
// a change to the panel, and logging it as one would make the trail lie.
type auditStatus struct {
	http.ResponseWriter
	code int
}

func (w *auditStatus) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.NewResponseController reach the real writer — the restore upload
// extends its read deadline through it, and the SSE handlers flush.
func (w *auditStatus) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// audited wraps a handler so a successful request lands in the admin trail. Routes
// mapped to "" (and read-only routes that aren't in the map at all) pass through
// untouched.
func (rt *Router) audited(pattern string, h http.HandlerFunc) http.HandlerFunc {
	route, known := auditActions[pattern]
	if route.action == "" {
		if known || !mutatingPattern(pattern) {
			return h // deliberately unaudited, or a plain read
		}
		// A mutating route nobody labelled. Record it under its own pattern rather
		// than silently dropping it — an ugly row beats a missing one, and the
		// exhaustiveness test turns this into a build failure anyway.
		route.action = "panel.unlabelled:" + pattern
	}

	return func(w http.ResponseWriter, r *http.Request) {
		// The section is the default target; a handler that knows something more
		// specific (an admin login, a key name) overrides it via auditTarget.
		slot := &auditDetail{target: route.section}
		sw := &auditStatus{ResponseWriter: w, code: http.StatusOK}
		h(sw, r.WithContext(context.WithValue(r.Context(), ctxKeyAudit{}, slot)))

		if sw.code >= http.StatusBadRequest {
			return // it didn't happen
		}
		a := actor.From(r.Context())
		rt.mgr.AddAdminAudit(model.AdminAudit{
			Action:    route.action,
			Target:    slot.target,
			ActorKind: a.Kind,
			ActorName: a.Name,
			IP:        clientIP(r),
			Details:   detailsOrNil(slot.details),
		})
	}
}

// mutatingPattern reports whether a route pattern changes state, by its method.
func mutatingPattern(pattern string) bool {
	method, _, _ := strings.Cut(pattern, " ")
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

func detailsOrNil(d map[string]any) any {
	if len(d) == 0 {
		return nil
	}
	return d
}
