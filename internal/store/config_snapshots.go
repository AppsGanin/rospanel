package store

import (
	"database/sql"
	"encoding/json"

	"github.com/AppsGanin/rospanel/internal/model"
)

// maxConfigSnapshots caps how many server-config snapshots are kept. Enough to undo a
// run of bad edits, bounded so the history can't grow forever.
const maxConfigSnapshots = 30

// CreateConfigSnapshot stores one server-config snapshot and trims the history to the
// cap. The JSON is encrypted at rest (it carries the REALITY/WARP private keys), like
// the token fields in the settings row. auto marks the ones taken automatically (e.g.
// before a rollback) vs. an operator's manual save-point.
func (s *Store) CreateConfigSnapshot(label string, auto bool, configJSON string) (int64, error) {
	var id int64
	err := s.withTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(
			`INSERT INTO config_snapshots (created_at, label, auto, config_json)
			 VALUES (unixepoch(), ?, ?, ?)`,
			label, boolToInt(auto), encField(configJSON),
		)
		if err != nil {
			return err
		}
		// The id is returned rather than left to the caller to guess from "newest
		// first": created_at is whole seconds, so a concurrent create — or the
		// auto-snapshot a rollback takes — can land between the insert and the list and
		// hand the caller someone else's save-point to roll back to.
		id, _ = res.LastInsertId()
		_, err = tx.Exec(
			`DELETE FROM config_snapshots WHERE id NOT IN (
			   SELECT id FROM config_snapshots ORDER BY created_at DESC, id DESC LIMIT ?
			 )`, maxConfigSnapshots)
		return err
	})
	return id, err
}

// ListConfigSnapshots returns the snapshots newest first, without the payload.
func (s *Store) ListConfigSnapshots() ([]model.ConfigSnapshot, error) {
	rows, err := s.db.Query(
		`SELECT id, created_at, label, auto FROM config_snapshots
		 ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ConfigSnapshot
	for rows.Next() {
		var sn model.ConfigSnapshot
		var auto int
		if err := rows.Scan(&sn.ID, &sn.CreatedAt, &sn.Label, &auto); err != nil {
			return nil, err
		}
		sn.Auto = auto != 0
		out = append(out, sn)
	}
	return out, rows.Err()
}

// ConfigSnapshot returns one snapshot's decoded payload, or an error if the id is
// unknown or the stored JSON is corrupt.
func (s *Store) ConfigSnapshot(id int64) (*model.ServerConfigSnapshot, error) {
	var raw string
	if err := s.db.QueryRow(
		`SELECT config_json FROM config_snapshots WHERE id = ?`, id).Scan(&raw); err != nil {
		return nil, err
	}
	var cfg model.ServerConfigSnapshot
	if err := json.Unmarshal([]byte(decField(raw)), &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// DeleteConfigSnapshot removes one snapshot.
func (s *Store) DeleteConfigSnapshot(id int64) error {
	_, err := s.db.Exec(`DELETE FROM config_snapshots WHERE id = ?`, id)
	return err
}

// RestoreServerConfig writes a snapshot back onto the master in ONE transaction: the
// settings singleton (everything EXCEPT the certificate/domain identity — see
// ServerConfigSnapshot) plus the server's custom inbounds. Doing it atomically means a
// crash or a validation failure can never leave settings restored with inbounds half
// wiped, and a concurrent reconcile never observes an empty inbound set mid-restore.
//
// Inbounds are recreated with their ORIGINAL ids so group grants — which reference a
// custom inbound by the opaque token inbound:<id> — keep resolving; recreating them with
// fresh autoincrement ids would silently strip access from every grouped user of that
// lane. Grants for inbounds that existed before the rollback but aren't in the snapshot
// are swept, matching what DeleteInbound does, so no dangling tokens are left behind.
//
// The two secret columns are re-encrypted on the way in, matching how GetSettings
// decrypts them on the way out.
func (s *Store) RestoreServerConfig(c *model.ServerConfigSnapshot) error {
	routingJSON, err := json.Marshal(c.Routing)
	if err != nil {
		return err
	}
	return s.withTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE settings SET
			vless_enabled = ?, hysteria_enabled = ?, reality_enabled = ?,
			vless_port = ?, hysteria_port = ?, hop_start = ?, hop_end = ?, hop_interval = ?,
			hysteria_obfs = ?,
			reality_port = ?, reality_dest = ?, reality_private_key = ?, reality_public_key = ?,
			reality_short_id = ?, reality_path = ?, reality_max_time_diff = ?,
			vless_fp = ?, reality_fp = ?,
			vless_name = ?, reality_name = ?, hysteria_name = ?,
			tls_fragment = ?, tls_min13 = ?, block_quic = ?,
			routing_config = ?,
			warp_enabled = ?, warp_private_key = ?, warp_public_key = ?, warp_endpoint = ?,
			warp_address_v4 = ?, warp_address_v6 = ?, warp_reserved = ?,
			opera_enabled = ?, opera_country = ?, opera_port = ?,
			xray_dns = ?, decoy_template = ?,
			updated_at = unixepoch()
			WHERE id = 1`,
			boolToInt(c.VLESSEnabled), boolToInt(c.HysteriaEnabled), boolToInt(c.RealityEnabled),
			c.VLESSPort, c.HysteriaPort, c.HopStart, c.HopEnd, c.HopInterval,
			c.HysteriaObfs,
			c.RealityPort, c.RealityDest, encField(c.RealityPrivateKey), c.RealityPublicKey,
			c.RealityShortID, c.RealityPath, c.RealityMaxTimeDiff,
			c.VLESSFp, c.RealityFp,
			c.VLESSName, c.RealityName, c.HysteriaName,
			boolToInt(c.TLSFragment), boolToInt(c.TLSMin13), boolToInt(c.BlockQUIC),
			string(routingJSON),
			boolToInt(c.WarpEnabled), encField(c.WarpPrivateKey), c.WarpPublicKey, c.WarpEndpoint,
			c.WarpAddressV4, c.WarpAddressV6, c.WarpReserved,
			boolToInt(c.OperaEnabled), c.OperaCountry, c.OperaPort,
			c.XrayDNS, c.DecoyTemplate,
		); err != nil {
			return err
		}

		// The inbound ids present before the restore — any not brought back by the
		// snapshot loses its group grants below.
		rows, err := tx.Query(`SELECT id FROM inbounds WHERE server_id = ?`, model.LocalNodeID)
		if err != nil {
			return err
		}
		var existing []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			existing = append(existing, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()

		if _, err := tx.Exec(`DELETE FROM inbounds WHERE server_id = ?`, model.LocalNodeID); err != nil {
			return err
		}

		kept := make(map[int64]bool, len(c.Inbounds))
		for _, in := range c.Inbounds {
			opts, err := marshalInboundOpts(in.Opts)
			if err != nil {
				return err
			}
			// Preserve the original id (nil → autoincrement only for the degenerate
			// id-0 case that captured inbounds never actually hit).
			var idArg any
			if in.ID != 0 {
				idArg = in.ID
				kept[in.ID] = true
			}
			if _, err := tx.Exec(`
				INSERT INTO inbounds (id, server_id, enabled, sort, name, protocol, port, opts, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, COALESCE(NULLIF(?, 0), unixepoch()))`,
				idArg, model.LocalNodeID, boolToInt(in.Enabled), in.Sort, in.Name,
				in.Protocol, in.Port, opts, in.CreatedAt); err != nil {
				return mapInboundConflict(err)
			}
		}

		for _, id := range existing {
			if !kept[id] {
				if _, err := tx.Exec(
					`DELETE FROM group_grants WHERE token = ?`, model.InboundToken(id)); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
