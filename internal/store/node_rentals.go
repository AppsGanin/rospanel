package store

import (
	"database/sql"
	"errors"
	"time"

	"github.com/Shu1t3/rospanel-shu1t3/internal/auth"
	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
)

// ErrRentedNodeCannotShare is returned when trying to share a node that is itself rented.
var ErrRentedNodeCannotShare = errors.New("rented node cannot be shared")

// ErrTenantNotFound is returned when a requested tenant cannot be found.
var ErrTenantNotFound = errors.New("tenant not found")

// SetNodeRentalSettings updates the sharing parameters for a node owned by this panel.
func (s *Store) SetNodeRentalSettings(nodeID int64, st model.NodeRentalSettings) error {
	node, err := s.GetNode(nodeID)
	if err != nil {
		return err
	}
	if node == nil {
		return sql.ErrNoRows
	}
	if node.IsRented {
		return ErrRentedNodeCannotShare
	}
	token := node.ShareToken
	if st.ShareEnabled && token == "" {
		newToken, terr := auth.RandomToken()
		if terr != nil {
			return terr
		}
		token = "rpn_share_" + newToken
	}

	_, err = s.db.Exec(`
		UPDATE nodes SET share_enabled = ?, share_quota_percent = ?,
			share_speed_limit = ?, share_token = ?
		WHERE id = ? AND deleted_at = 0`,
		boolToInt(st.ShareEnabled), st.ShareQuotaPercent, st.ShareSpeedLimit, token, nodeID,
	)
	return err
}

// GetNodeRentalSettings returns the current sharing parameters and generated token for a node.
func (s *Store) GetNodeRentalSettings(nodeID int64) (*model.NodeRentalSettings, error) {
	var shareEn, quota, speed int
	var token string
	err := s.db.QueryRow(`
		SELECT share_enabled, share_quota_percent, share_speed_limit, share_token
		FROM nodes WHERE id = ? AND deleted_at = 0`, nodeID).Scan(&shareEn, &quota, &speed, &token)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if quota <= 0 {
		quota = 100
	}
	return &model.NodeRentalSettings{
		ShareEnabled:      shareEn != 0,
		ShareQuotaPercent: quota,
		ShareSpeedLimit:   speed,
		ShareToken:        token,
		MaxTenants:        10,
	}, nil
}

// RegisterNodeTenant registers or updates a tenant on the owner node.
func (s *Store) RegisterNodeTenant(nodeID int64, t model.NodeTenant) error {
	now := time.Now().Unix()
	_, err := s.db.Exec(`
		INSERT INTO node_tenants (node_id, tenant_id, name, speed_limit, last_seen, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(node_id, tenant_id) DO UPDATE SET
			name = CASE WHEN excluded.name != '' THEN excluded.name ELSE node_tenants.name END,
			speed_limit = excluded.speed_limit,
			last_seen = excluded.last_seen`,
		nodeID, t.TenantID, t.Name, t.SpeedLimit, now, now,
	)
	return err
}

// ListNodeTenants returns all tenants associated with a shared node.
func (s *Store) ListNodeTenants(nodeID int64) ([]model.NodeTenant, error) {
	rows, err := s.db.Query(`
		SELECT id, node_id, tenant_id, name, traffic_up, traffic_down, speed_limit, last_seen, created_at
		FROM node_tenants WHERE node_id = ? ORDER BY id ASC`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.NodeTenant
	for rows.Next() {
		var t model.NodeTenant
		if err := rows.Scan(
			&t.ID, &t.NodeID, &t.TenantID, &t.Name,
			&t.TrafficUp, &t.TrafficDown, &t.SpeedLimit, &t.LastSeen, &t.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteNodeTenant removes a tenant record and deletes any inbounds associated with this tenant.
func (s *Store) DeleteNodeTenant(nodeID int64, tenantID string) error {
	return s.withTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`DELETE FROM node_tenants WHERE node_id = ? AND tenant_id = ?`, nodeID, tenantID)
		if err != nil {
			return err
		}
		if aff, _ := res.RowsAffected(); aff == 0 {
			return ErrTenantNotFound
		}
		_, err = tx.Exec(`DELETE FROM inbounds WHERE server_id = ? AND tenant_id = ?`, nodeID, tenantID)
		return err
	})
}

// UpdateTenantTraffic records traffic transferred by a tenant on a node.
func (s *Store) UpdateTenantTraffic(nodeID int64, tenantID string, up, down int64) error {
	now := time.Now().Unix()
	_, err := s.db.Exec(`
		UPDATE node_tenants SET traffic_up = traffic_up + ?, traffic_down = traffic_down + ?, last_seen = ?
		WHERE node_id = ? AND tenant_id = ?`,
		up, down, now, nodeID, tenantID,
	)
	return err
}

// CreateRentedNode inserts a node marked as is_rented=1 on the tenant's panel.
func (s *Store) CreateRentedNode(name, host string, ownerNodeID int64, shareKey, tenantID string, quotaPercent, speedLimit int) (*model.Node, error) {
	now := time.Now()
	res, err := s.db.Exec(`
		INSERT INTO nodes (name, host, enabled, is_rented, rent_owner_node_id,
			rent_share_key, rent_tenant_id, share_quota_percent, share_speed_limit,
			created_at, geo_refresh_hours)
		VALUES (?, ?, 1, 1, ?, ?, ?, ?, ?, ?, ?)`,
		name, host, ownerNodeID, shareKey, tenantID, quotaPercent, speedLimit, now.Unix(), defaultGeoRefreshHours,
	)
	if err != nil {
		if isNameConflict(err) {
			return nil, ErrNodeNameTaken
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &model.Node{
		ID:                id,
		Name:              name,
		Host:              host,
		Enabled:           true,
		IsRented:          true,
		RentOwnerNodeID:   ownerNodeID,
		RentShareKey:      shareKey,
		RentTenantID:      tenantID,
		ShareQuotaPercent: quotaPercent,
		ShareSpeedLimit:   speedLimit,
		CreatedAt:         now.Unix(),
	}, nil
}

// DeleteTenantInbounds purges all custom inbounds created by a specific tenant on a server.
func (s *Store) DeleteTenantInbounds(serverID int64, tenantID string) error {
	_, err := s.db.Exec(`DELETE FROM inbounds WHERE server_id = ? AND tenant_id = ?`, serverID, tenantID)
	return err
}

// DeleteRentedNode hard-deletes a rented node on the tenant panel and cleans up all its inbounds.
func (s *Store) DeleteRentedNode(id int64) error {
	return s.withTx(func(tx *sql.Tx) error {
		// Clean up any group grants for inbounds belonging to this rented node
		_, _ = tx.Exec(`DELETE FROM group_members WHERE group_id IN (SELECT id FROM groups WHERE target_type = 'inbound' AND target_id IN (SELECT id FROM inbounds WHERE server_id = ?))`, id)
		// Clean up inbounds
		_, _ = tx.Exec(`DELETE FROM inbounds WHERE server_id = ?`, id)
		// Clean up server level group grants
		_, _ = tx.Exec(`DELETE FROM group_members WHERE group_id IN (SELECT id FROM groups WHERE target_type = 'server' AND target_id = ?)`, id)
		// Delete the rented node record
		_, err := tx.Exec(`DELETE FROM nodes WHERE id = ? AND is_rented = 1`, id)
		return err
	})
}
