package store

import "database/sql"

// One-shot node commands (self-update, geo refresh). See migration 0054 for why they
// are on disk rather than in a map.

// NodeCommand is one pending command's state.
type NodeCommand struct {
	At   int64
	Sent bool
}

// setNodeCommandOn is the shared upsert. rearm decides the one clause the two callers
// disagree about — see SetNodeCommand and SetNodeCommands. Kept in one place because
// getting that clause wrong in either direction is a bug the panel has already had.
func setNodeCommandOn(ex execer, nodeID int64, kind string, at int64, rearm bool) error {
	set := "at = excluded.at"
	if rearm {
		set += ", sent = 0"
	}
	_, err := ex.Exec(`
		INSERT INTO node_commands (node_id, kind, at, sent) VALUES (?, ?, ?, 0)
		ON CONFLICT (node_id, kind) DO UPDATE SET `+set,
		nodeID, kind, at)
	return err
}

// SetNodeCommand records a command for ONE node, at an operator's explicit request. It
// re-arms: pressing the button again on a node that was already handed the command sends
// it again.
//
// That is the opposite of what the fleet-wide call does, and the difference is the point.
// Handover is not proof of delivery — a lost sync response is indistinguishable from a
// node that received one — so an operator who presses Update again because nothing
// happened is supplying the only evidence available that it did not land. Swallowing that
// retry leaves them with no way to ask twice. Doing the same thing for the WHOLE fleet is
// what re-armed every node after a panel restart (see SetNodeCommands), so only the
// deliberate, single-node action gets it.
func (s *Store) SetNodeCommand(nodeID int64, kind string, at int64) error {
	return setNodeCommandOn(s.db, nodeID, kind, at, true)
}

// SetNodeCommands records the same command for many nodes in one transaction.
//
// One statement per node under the manager's lock meant a fleet-wide "update all" held
// that lock across N round trips on the single connection, stalling every node's sync
// and the panel's own Nodes page for the duration. Returns how many rows it wrote, which
// is what the caller reports back as its receipt.
func (s *Store) SetNodeCommands(nodeIDs []int64, kind string, at int64) (int, error) {
	if len(nodeIDs) == 0 {
		return 0, nil
	}
	err := s.withTx(func(tx *sql.Tx) error {
		for _, id := range nodeIDs {
			// rearm=false: a fleet-wide re-record must not resend to nodes already
			// handed the command. "Panel self-updates, operator then runs update-all"
			// would otherwise tell every node that had just updated to update again.
			if err := setNodeCommandOn(tx, id, kind, at, false); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	// Every id is written or none is, so the count is the input length — counting rows
	// would only differ by double-counting a duplicate id.
	return len(nodeIDs), nil
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
