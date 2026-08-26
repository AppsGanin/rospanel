package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
)

// ErrInboundPortTaken / ErrInboundNameTaken are the unique-index violations from
// migration 0035 — the manager's own pre-write checks losing a race with a
// concurrent save. The manager maps them to the same user-facing message the
// pre-write check would have produced.
var (
	ErrInboundPortTaken = errors.New("inbound port already in use on this server")
	ErrInboundNameTaken = errors.New("inbound name already in use on this server")
)

// mapInboundConflict turns a unique-index violation into the matching sentinel.
func mapInboundConflict(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case strings.Contains(err.Error(), "idx_inbounds_live_port"):
		return ErrInboundPortTaken
	case strings.Contains(err.Error(), "idx_inbounds_name"):
		return ErrInboundNameTaken
	}
	return err
}

// inboundColumns is the SELECT list every inbound read shares, in Inbound-field order.
const inboundColumns = `id, server_id, enabled, sort, name, protocol, port, opts, created_at`

// scanInbound reads one inbound row in inboundColumns order, decoding the opts blob
// and decrypting the REALITY private key it may carry.
func scanInbound(sc interface{ Scan(...any) error }) (*model.Inbound, error) {
	var in model.Inbound
	var enabled int
	var optsJSON string
	if err := sc.Scan(
		&in.ID, &in.ServerID, &enabled, &in.Sort, &in.Name, &in.Protocol, &in.Port,
		&optsJSON, &in.CreatedAt,
	); err != nil {
		return nil, err
	}
	in.Enabled = enabled != 0
	if optsJSON != "" {
		if err := json.Unmarshal([]byte(optsJSON), &in.Opts); err != nil {
			return nil, fmt.Errorf("inbound %d: decode opts: %w", in.ID, err)
		}
	}
	in.Opts.RealityPrivateKey = decField(in.Opts.RealityPrivateKey)
	return &in, nil
}

// marshalInboundOpts encodes the opts blob for storage, encrypting the REALITY
// private key at rest exactly as the settings/node rows do with theirs.
func marshalInboundOpts(o model.InboundOpts) (string, error) {
	o.RealityPrivateKey = encField(o.RealityPrivateKey)
	b, err := json.Marshal(o)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Inbounds returns one server's custom inbounds in display order (LocalNodeID for
// the master).
func (s *Store) Inbounds(serverID int64) ([]model.Inbound, error) {
	rows, err := s.db.Query(
		`SELECT `+inboundColumns+` FROM inbounds WHERE server_id = ? ORDER BY sort, id`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Inbound{}
	for rows.Next() {
		in, err := scanInbound(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *in)
	}
	return out, rows.Err()
}

// EnabledInbounds returns one server's enabled custom inbounds, in display order.
// This is what the config generator and the link builders consume.
func (s *Store) EnabledInbounds(serverID int64) ([]model.Inbound, error) {
	all, err := s.Inbounds(serverID)
	if err != nil {
		return nil, err
	}
	out := make([]model.Inbound, 0, len(all))
	for _, in := range all {
		if in.Enabled {
			out = append(out, in)
		}
	}
	return out, nil
}

// AllInbounds returns every server's inbounds keyed by server id. Used by the
// subscription builders, which walk the whole fleet in one pass and would otherwise
// issue a query per node.
func (s *Store) AllInbounds() (map[int64][]model.Inbound, error) {
	rows, err := s.db.Query(`SELECT ` + inboundColumns + ` FROM inbounds ORDER BY server_id, sort, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]model.Inbound{}
	for rows.Next() {
		in, err := scanInbound(rows)
		if err != nil {
			return nil, err
		}
		out[in.ServerID] = append(out[in.ServerID], *in)
	}
	return out, rows.Err()
}

// GetInbound reads one inbound by id, or nil when it doesn't exist.
func (s *Store) GetInbound(id int64) (*model.Inbound, error) {
	in, err := scanInbound(s.db.QueryRow(
		`SELECT `+inboundColumns+` FROM inbounds WHERE id = ?`, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return in, nil
}

// CreateInbound inserts a new inbound and returns it with its assigned id. Sort
// defaults to the end of the server's list so a new entry lands last.
func (s *Store) CreateInbound(in model.Inbound) (*model.Inbound, error) {
	opts, err := marshalInboundOpts(in.Opts)
	if err != nil {
		return nil, err
	}
	if in.Sort == 0 {
		var maxSort int
		if err := s.db.QueryRow(
			`SELECT COALESCE(MAX(sort), 0) FROM inbounds WHERE server_id = ?`, in.ServerID,
		).Scan(&maxSort); err != nil {
			return nil, err
		}
		in.Sort = maxSort + 1
	}
	res, err := s.db.Exec(`
		INSERT INTO inbounds (server_id, enabled, sort, name, protocol, port, opts)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		in.ServerID, boolToInt(in.Enabled), in.Sort, in.Name, in.Protocol, in.Port, opts)
	if err != nil {
		return nil, mapInboundConflict(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetInbound(id)
}

// UpdateInbound overwrites an inbound's editable fields. server_id is deliberately
// not among them: moving an inbound between servers would carry a port that is free
// on one box onto another where it may not be, so the UI deletes and re-creates.
func (s *Store) UpdateInbound(in model.Inbound) error {
	opts, err := marshalInboundOpts(in.Opts)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		UPDATE inbounds SET enabled = ?, sort = ?, name = ?, protocol = ?, port = ?, opts = ?
		WHERE id = ?`,
		boolToInt(in.Enabled), in.Sort, in.Name, in.Protocol, in.Port, opts, in.ID)
	return mapInboundConflict(err)
}

// DeleteInbound removes an inbound. Its users lose that lane on the next reconcile.
func (s *Store) DeleteInbound(id int64) error {
	_, err := s.db.Exec(`DELETE FROM inbounds WHERE id = ?`, id)
	return err
}

// DeleteServerInbounds removes every inbound of a server. Called when a node is
// deleted, so its inbounds don't outlive it as orphan rows that the fleet-wide
// readers would still hand out.
func (s *Store) DeleteServerInbounds(serverID int64) error {
	_, err := s.db.Exec(`DELETE FROM inbounds WHERE server_id = ?`, serverID)
	return err
}
