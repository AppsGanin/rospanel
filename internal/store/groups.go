package store

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
)

// ErrGroupNameTaken is the unique-name index violation, mapped to a user-facing error
// by the manager.
var ErrGroupNameTaken = errors.New("group name already in use")

func isGroupNameConflict(err error) bool {
	return err != nil && strings.Contains(err.Error(), "idx_groups_name")
}

// Groups returns every group with its member count and grant tokens, for the
// management list.
func (s *Store) Groups() ([]model.Group, error) {
	rows, err := s.db.Query(`
		SELECT g.id, g.name, g.created_at,
		       (SELECT COUNT(*) FROM group_members m WHERE m.group_id = g.id)
		FROM groups g ORDER BY lower(g.name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Group{}
	byID := map[int64]int{} // group id → index in out, to attach grants below
	for rows.Next() {
		var g model.Group
		if err := rows.Scan(&g.ID, &g.Name, &g.CreatedAt, &g.Members); err != nil {
			return nil, err
		}
		byID[g.ID] = len(out)
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Grants in one pass, attached to their group.
	grows, err := s.db.Query(`SELECT group_id, token FROM group_grants`)
	if err != nil {
		return nil, err
	}
	defer grows.Close()
	for grows.Next() {
		var gid int64
		var token string
		if err := grows.Scan(&gid, &token); err != nil {
			return nil, err
		}
		if i, ok := byID[gid]; ok {
			out[i].Grants = append(out[i].Grants, token)
		}
	}
	if err := grows.Err(); err != nil {
		return nil, err
	}
	// Member ids in one pass, so the editor can preselect them without a query per group.
	mrows, err := s.db.Query(`SELECT group_id, user_id FROM group_members`)
	if err != nil {
		return nil, err
	}
	defer mrows.Close()
	for mrows.Next() {
		var gid, uid int64
		if err := mrows.Scan(&gid, &uid); err != nil {
			return nil, err
		}
		if i, ok := byID[gid]; ok {
			out[i].MemberIDs = append(out[i].MemberIDs, uid)
		}
	}
	return out, mrows.Err()
}

// GetGroup returns one group with its grants, or nil.
func (s *Store) GetGroup(id int64) (*model.Group, error) {
	var g model.Group
	err := s.db.QueryRow(`SELECT id, name, created_at FROM groups WHERE id = ?`, id).
		Scan(&g.ID, &g.Name, &g.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	grants, err := s.groupGrants(id)
	if err != nil {
		return nil, err
	}
	g.Grants = grants
	mrows, err := s.db.Query(`SELECT user_id FROM group_members WHERE group_id = ?`, id)
	if err != nil {
		return nil, err
	}
	defer mrows.Close()
	for mrows.Next() {
		var uid int64
		if err := mrows.Scan(&uid); err != nil {
			return nil, err
		}
		g.MemberIDs = append(g.MemberIDs, uid)
		g.Members++
	}
	return &g, mrows.Err()
}

// groupGrants returns a group's grant tokens.
func (s *Store) groupGrants(id int64) ([]string, error) {
	rows, err := s.db.Query(`SELECT token FROM group_grants WHERE group_id = ?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// CreateGroup inserts a group and its grants in one transaction.
func (s *Store) CreateGroup(name string, grants []string) (*model.Group, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	res, err := tx.Exec(`INSERT INTO groups (name) VALUES (?)`, name)
	if err != nil {
		if isGroupNameConflict(err) {
			return nil, ErrGroupNameTaken
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	if err := replaceGrantsTx(tx, id, grants); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetGroup(id)
}

// UpdateGroup renames a group and replaces its grants in one transaction.
func (s *Store) UpdateGroup(id int64, name string, grants []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.Exec(`UPDATE groups SET name = ? WHERE id = ?`, name, id); err != nil {
		if isGroupNameConflict(err) {
			return ErrGroupNameTaken
		}
		return err
	}
	if err := replaceGrantsTx(tx, id, grants); err != nil {
		return err
	}
	return tx.Commit()
}

// replaceGrantsTx wipes and re-inserts a group's grants.
func replaceGrantsTx(tx *sql.Tx, groupID int64, grants []string) error {
	if _, err := tx.Exec(`DELETE FROM group_grants WHERE group_id = ?`, groupID); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, t := range grants {
		if t = strings.TrimSpace(t); t == "" || seen[t] {
			continue
		}
		seen[t] = true
		if _, err := tx.Exec(`INSERT INTO group_grants (group_id, token) VALUES (?, ?)`, groupID, t); err != nil {
			return err
		}
	}
	return nil
}

// DeleteGroup removes a group; membership and grants cascade.
func (s *Store) DeleteGroup(id int64) error {
	_, err := s.db.Exec(`DELETE FROM groups WHERE id = ?`, id)
	return err
}

// SetGroupMembers replaces the set of users in a group — the group-side twin of
// SetUserGroups.
func (s *Store) SetGroupMembers(groupID int64, userIDs []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	viaPlan, err := planOwnedIn(tx, `SELECT user_id, via_plan FROM group_members WHERE group_id = ?`, groupID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM group_members WHERE group_id = ?`, groupID); err != nil {
		return err
	}
	seen := map[int64]bool{}
	for _, uid := range userIDs {
		if seen[uid] {
			continue
		}
		seen[uid] = true
		if _, err := tx.Exec(
			`INSERT INTO group_members (group_id, user_id, via_plan) VALUES (?, ?, ?)`,
			groupID, uid, boolToInt(viaPlan[uid]),
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetUserGroups replaces the set of groups a user belongs to.
func (s *Store) SetUserGroups(userID int64, groupIDs []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	viaPlan, err := planOwnedIn(tx, `SELECT group_id, via_plan FROM group_members WHERE user_id = ?`, userID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM group_members WHERE user_id = ?`, userID); err != nil {
		return err
	}
	seen := map[int64]bool{}
	for _, gid := range groupIDs {
		if seen[gid] {
			continue
		}
		seen[gid] = true
		if _, err := tx.Exec(
			`INSERT INTO group_members (group_id, user_id, via_plan) VALUES (?, ?, ?)`,
			gid, userID, boolToInt(viaPlan[gid]),
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// planOwnedIn reads the via_plan flag of the rows a replace-everything membership edit
// is about to wipe, keyed by the id that varies (the user, or the group). Both editors
// re-insert with the flag they found, so a hand edit that keeps a plan-granted row
// keeps it plan-granted — otherwise saving the user card would quietly convert the
// plan's own grants into manual ones and the next plan switch could not take them back.
func planOwnedIn(tx *sql.Tx, query string, arg int64) (map[int64]bool, error) {
	rows, err := tx.Query(query, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		var via int
		if err := rows.Scan(&id, &via); err != nil {
			return nil, err
		}
		out[id] = via != 0
	}
	return out, rows.Err()
}

// ExistingGroupIDs filters ids down to the groups that still exist, de-duplicated and
// in the caller's order. Its own query rather than a filter over Groups(): the caller
// (saving a tariff plan) needs existence only, and Groups() drags every membership row
// in the install along with it.
func (s *Store) ExistingGroupIDs(ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := make([]any, 0, len(ids))
	seen := map[int64]bool{}
	for _, id := range ids {
		if id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		args = append(args, id)
	}
	if len(args) == 0 {
		return nil, nil
	}
	q := `SELECT id FROM groups WHERE id IN (?` + strings.Repeat(",?", len(args)-1) + `)`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	live := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		live[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]int64, 0, len(args))
	for _, id := range ids {
		if live[id] {
			delete(live, id) // keep the caller's order, drop duplicates
			out = append(out, id)
		}
	}
	return out, nil
}

// GroupsForUser returns the groups a user belongs to (id + name), for the user views.
func (s *Store) GroupsForUser(userID int64) ([]model.GroupRef, error) {
	rows, err := s.db.Query(`
		SELECT g.id, g.name FROM group_members m
		JOIN groups g ON g.id = m.group_id
		WHERE m.user_id = ? ORDER BY lower(g.name)`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.GroupRef{}
	for rows.Next() {
		var g model.GroupRef
		if err := rows.Scan(&g.ID, &g.Name); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// GroupsForAllUsers returns every user's groups keyed by user id, so the user LIST
// can show chips without a query per row.
func (s *Store) GroupsForAllUsers() (map[int64][]model.GroupRef, error) {
	rows, err := s.db.Query(`
		SELECT m.user_id, g.id, g.name FROM group_members m
		JOIN groups g ON g.id = m.group_id
		ORDER BY lower(g.name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]model.GroupRef{}
	for rows.Next() {
		var uid int64
		var g model.GroupRef
		if err := rows.Scan(&uid, &g.ID, &g.Name); err != nil {
			return nil, err
		}
		out[uid] = append(out[uid], g)
	}
	return out, rows.Err()
}

// AccessMap resolves every user's access in one pass: userID → Access. A user with no
// group membership is absent from the result, which model.AccessOf reads as
// unrestricted — so the map only ever lists the users who ARE restricted.
func (s *Store) AccessMap() (map[int64]model.Access, error) {
	rows, err := s.db.Query(`
		SELECT m.user_id, gr.token FROM group_members m
		LEFT JOIN group_grants gr ON gr.group_id = m.group_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]model.Access{}
	for rows.Next() {
		var uid int64
		var token sql.NullString
		if err := rows.Scan(&uid, &token); err != nil {
			return nil, err
		}
		a, ok := out[uid]
		if !ok {
			// The user is in at least one group ⇒ restricted (All stays false), even if
			// that group grants nothing (then the user reaches nothing, which is the
			// operator's choice).
			a = model.Access{Tokens: map[string]bool{}}
		}
		if token.Valid && token.String != "" {
			a.Tokens[token.String] = true
		}
		out[uid] = a
	}
	return out, rows.Err()
}

// UserAccess resolves one user's access — the subscription path, which only needs the
// requesting user. A user in no group is unrestricted.
func (s *Store) UserAccess(userID int64) (model.Access, error) {
	rows, err := s.db.Query(`
		SELECT gr.token FROM group_members m
		LEFT JOIN group_grants gr ON gr.group_id = m.group_id
		WHERE m.user_id = ?`, userID)
	if err != nil {
		return model.Access{}, err
	}
	defer rows.Close()
	tokens := map[string]bool{}
	restricted := false
	for rows.Next() {
		restricted = true // a membership row exists ⇒ restricted
		var token sql.NullString
		if err := rows.Scan(&token); err != nil {
			return model.Access{}, err
		}
		if token.Valid && token.String != "" {
			tokens[token.String] = true
		}
	}
	if err := rows.Err(); err != nil {
		return model.Access{}, err
	}
	if !restricted {
		return model.UnrestrictedAccess(), nil
	}
	return model.Access{Tokens: tokens}, nil
}

// DeleteInboundGrants removes every group grant referencing a custom inbound, called
// when the inbound is deleted so no group keeps a dangling token.
func (s *Store) DeleteInboundGrants(inboundID int64) error {
	_, err := s.db.Exec(`DELETE FROM group_grants WHERE token = ?`, model.InboundToken(inboundID))
	return err
}

// DeleteServerGrants removes every group grant referencing a server (its built-in
// lanes), called when a node is deleted. Its inbounds' grants are removed alongside
// via DeleteInboundGrants as those inbounds are dropped.
func (s *Store) DeleteServerGrants(serverID int64) error {
	_, err := s.db.Exec(
		`DELETE FROM group_grants WHERE token LIKE ?`,
		"builtin:"+strconv.FormatInt(serverID, 10)+":%")
	return err
}
