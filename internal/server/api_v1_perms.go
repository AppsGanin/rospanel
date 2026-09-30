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

	"GET /v1/health":              {},
	"GET /v1/health/report":       {model.PermServersView},
	"GET /v1/system":              {model.PermStatsView, model.PermUsersView, model.PermServersView},
	"GET /v1/system/auto-update":  {model.PermUpdate},
	"POST /v1/system/auto-update": {model.PermUpdate},
	"GET /v1/summary":             {model.PermStatsView, model.PermUsersView},
	"GET /v1/metrics":             {model.PermStatsView},

	"GET /v1/users":                       {model.PermUsersView},
	"POST /v1/users":                      {model.PermUsersManage},
	"POST /v1/signup":                     {model.PermUsersManage},
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
	"GET /v1/billing/promos":               {model.PermBillingView},
	"POST /v1/billing/promos":              {model.PermBillingManage},
	"DELETE /v1/billing/promos/{id}":       {model.PermBillingManage},
	"GET /v1/users/{id}/wallet":            {model.PermBillingView},
	"GET /v1/users/{id}/referrals":         {model.PermBillingView},
	"POST /v1/users/{id}/autorenew":        {model.PermBillingManage},
	"POST /v1/users/{id}/promo":            {model.PermBillingManage},
	"GET /v1/users/{id}/quotes":            {model.PermBillingView},
	"GET /v1/users/{id}/extras":            {model.PermBillingView},
	"POST /v1/users/{id}/telegram":         {model.PermUsersManage},
	"GET /v1/users/{id}/subscription":      {model.PermUsersView},
	"POST /v1/users/{id}/referrer":         {model.PermBillingManage},
	"POST /v1/users/{id}/source":           {model.PermUsersManage},
	"GET /v1/billing/promos/{id}/uses":     {model.PermBillingView},
	"GET /v1/billing/referrals":            {model.PermBillingView},
	"GET /v1/billing/funnel":               {model.PermBillingView},
	"POST /v1/billing/orders/{id}/refund":  {model.PermBillingManage},
	"POST /v1/users/{id}/balance":          {model.PermBillingManage},
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

// apiAccess is what the calling API key may do: its permissions, and — for a key
// held to exact methods — the methods. Set by apiAuth.
type apiAccess struct {
	perms  model.PermSet
	routes map[string]bool // nil: not held to methods
}

type ctxKeyAPIAccess struct{}

func withAPIAccess(ctx context.Context, a apiAccess) context.Context {
	return context.WithValue(ctx, ctxKeyAPIAccess{}, a)
}

// withAPIPerms is a key held to its permissions alone.
func withAPIPerms(ctx context.Context, p model.PermSet) context.Context {
	return withAPIAccess(ctx, apiAccess{perms: p})
}

func apiAccessOf(r *http.Request) apiAccess {
	a, _ := r.Context().Value(ctxKeyAPIAccess{}).(apiAccess)
	return a
}

// apiPerms is what the calling key holds — nothing if apiAuth did not run.
func apiPerms(r *http.Request) model.PermSet {
	if p := apiAccessOf(r).perms; p != nil {
		return p
	}
	return model.PermSet{}
}

// apiMayCall reports whether a key may call the /v1 route pattern. A key held to
// methods calls exactly those (they were checked against its creator when ticked); any
// other key, the routes its permissions open. A route open to every key stays open.
func apiMayCall(a apiAccess, pattern string) bool {
	perms, ok := apiRoutePerms[pattern]
	if !ok {
		return false
	}
	if len(perms) == 0 {
		return true
	}
	if a.routes != nil {
		return a.routes[pattern]
	}
	return a.perms.Any(perms...)
}

// apiGate wraps one /v1 route in its permission check. A key that may not call it
// gets 403 in the API's own envelope.
func apiGate(pattern string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !apiMayCall(apiAccessOf(r), pattern) {
			writeAPIErr(w, http.StatusForbidden, "forbidden", "this API key does not allow this operation")
			return
		}
		h(w, r)
	}
}

// keyAccess is a stored key's reach as apiMayCall reads it.
func keyAccess(k model.APIKey) apiAccess {
	a := apiAccess{perms: k.Perms}
	if k.FullAccess {
		a.perms = model.OwnerPermSet()
		return a
	}
	if len(k.Routes) > 0 {
		a.routes = make(map[string]bool, len(k.Routes))
		for _, r := range k.Routes {
			a.routes[r] = true
		}
	}
	return a
}

// keyRoutes is the methods a key may call — what the panel ticks for it: its own list,
// or for a key held to its permissions (full access included) the methods those open.
func keyRoutes(k model.APIKey) []string {
	if len(k.Routes) > 0 {
		return k.Routes
	}
	a := keyAccess(k)
	out := []string{}
	for _, r := range tickableRoutes() {
		if apiMayCall(a, r.pattern) {
			out = append(out, r.pattern)
		}
	}
	return out
}

// apiRoutesPerField are the methods that check each field (or bulk action) against
// its own permission inside: a key ticked for one gets every permission the route
// names that its creator holds, so "change settings" is every setting they may
// change. Any other method gets the first permission it names that the creator holds.
var apiRoutesPerField = map[string]bool{
	"PATCH /v1/settings":   true,
	"PATCH /v1/nodes/{id}": true,
	"POST /v1/users/bulk":  true,
}

// keyGrantForRoutes checks the methods ticked for a key against the caller and
// derives the permissions they carry. Only a method the caller may call themselves
// can be ticked.
func keyGrantForRoutes(routes []string, caller model.PermSet) ([]string, error) {
	if len(routes) == 0 {
		return nil, model.FieldErr("err.keyPermsRequired", "отметьте хотя бы один метод API")
	}
	tickable := map[string]bool{}
	for _, r := range tickableRoutes() {
		tickable[r.pattern] = true
	}
	var perms []string
	for _, r := range routes {
		if !tickable[r] {
			return nil, model.FieldErr("err.keyRouteUnknown", "нет такого метода API: {{value}}", map[string]any{"value": r})
		}
		if !apiMayCall(apiAccess{perms: caller}, r) {
			return nil, model.FieldErr("err.keyRoleTooBroad", "нельзя выдать ключу больше прав, чем у вас самих")
		}
		for _, p := range apiRoutePerms[r] {
			if !caller.Has(p) {
				continue
			}
			perms = append(perms, p)
			if !apiRoutesPerField[r] {
				break
			}
		}
	}
	return perms, nil
}

// keyCoveredBy reports whether the caller could have issued this key: every method it
// may call, and every permission it holds. Otherwise holding the API permission would
// let a narrow admin change or cut off a key the owner made — one allowed backups,
// which no permission names.
func keyCoveredBy(k model.APIKey, caller model.PermSet) bool {
	if k.FullAccess {
		return caller.Covers(model.OwnerPermSet())
	}
	for _, r := range keyRoutes(k) {
		if !apiMayCall(apiAccess{perms: caller}, r) {
			return false
		}
	}
	return caller.Covers(k.Perms)
}

// tickableRoute is one method a key can be given, in the published spec's order.
type tickableRoute struct {
	pattern, method, path, tag string
}

// tickableRoutes is every method a key may be ticked for: all but the ones open to
// any key (or to none).
func tickableRoutes() []tickableRoute {
	var out []tickableRoute
	for _, r := range apiSpecRoutes() {
		pattern := r.method + " " + r.path
		if perms, ok := apiRoutePerms[pattern]; ok && len(perms) > 0 {
			out = append(out, tickableRoute{pattern: pattern, method: r.method, path: r.path, tag: r.tag})
		}
	}
	return out
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
