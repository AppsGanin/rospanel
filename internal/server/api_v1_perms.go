package server

import (
	"context"
	"net/http"

	"github.com/AppsGanin/rospanel/internal/model"
)

// What an API key must hold to reach each /v1 route — the same permissions the
// panel's routes need (see panelMux), so a key given a role can do over the API
// exactly what an admin with that role can do in the panel. A key without a role
// holds every permission (model.APIKey.Perms).
//
// Exhaustive by construction: apiMux refuses to register a route that has no entry
// here, so an endpoint added later cannot ship open to every key by omission. An
// empty list means "any valid key" and is kept to what the panel shows everyone.
var apiRoutePerms = map[string][]string{
	// Served without a key at all (see apiHandler); listed so the MCP tool list,
	// which is built from the same document, keeps them.
	"GET /v1/healthz":      {},
	"GET /v1/openapi.json": {},
	"GET /v1/docs":         {},

	"GET /v1/health":        {},
	"GET /v1/health/report": {model.PermServersView},
	"GET /v1/system":        {model.PermStatsView, model.PermUsersView, model.PermServersView},
	"GET /v1/summary":       {model.PermStatsView, model.PermUsersView},
	"GET /v1/metrics":       {model.PermStatsView},

	"GET /v1/users":                       {model.PermUsersView},
	"POST /v1/users":                      {model.PermUsersManage},
	"POST /v1/users/bulk":                 {model.PermUsersManage, model.PermUsersDelete}, // per action (bulkActionPerm)
	"GET /v1/users/{id}":                  {model.PermUsersView},
	"PATCH /v1/users/{id}":                {model.PermUsersManage},
	"DELETE /v1/users/{id}":               {model.PermUsersDelete},
	"POST /v1/users/{id}/reset":           {model.PermUsersManage},
	"POST /v1/users/{id}/reset-period":    {model.PermUsersManage},
	"POST /v1/users/{id}/rotate-sub":      {model.PermUsersManage},
	"POST /v1/users/{id}/plan":            {model.PermUsersManage},
	"POST /v1/users/{id}/plan/cancel":     {model.PermUsersManage},
	"GET /v1/users/{id}/connections":      {model.PermUsersView},
	"GET /v1/users/{id}/devices":          {model.PermUsersView},
	"POST /v1/users/{id}/devices/unbind":  {model.PermUsersManage},
	"GET /v1/users/{id}/events":           {model.PermUsersView},
	"GET /v1/users/{id}/abuse":            {model.PermUsersView},
	"GET /v1/users/{id}/happ-link":        {model.PermUsersView},
	"POST /v1/users/{id}/groups":          {model.PermUsersManage},
	"GET /v1/events":                      {model.PermUsersView},
	"GET /v1/events/catalog":              {model.PermUsersView},
	"GET /v1/registrations":               {model.PermUsersView},
	"POST /v1/registrations/{id}/approve": {model.PermUsersManage},
	"POST /v1/registrations/{id}/reject":  {model.PermUsersManage},
	"GET /v1/groups":                      {model.PermGroupsView, model.PermUsersView, model.PermBillingView},
	"GET /v1/groups/targets":              {model.PermGroupsView},
	"POST /v1/groups":                     {model.PermGroupsManage},
	"POST /v1/groups/{id}":                {model.PermGroupsManage},
	"DELETE /v1/groups/{id}":              {model.PermGroupsManage},
	"POST /v1/groups/{id}/members":        {model.PermGroupsManage},

	"GET /v1/billing/providers":            {model.PermBillingView},
	"GET /v1/billing/plans":                {model.PermBillingView, model.PermUsersView},
	"POST /v1/billing/plans":               {model.PermBillingManage},
	"DELETE /v1/billing/plans/{id}":        {model.PermBillingManage},
	"POST /v1/billing/plans/{id}/migrate":  {model.PermBillingManage},
	"GET /v1/billing/orders":               {model.PermBillingView},
	"POST /v1/billing/orders":              {model.PermBillingManage},
	"GET /v1/billing/orders/{id}":          {model.PermBillingView},
	"POST /v1/billing/orders/{id}/confirm": {model.PermBillingManage},
	"POST /v1/billing/orders/{id}/cancel":  {model.PermBillingManage},
	"GET /v1/billing/stats":                {model.PermBillingView},
	"GET /v1/billing/settings":             {model.PermBillingView},
	"POST /v1/billing/settings":            {model.PermBillingManage},
	"GET /v1/payments":                     {model.PermPayments},
	"POST /v1/payments":                    {model.PermPayments},

	"GET /v1/stats/series":       {model.PermStatsView, model.PermUsersView},
	"GET /v1/stats/nodes":        {model.PermStatsView, model.PermUsersView},
	"GET /v1/stats/nodes/series": {model.PermStatsView},
	"GET /v1/stats/users":        {model.PermStatsView},
	"GET /v1/stats/abuse":        {model.PermStatsView},
	"GET /v1/stats/countries":    {model.PermStatsView},
	"GET /v1/stats/asns":         {model.PermStatsView},

	"GET /v1/admin-audit":         {model.PermAudit},
	"GET /v1/admin-audit/catalog": {model.PermAudit},
	// A backup is the whole database: the owner's, and a full-access key's (which
	// only the owner can mint).
	"GET /v1/backup":      {model.PermOwner},
	"GET /v1/backup/info": {model.PermOwner},

	"GET /v1/nodes":      {model.PermServersView, model.PermRoutingView},
	"POST /v1/nodes":     {model.PermServersManage},
	"GET /v1/nodes/{id}": {model.PermServersView, model.PermRoutingView},
	// Its fields span two sections; see nodeFieldPerms.
	"PATCH /v1/nodes/{id}":                    {model.PermServersManage, model.PermRoutingManage},
	"DELETE /v1/nodes/{id}":                   {model.PermServersManage},
	"POST /v1/nodes/{id}/enabled":             {model.PermServersManage},
	"POST /v1/nodes/{id}/regen-join":          {model.PermServersManage},
	"POST /v1/nodes/{id}/update":              {model.PermUpdate},
	"POST /v1/nodes/update-all":               {model.PermUpdate},
	"POST /v1/nodes/{id}/proxy":               {model.PermRoutingManage},
	"POST /v1/nodes/{id}/placement":           {model.PermServersManage},
	"GET /v1/nodes/{id}/health":               {model.PermServersView},
	"GET /v1/nodes/{id}/logs":                 {model.PermLogs},
	"GET /v1/inbounds/catalog":                {model.PermServersView},
	"GET /v1/servers/{id}/inbounds":           {model.PermServersView},
	"POST /v1/servers/{id}/inbounds":          {model.PermServersManage},
	"POST /v1/inbounds/{id}":                  {model.PermServersManage},
	"DELETE /v1/inbounds/{id}":                {model.PermServersManage},
	"GET /v1/servers/{id}/routing":            {model.PermRoutingView},
	"POST /v1/servers/{id}/routing":           {model.PermRoutingManage},
	"POST /v1/servers/{id}/xray-restart":      {model.PermServersManage},
	"GET /v1/config/snapshots":                {model.PermServersView},
	"POST /v1/config/snapshots":               {model.PermServersManage},
	"POST /v1/config/snapshots/{id}/rollback": {model.PermServersManage},
	"DELETE /v1/config/snapshots/{id}":        {model.PermServersManage},

	// The fields of PATCH /v1/settings span several sections of the panel: the route
	// opens to any of their permissions, and apiPatchSettings asks for each field's
	// own (see settingsFieldPerms).
	// The same readers as the panel's GET /api/settings: each section's view reads it.
	"GET /v1/settings": {model.PermSettingsView, model.PermServersView, model.PermRoutingView, model.PermSecurityView},
	"PATCH /v1/settings": {model.PermSettingsManage, model.PermRoutingManage,
		model.PermSecurityManage, model.PermServersManage, model.PermOwner},

	"GET /v1/webhooks":            {model.PermWebhooks},
	"GET /v1/webhooks/events":     {model.PermWebhooks},
	"POST /v1/webhooks":           {model.PermWebhooks},
	"POST /v1/webhooks/{id}":      {model.PermWebhooks},
	"DELETE /v1/webhooks/{id}":    {model.PermWebhooks},
	"POST /v1/webhooks/{id}/test": {model.PermWebhooks},
}

// ctxKeyAPIPerms carries what the calling API key may do, set by apiAuth.
type ctxKeyAPIPerms struct{}

func withAPIPerms(ctx context.Context, p model.PermSet) context.Context {
	return context.WithValue(ctx, ctxKeyAPIPerms{}, p)
}

// apiPerms is what the calling key holds — nothing if apiAuth did not run.
func apiPerms(r *http.Request) model.PermSet {
	if p, ok := r.Context().Value(ctxKeyAPIPerms{}).(model.PermSet); ok {
		return p
	}
	return model.PermSet{}
}

// apiGate wraps one /v1 route in its permission check. A key without the permission
// gets 403 in the API's own envelope.
func apiGate(perms []string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !apiPerms(r).Any(perms...) {
			writeAPIErr(w, http.StatusForbidden, "forbidden", "this API key's role does not allow this operation")
			return
		}
		h(w, r)
	}
}

// apiRouteAllowed reports whether a key holding p may call the /v1 route pattern —
// how the MCP endpoint leaves out the tools a key could only get a 403 from.
func apiRouteAllowed(p model.PermSet, pattern string) bool {
	perms, ok := apiRoutePerms[pattern]
	return ok && p.Any(perms...)
}

// fieldPerm is one field of a partial update that the panel keeps under a
// permission of its own: set says whether the request carries it.
type fieldPerm struct {
	set   bool
	field string
	perm  string
}

// apiFieldsAllowed refuses a partial update carrying a field the key's role may not
// change, naming the field — the route is open to several permissions, each field is
// not.
func apiFieldsAllowed(w http.ResponseWriter, r *http.Request, fields []fieldPerm) bool {
	for _, f := range fields {
		if f.set && !apiPerms(r).Has(f.perm) {
			writeAPIErr(w, http.StatusForbidden, "forbidden",
				"this API key's role does not allow changing "+f.field)
			return false
		}
	}
	return true
}

// bulkActionPerm is what one bulk user action needs: deleting is users.delete, the
// rest users.manage — the same split the single-user routes have.
func bulkActionPerm(action string) string {
	if action == "delete" {
		return model.PermUsersDelete
	}
	return model.PermUsersManage
}
