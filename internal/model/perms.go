package model

import (
	"slices"
	"strings"
)

// Permissions are what a role is made of. Each one names a slice of the panel an
// admin (or an API key) may reach; a route declares the permission it needs, and the
// owner holds every one of them without it being written down anywhere.
//
// Most of the panel splits into sections, each with a "view" and a "manage"
// permission — manage always carries view with it (see PermImplies), because a
// form you may save but not read is not a thing anybody means. What does not fit that
// shape — deleting users, the export with every credential, backups, the factory
// reset — is a permission of its own, so it can be withheld from a role that
// otherwise manages the section.
const (
	PermUsersView      = "users.view"
	PermUsersManage    = "users.manage"
	PermUsersDelete    = "users.delete"
	PermUsersExport    = "users.export"
	PermGroupsView     = "groups.view"
	PermGroupsManage   = "groups.manage"
	PermStatsView      = "stats.view"
	PermStatsManage    = "stats.manage" // resetting the statistics
	PermBillingView    = "billing.view"
	PermBillingManage  = "billing.manage"
	PermPayments       = "payments.manage"
	PermBroadcasts     = "broadcasts.manage"
	PermServersView    = "servers.view"
	PermServersManage  = "servers.manage"
	PermRoutingView    = "routing.view"
	PermRoutingManage  = "routing.manage"
	PermSettingsView   = "settings.view"
	PermSettingsManage = "settings.manage"
	PermSecurityView   = "security.view"
	PermSecurityManage = "security.manage"
	PermWebhooks       = "webhooks.manage"
	PermAPI            = "api.manage"
	PermLogs           = "logs.view"
	PermAudit          = "audit.view"
	PermUpdate         = "system.update"

	// PermOwner is not a permission anyone is granted: it marks what only the owner
	// (and a full-access API key, which only the owner can mint) may do — backups and
	// restore, which carry the whole database and the admin roster with it. It is in
	// no catalog row, so NormalizePerms strips it from any stored role.
	PermOwner = "owner"
)

// PermSection is one row of the role editor: a section with a view and/or a manage
// permission, or a single permission of its own (View empty, Manage set).
type PermSection struct {
	Key    string `json:"key"`
	View   string `json:"view,omitempty"`
	Manage string `json:"manage,omitempty"`
}

// PermCatalog is every permission, in the order the role editor lists them. It is
// the single source of truth: a permission not in here does not exist, is dropped
// from a stored role on read, and cannot be granted.
var PermCatalog = []PermSection{
	// Sections with both columns first, then what is only read, then what is only
	// done — the editor reads as a table, not as a list of exceptions.
	{Key: "users", View: PermUsersView, Manage: PermUsersManage},
	{Key: "groups", View: PermGroupsView, Manage: PermGroupsManage},
	{Key: "billing", View: PermBillingView, Manage: PermBillingManage},
	{Key: "servers", View: PermServersView, Manage: PermServersManage},
	{Key: "routing", View: PermRoutingView, Manage: PermRoutingManage},
	{Key: "settings", View: PermSettingsView, Manage: PermSettingsManage},
	{Key: "security", View: PermSecurityView, Manage: PermSecurityManage},
	{Key: "stats", View: PermStatsView, Manage: PermStatsManage},
	{Key: "logs", View: PermLogs},
	{Key: "audit", View: PermAudit},
	{Key: "usersDelete", Manage: PermUsersDelete},
	{Key: "usersExport", Manage: PermUsersExport},
	{Key: "payments", Manage: PermPayments},
	{Key: "broadcasts", Manage: PermBroadcasts},
	{Key: "webhooks", Manage: PermWebhooks},
	{Key: "api", Manage: PermAPI},
	{Key: "update", Manage: PermUpdate},
}

// allPerms is the catalog flattened and sorted, built once: permission checks run on
// every authenticated request, and rebuilding it per check was a slice and a sort
// per permission.
var allPerms = func() []string {
	var out []string
	for _, s := range PermCatalog {
		if s.View != "" {
			out = append(out, s.View)
		}
		if s.Manage != "" {
			out = append(out, s.Manage)
		}
	}
	slices.Sort(out)
	return out
}()

var knownPerms = func() map[string]bool {
	m := map[string]bool{}
	for _, p := range allPerms {
		m[p] = true
	}
	return m
}()

// AllPerms lists every permission in the catalog, sorted.
func AllPerms() []string { return slices.Clone(allPerms) }

// PermImplies lists what holding a permission brings with it. Manage brings its
// view. Webhooks bring more, because what they control reaches further than their
// own section: a webhook delivers user and payment events to whatever address it is
// given. Written into the role rather than checked beside it, so the role editor
// shows what a role can really reach instead of promising less.
//
// Some things are no permission at all, because they reach the owner's own power:
// the Telegram bots (the admin bot's chat gets every admin's sign-in alerts and can
// end any admin's sessions), backups (the whole database, the owner's password hash
// and second factor with it) and restore (which replaces the admin roster). Those are
// the owner's — see PermOwner and panelMux.
var PermImplies = func() map[string][]string {
	m := map[string][]string{}
	for _, s := range PermCatalog {
		if s.View != "" && s.Manage != "" {
			m[s.Manage] = []string{s.View}
		}
	}
	m[PermWebhooks] = []string{PermUsersView, PermBillingView}
	// These act on a section they need to see to act from: the users to delete or
	// export, the plans the providers take payment for.
	m[PermUsersDelete] = []string{PermUsersView}
	m[PermUsersExport] = []string{PermUsersView}
	m[PermPayments] = []string{PermBillingView}
	return m
}()

// NormalizePerms returns the permission set in its one stored shape: unknown
// entries dropped, everything a permission implies added (transitively), no
// duplicates, sorted. Dropping rather than refusing an unknown permission is what
// lets a database written by a newer build still load in an older one: the role
// loses what this build cannot enforce, it does not gain anything.
func NormalizePerms(perms []string) []string {
	set := map[string]bool{}
	var add func(p string)
	add = func(p string) {
		if !knownPerms[p] || set[p] {
			return
		}
		set[p] = true
		for _, q := range PermImplies[p] {
			add(q)
		}
	}
	for _, p := range perms {
		add(strings.TrimSpace(p))
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}

// PermSet is a resolved set of permissions — what one request is allowed to do.
type PermSet map[string]bool

// NewPermSet builds a set from a list, normalised.
func NewPermSet(perms []string) PermSet {
	s := PermSet{}
	for _, p := range NormalizePerms(perms) {
		s[p] = true
	}
	return s
}

// FullPermSet is every permission in the catalog.
func FullPermSet() PermSet {
	s := make(PermSet, len(allPerms)+1)
	for _, p := range allPerms {
		s[p] = true
	}
	return s
}

// OwnerPermSet is what the owner and a full-access API key hold: every permission,
// and PermOwner besides.
func OwnerPermSet() PermSet {
	s := FullPermSet()
	s[PermOwner] = true
	return s
}

// Has reports whether the set holds p.
func (s PermSet) Has(p string) bool { return s[p] }

// Any reports whether the set holds at least one of ps. An empty ps is "no
// permission needed" and is true.
func (s PermSet) Any(ps ...string) bool {
	if len(ps) == 0 {
		return true
	}
	for _, p := range ps {
		if s[p] {
			return true
		}
	}
	return false
}

// Covers reports whether s holds every permission in other — the check that stops
// anyone handing out more than they have themselves (an API key minted with a role
// broader than its creator's).
func (s PermSet) Covers(other PermSet) bool {
	for p := range other {
		if !s[p] {
			return false
		}
	}
	return true
}

// List returns the set as a sorted list.
func (s PermSet) List() []string {
	out := make([]string, 0, len(s))
	for p := range s {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}

// JoinPerms and SplitPerms are the stored form: a comma-separated, normalised list.
func JoinPerms(perms []string) string { return strings.Join(NormalizePerms(perms), ",") }

func SplitPerms(stored string) []string {
	if stored == "" {
		return nil
	}
	return NormalizePerms(strings.Split(stored, ","))
}

// PresetAdminPerms is the built-in "administrator": every permission but the admin
// trail. (The roster, the roles, the bots, backups and restore are the owner's and
// no permission at all.)
func PresetAdminPerms() []string {
	var out []string
	for _, p := range AllPerms() {
		if p != PermAudit {
			out = append(out, p)
		}
	}
	return out
}

// PresetOperatorPerms is what the built-in "operator" role held: end users, their
// groups, the statistics and the journal — no settings, servers, backups or API.
func PresetOperatorPerms() []string {
	return NormalizePerms([]string{
		PermUsersManage, PermUsersDelete, PermGroupsManage, PermStatsView,
	})
}
