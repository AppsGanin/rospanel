package store

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/auth"
	"github.com/AppsGanin/rospanel/internal/model"
)

// nodeTokenPrefix marks a raw node bearer/join token, so a leaked one is
// recognizably a RosPanel node credential (and greppable in logs).
const nodeTokenPrefix = "rpn_"

// joinTokenTTL is how long a freshly-issued install command stays valid.
const joinTokenTTL = 24 * time.Hour

// nodeColumns is the SELECT list every node read shares, in Node-field order.
const nodeColumns = `id, name, host, enabled,
	reality_private_key, reality_public_key, reality_short_id, reality_service_name,
	vless_enabled, trojan_enabled, hysteria_enabled, reality_enabled,
	decoy_template, routing_config, xray_dns,
	last_seen, node_version, xray_version, xray_running,
	cert_sha256, cert_self_signed, config_hash, last_report_id, created_at,
	join_expires_at, deleted_at`

// generateNodeToken mints a raw token ("rpn_<43 url-safe chars>", 256 bits).
func generateNodeToken() (string, error) {
	body, err := auth.RandomToken()
	if err != nil {
		return "", err
	}
	return nodeTokenPrefix + body, nil
}

// scanNode reads one node row in nodeColumns order, decrypting the private key
// and mapping the nullable protocol overrides to *bool.
func scanNode(sc interface{ Scan(...any) error }) (*model.Node, error) {
	var n model.Node
	var enabled, xrayRunning, certSelfSigned int
	var vlessEn, trojanEn, hysteriaEn, realityEn sql.NullBool
	var routingJSON string
	var xrayDNS sql.NullString
	if err := sc.Scan(
		&n.ID, &n.Name, &n.Host, &enabled,
		&n.RealityPrivateKey, &n.RealityPublicKey, &n.RealityShortID, &n.RealityServiceName,
		&vlessEn, &trojanEn, &hysteriaEn, &realityEn,
		&n.DecoyTemplate, &routingJSON, &xrayDNS,
		&n.LastSeen, &n.NodeVersion, &n.XrayVersion, &xrayRunning,
		&n.CertSHA256, &certSelfSigned, &n.ConfigHash, &n.LastReportID, &n.CreatedAt,
		&n.JoinExpiresAt, &n.DeletedAt,
	); err != nil {
		return nil, err
	}
	n.Enabled = enabled != 0
	n.XrayRunning = xrayRunning != 0
	n.CertSelfSigned = certSelfSigned != 0
	n.RealityPrivateKey = decField(n.RealityPrivateKey)
	n.VLESSEnabled = nullBoolPtr(vlessEn)
	n.TrojanEnabled = nullBoolPtr(trojanEn)
	n.HysteriaEnabled = nullBoolPtr(hysteriaEn)
	n.RealityEnabled = nullBoolPtr(realityEn)
	if routingJSON != "" {
		var rc model.RoutingConfig
		if err := json.Unmarshal([]byte(routingJSON), &rc); err == nil {
			n.Routing = &rc
		}
	}
	if xrayDNS.Valid {
		v := xrayDNS.String
		n.XrayDNS = &v
	}
	return &n, nil
}

func nullBoolPtr(v sql.NullBool) *bool {
	if !v.Valid {
		return nil
	}
	b := v.Bool
	return &b
}

func boolToNull(p *bool) sql.NullBool {
	if p == nil {
		return sql.NullBool{}
	}
	return sql.NullBool{Bool: *p, Valid: true}
}

// CreateNode inserts a node with a fresh REALITY identity, a random decoy, and a
// one-time join token. The returned Node has RawJoinToken populated (shown to the
// operator exactly once, inside the install command); only its hash is stored.
func (s *Store) CreateNode(name, host, decoyTemplate string) (*model.Node, error) {
	priv, pub, err := auth.GenerateRealityKeys()
	if err != nil {
		return nil, err
	}
	shortID, err := auth.RandomShortIDs()
	if err != nil {
		return nil, err
	}
	serviceName, err := auth.RandomServiceName()
	if err != nil {
		return nil, err
	}
	joinToken, err := generateNodeToken()
	if err != nil {
		return nil, err
	}
	joinHash, err := s.tokenHash(joinToken)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	exp := now.Add(joinTokenTTL).Unix()
	res, err := s.db.Exec(`
		INSERT INTO nodes (name, host, enabled,
			reality_private_key, reality_public_key, reality_short_id, reality_service_name,
			decoy_template, join_token_hash, join_expires_at, created_at)
		VALUES (?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?)`,
		name, host, encField(priv), pub, shortID, serviceName,
		decoyTemplate, joinHash, exp, now.Unix(),
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &model.Node{
		ID:                 id,
		Name:               name,
		Host:               host,
		Enabled:            true,
		RealityPrivateKey:  priv,
		RealityPublicKey:   pub,
		RealityShortID:     shortID,
		RealityServiceName: serviceName,
		DecoyTemplate:      decoyTemplate,
		CreatedAt:          now.Unix(),
		JoinExpiresAt:      exp,
		RawJoinToken:       joinToken,
	}, nil
}

// NodeNameTaken reports whether a live node other than excludeID already uses the
// given name (case-insensitively). Node names must be unique because they become
// Clash proxy names / sing-box outbound tags in multi-node subscriptions, which a
// client rejects if duplicated.
func (s *Store) NodeNameTaken(name string, excludeID int64) (bool, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM nodes WHERE deleted_at = 0 AND id != ? AND lower(name) = lower(?)`,
		excludeID, strings.TrimSpace(name),
	).Scan(&n)
	return n > 0, err
}

// ListNodes returns all live (non-tombstoned) nodes, oldest first. RawJoinToken is
// never populated here.
func (s *Store) ListNodes() ([]model.Node, error) {
	rows, err := s.db.Query(`SELECT ` + nodeColumns + ` FROM nodes WHERE deleted_at = 0 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

// GetNode returns one live node by id, or (nil, nil) if it doesn't exist or was
// deleted. (A tombstoned node is invisible to the operator; only the token lookup
// still finds it, so its next sync can be answered Revoked.)
func (s *Store) GetNode(id int64) (*model.Node, error) {
	n, err := scanNode(s.db.QueryRow(
		`SELECT `+nodeColumns+` FROM nodes WHERE id = ? AND deleted_at = 0`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return n, err
}

// LookupNodeByToken resolves a raw permanent token to its (enabled or disabled)
// node, by HMAC hash. Returns (nil, nil) when nothing matches. The caller decides
// what a disabled node means (it is told to stop serving, not deauthenticated).
func (s *Store) LookupNodeByToken(raw string) (*model.Node, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, nodeTokenPrefix) {
		return nil, nil
	}
	hash, err := s.tokenHash(raw)
	if err != nil {
		return nil, err
	}
	n, err := scanNode(s.db.QueryRow(
		`SELECT `+nodeColumns+` FROM nodes WHERE token_hash = ? AND token_hash != ''`, hash))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return n, err
}

// ConsumeJoinToken exchanges a valid, unexpired one-time join token for a fresh
// permanent bearer token: the join token is cleared (single use) and the permanent
// token's hash is stored. The raw permanent token is returned exactly once.
// Returns (nil, "", nil) when the token is unknown or expired.
func (s *Store) ConsumeJoinToken(raw string) (*model.Node, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, nodeTokenPrefix) {
		return nil, "", nil
	}
	hash, err := s.tokenHash(raw)
	if err != nil {
		return nil, "", err
	}
	var id, exp int64
	err = s.db.QueryRow(
		`SELECT id, join_expires_at FROM nodes WHERE join_token_hash = ? AND join_token_hash != ''`,
		hash,
	).Scan(&id, &exp)
	if err == sql.ErrNoRows {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	if exp != 0 && time.Now().Unix() > exp {
		return nil, "", nil // expired; leave the row for a fresh regen
	}
	perm, err := generateNodeToken()
	if err != nil {
		return nil, "", err
	}
	permHash, err := s.tokenHash(perm)
	if err != nil {
		return nil, "", err
	}
	// Single-use: clear the join token as we set the permanent one.
	if _, err := s.db.Exec(
		`UPDATE nodes SET token_hash = ?, join_token_hash = '', join_expires_at = 0 WHERE id = ?`,
		permHash, id,
	); err != nil {
		return nil, "", err
	}
	n, err := s.GetNode(id)
	if err != nil {
		return nil, "", err
	}
	return n, perm, nil
}

// RegenJoinToken issues a new one-time join token for a node (e.g. to re-install
// it) and revokes its current permanent token so the old install can't keep
// syncing. The raw join token is returned once.
func (s *Store) RegenJoinToken(id int64) (string, error) {
	joinToken, err := generateNodeToken()
	if err != nil {
		return "", err
	}
	joinHash, err := s.tokenHash(joinToken)
	if err != nil {
		return "", err
	}
	exp := time.Now().Add(joinTokenTTL).Unix()
	res, err := s.db.Exec(
		`UPDATE nodes SET join_token_hash = ?, join_expires_at = ?, token_hash = '',
			config_hash = '', last_seen = 0 WHERE id = ?`,
		joinHash, exp, id,
	)
	if err != nil {
		return "", err
	}
	if aff, _ := res.RowsAffected(); aff == 0 {
		return "", sql.ErrNoRows
	}
	return joinToken, nil
}

// NodeEdit carries the operator-editable fields of a node. Pointer fields left nil
// mean "inherit the global setting" for protocol/DNS toggles and routing.
type NodeEdit struct {
	Name          string
	Host          string
	DecoyTemplate string
	VLESS         *bool
	Trojan        *bool
	Hysteria      *bool
	Reality       *bool
	Routing       *model.RoutingConfig // nil ⇒ inherit global routing
	XrayDNS       *string              // nil ⇒ inherit global DNS
}

// UpdateNode persists the operator-editable fields. Identity, tokens and reported
// status are untouched.
func (s *Store) UpdateNode(id int64, e NodeEdit) error {
	routingJSON := ""
	if e.Routing != nil {
		b, err := json.Marshal(e.Routing)
		if err != nil {
			return err
		}
		routingJSON = string(b)
	}
	var dns sql.NullString
	if e.XrayDNS != nil {
		dns = sql.NullString{String: *e.XrayDNS, Valid: true}
	}
	_, err := s.db.Exec(`
		UPDATE nodes SET name = ?, host = ?, decoy_template = ?,
			vless_enabled = ?, trojan_enabled = ?, hysteria_enabled = ?, reality_enabled = ?,
			routing_config = ?, xray_dns = ?
		WHERE id = ?`,
		e.Name, e.Host, e.DecoyTemplate,
		boolToNull(e.VLESS), boolToNull(e.Trojan), boolToNull(e.Hysteria), boolToNull(e.Reality),
		routingJSON, dns,
		id,
	)
	return err
}

// SetNodeEnabled toggles whether a node serves traffic and appears in links.
func (s *Store) SetNodeEnabled(id int64, enabled bool) error {
	_, err := s.db.Exec(`UPDATE nodes SET enabled = ? WHERE id = ?`, boolToInt(enabled), id)
	return err
}

// UpdateNodeStatus records what a node reported on a sync: liveness, versions,
// live cert fingerprint, and the desired-state hash it has applied.
func (s *Store) UpdateNodeStatus(id int64, st model.NodeStatusUpdate) error {
	_, err := s.db.Exec(`
		UPDATE nodes SET last_seen = ?, node_version = ?, xray_version = ?, xray_running = ?,
			cert_sha256 = ?, cert_self_signed = ?, config_hash = ? WHERE id = ?`,
		st.LastSeen, st.NodeVersion, st.XrayVersion, boolToInt(st.XrayRunning),
		st.CertSHA256, boolToInt(st.CertSelfSigned), st.ConfigHash, id,
	)
	return err
}

// ClaimNodeReport atomically advances a node's traffic-ingest watermark to
// reportID, but only if reportID is strictly greater than the stored one. It
// returns true iff this call won the claim — so two concurrent (or replayed)
// syncs carrying the same report can't both count their traffic. The caller
// applies the traffic deltas only when this returns true.
func (s *Store) ClaimNodeReport(id, reportID int64) (bool, error) {
	res, err := s.db.Exec(
		`UPDATE nodes SET last_report_id = ? WHERE id = ? AND last_report_id < ?`,
		reportID, id, reportID,
	)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// DeleteNode soft-deletes a node: it tombstones the row (keeping the token) and
// disables it, so the node's next sync is answered Revoked=true and it stops
// serving — a hard delete would drop the token and leave the node running its last
// config with live user credentials (it would read the resulting decoy response as
// "panel unreachable" and keep serving). PurgeDeletedNodes reclaims the row later.
// Traffic history in traffic_daily is kept (rows carry the numeric node_id).
func (s *Store) DeleteNode(id int64) error {
	_, err := s.db.Exec(
		`UPDATE nodes SET deleted_at = unixepoch(), enabled = 0 WHERE id = ? AND deleted_at = 0`, id)
	return err
}

// PurgeDeletedNodes hard-deletes tombstoned nodes whose deletion is older than the
// cutoff, once a decommissioned node has had ample time to receive its revocation.
func (s *Store) PurgeDeletedNodes(before int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM nodes WHERE deleted_at != 0 AND deleted_at < ?`, before)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
