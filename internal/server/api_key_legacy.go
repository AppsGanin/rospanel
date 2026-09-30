package server

import (
	"slices"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// retiredKeyPerms are permissions that once opened API methods and no longer exist:
// a key's methods now say what it may do, and the panel never checked them.
var retiredKeyPerms = map[string][]string{
	"users.signup": {"POST /v1/signup"},
	"billing.sell": {
		"POST /v1/billing/orders", "POST /v1/users/{id}/autorenew",
		"POST /v1/users/{id}/promo", "POST /v1/users/{id}/referrer",
	},
}

// ConvertLegacyAPIKeys holds every active key still on permissions alone to the API
// methods those permissions open — retired ones included, so a bot's key made with
// billing.sell goes on opening orders. Run at startup; a converted key is not seen
// again. Returns how many keys it converted.
func ConvertLegacyAPIKeys(st *store.Store) (int, error) {
	keys, err := st.PermsOnlyAPIKeys()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, k := range keys {
		a := apiAccess{perms: model.NewPermSet(k.Perms)}
		var routes []string
		for _, r := range tickableRoutes() {
			if apiMayCall(a, r.pattern) {
				routes = append(routes, r.pattern)
			}
		}
		for _, p := range k.Perms {
			for _, r := range retiredKeyPerms[p] {
				if !slices.Contains(routes, r) {
					routes = append(routes, r)
				}
			}
		}
		if len(routes) == 0 {
			continue
		}
		if err := st.SetAPIKeyPerms(k.ID, false, k.Perms, routes); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
