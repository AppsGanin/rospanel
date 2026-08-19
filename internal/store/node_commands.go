package store

import "database/sql"

// One-shot node commands (self-update, geo refresh). See migration 0054 for why they
// are on disk rather than in a map.

// NodeCommand is one pending command's state.
type NodeCommand struct {
	At   int64
	Sent bool
}

// SetNodeCommand records (or re-records) a command for a node. Re-asking extends the
// deadline; it deliberately does NOT clear `sent`.
//
// Clearing it re-arms a command the node has already been given. That was survivable
// while these lived in a map — a panel restart wiped them, so the fleet always started
// from empty — but the rows outlive a restart now, and RequestAllNodesUpdate re-records
// for every eligible node unconditionally. So "panel self-updates, operator then runs
// update-all" would tell nodes that had already updated to update again, fleet-wide:
// exactly the workflow persisting these was meant to fix. A command whose node HAS come
// back is deleted rather than left at sent=1, so a genuine retry still starts fresh.
func (s *Store) SetNodeCommand(nodeID int64, kind string, at int64) error {
	_, err := s.db.Exec(`
		INSERT INTO node_commands (node_id, kind, at, sent) VALUES (?, ?, ?, 0)
		ON CONFLICT (node_id, kind) DO UPDATE SET at = excluded.at`,
		nodeID, kind, at)
	return err
}

// NodeCommand returns a node's pending command of this kind, or nil.
func (s *Store) NodeCommand(nodeID int64, kind string) (*NodeCommand, error) {
	var c NodeCommand
	var sent int
	err := s.db.QueryRow(
		`SELECT at, sent FROM node_commands WHERE node_id = ? AND kind = ?`,
		nodeID, kind).Scan(&c.At, &sent)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	c.Sent = sent != 0
	return &c, nil
}

// MarkNodeCommandSent records that the command rode a sync response.
func (s *Store) MarkNodeCommandSent(nodeID int64, kind string) error {
	_, err := s.db.Exec(
		`UPDATE node_commands SET sent = 1 WHERE node_id = ? AND kind = ?`, nodeID, kind)
	return err
}

// DeleteNodeCommand clears a command — delivered and confirmed, or aged out.
func (s *Store) DeleteNodeCommand(nodeID int64, kind string) error {
	_, err := s.db.Exec(
		`DELETE FROM node_commands WHERE node_id = ? AND kind = ?`, nodeID, kind)
	return err
}

// PurgeNodeCommands drops commands older than the cutoff. Nodes that never come back
// would otherwise leave their rows behind; the take path only ever sees the ones that do.
func (s *Store) PurgeNodeCommands(before int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM node_commands WHERE at < ?`, before)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
