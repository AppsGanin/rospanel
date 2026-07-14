package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/auth"
	"github.com/AppsGanin/rospanel/internal/model"
)

// ErrNodeNameTaken is returned when a node write violates the live-node name unique
// index (the app-level NodeNameTaken check lost a race). The manager maps it to a
// user-facing validation error.
var ErrNodeNameTaken = errors.New("node name already in use")

// isNameConflict reports whether err is the unique-name index violation (0035).
func isNameConflict(err error) bool {
	return err != nil && strings.Contains(err.Error(), "idx_nodes_name_live")
}

// nodeTokenPrefix marks a raw node bearer/join token, so a leaked one is
// recognizably a RosPanel node credential (and greppable in logs).
const nodeTokenPrefix = "rpn_"

// joinTokenTTL is how long a freshly-issued install command stays valid.
const joinTokenTTL = 24 * time.Hour

// defaultGeoRefreshHours is the geo auto-refresh cadence a new node starts with
// (weekly). Set explicitly on insert so it holds even on installs whose nodes table
// predates the weekly default.
const defaultGeoRefreshHours = 168

// nodeColumns is the SELECT list every node read shares, in Node-field order.
const nodeColumns = `id, name, host, enabled,
	reality_private_key, reality_public_key, reality_short_id, reality_service_name, reality_dest,
	vless_enabled, trojan_enabled, hysteria_enabled, reality_enabled,
	decoy_template, routing_config, xray_dns,
	warp_enabled, warp_private_key, warp_public_key, warp_endpoint,
	warp_address_v4, warp_address_v6, warp_reserved, opera_enabled, opera_country,
	connections_config,
	last_seen, node_version, xray_version, xray_running,
	cert_sha256, cert_self_signed, config_hash, last_report_id, created_at,
	join_expires_at, deleted_at,
	cert_issuer, cert_expires_at, geo_refresh_hours,
	acme_email, acme_provider, zerossl_eab_kid, zerossl_eab_hmac`

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
	var enabled, xrayRunning, certSelfSigned, warpEn, operaEn int
	var vlessEn, trojanEn, hysteriaEn, realityEn sql.NullBool
	var routingJSON, connectionsJSON string
	var xrayDNS sql.NullString
	if err := sc.Scan(
		&n.ID, &n.Name, &n.Host, &enabled,
		&n.RealityPrivateKey, &n.RealityPublicKey, &n.RealityShortID, &n.RealityServiceName, &n.RealityDest,
		&vlessEn, &trojanEn, &hysteriaEn, &realityEn,
		&n.DecoyTemplate, &routingJSON, &xrayDNS,
		&warpEn, &n.WarpPrivateKey, &n.WarpPublicKey, &n.WarpEndpoint,
		&n.WarpAddressV4, &n.WarpAddressV6, &n.WarpReserved, &operaEn, &n.OperaCountry,
		&connectionsJSON,
		&n.LastSeen, &n.NodeVersion, &n.XrayVersion, &xrayRunning,
		&n.CertSHA256, &certSelfSigned, &n.ConfigHash, &n.LastReportID, &n.CreatedAt,
		&n.JoinExpiresAt, &n.DeletedAt,
		&n.CertIssuer, &n.CertExpiresAt, &n.GeoRefreshHours,
		&n.ACMEEmail, &n.ACMEProvider, &n.ZeroSSLEABKID, &n.ZeroSSLEABHMAC,
	); err != nil {
		return nil, err
	}
	n.Enabled = enabled != 0
	n.XrayRunning = xrayRunning != 0
	n.CertSelfSigned = certSelfSigned != 0
	n.WarpEnabled = warpEn != 0
	n.OperaEnabled = operaEn != 0
	n.WarpPrivateKey = decField(n.WarpPrivateKey)
	n.RealityPrivateKey = decField(n.RealityPrivateKey)
	n.ZeroSSLEABHMAC = decField(n.ZeroSSLEABHMAC)
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
	if connectionsJSON != "" {
		var c model.NodeConnections
		if err := json.Unmarshal([]byte(connectionsJSON), &c); err == nil {
			n.Connections = &c
		}
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
			decoy_template, join_token_hash, join_expires_at, created_at, geo_refresh_hours)
		VALUES (?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		name, host, encField(priv), pub, shortID, serviceName,
		decoyTemplate, joinHash, exp, now.Unix(), defaultGeoRefreshHours,
	)
	if err != nil {
		if isNameConflict(err) {
			return nil, ErrNodeNameTaken
		}
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
	// Single-use, atomically: the UPDATE re-asserts the exact join-token hash it read,
	// so a concurrent second consume (or a RegenJoinToken that rotated the token in
	// between) affects zero rows and is treated as "not consumed" — no double-mint and
	// no clobbering a freshly-regenerated token.
	res, err := s.db.Exec(
		`UPDATE nodes SET token_hash = ?, join_token_hash = '', join_expires_at = 0
			WHERE id = ? AND join_token_hash = ? AND join_token_hash != ''`,
		permHash, id, hash,
	)
	if err != nil {
		return nil, "", err
	}
	if aff, _ := res.RowsAffected(); aff == 0 {
		return nil, "", nil // lost the race / token rotated — not consumed
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
	return s.issueJoinToken(id, true)
}

// IssueJoinToken issues a fresh one-time join token WITHOUT revoking the node's
// current permanent token. Used for SSH re-provisioning: if the install fails the
// live node keeps working on its old token, and a successful re-join replaces the
// token via ConsumeJoinToken. The raw join token is returned once.
func (s *Store) IssueJoinToken(id int64) (string, error) {
	return s.issueJoinToken(id, false)
}

func (s *Store) issueJoinToken(id int64, revoke bool) (string, error) {
	joinToken, err := generateNodeToken()
	if err != nil {
		return "", err
	}
	joinHash, err := s.tokenHash(joinToken)
	if err != nil {
		return "", err
	}
	exp := time.Now().Add(joinTokenTTL).Unix()
	// revoke=true also kills the current permanent token + resets status (manual
	// regen); revoke=false leaves them so a failed re-provision doesn't down the node.
	q := `UPDATE nodes SET join_token_hash = ?, join_expires_at = ? WHERE id = ?`
	if revoke {
		q = `UPDATE nodes SET join_token_hash = ?, join_expires_at = ?, token_hash = '',
			config_hash = '', last_seen = 0 WHERE id = ?`
	}
	res, err := s.db.Exec(q, joinHash, exp, id)
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
	// Per-node egress toggles (independent of the master). WARP keys are provisioned
	// separately (SaveNodeWarp) when WarpEnabled flips on.
	WarpEnabled  bool
	OperaEnabled bool
	OperaCountry string
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
			routing_config = ?, xray_dns = ?,
			warp_enabled = ?, opera_enabled = ?, opera_country = ?
		WHERE id = ?`,
		e.Name, e.Host, e.DecoyTemplate,
		boolToNull(e.VLESS), boolToNull(e.Trojan), boolToNull(e.Hysteria), boolToNull(e.Reality),
		routingJSON, dns,
		boolToInt(e.WarpEnabled), boolToInt(e.OperaEnabled), e.OperaCountry,
		id,
	)
	if isNameConflict(err) {
		return ErrNodeNameTaken
	}
	return err
}

// SaveNodeWarp stores a node's WARP registration (WireGuard identity). The private
// key is encrypted at rest.
func (s *Store) SaveNodeWarp(id int64, priv, pub, endpoint, v4, v6, reserved string) error {
	_, err := s.db.Exec(`
		UPDATE nodes SET warp_private_key = ?, warp_public_key = ?, warp_endpoint = ?,
			warp_address_v4 = ?, warp_address_v6 = ?, warp_reserved = ? WHERE id = ?`,
		encField(priv), pub, endpoint, v4, v6, reserved, id,
	)
	return err
}

// SetNodeDNS persists a node's own DNS override, touching only the xray_dns column
// (nil ⇒ inherit the panel's). Kept separate from UpdateNode so the DNS tab saves
// without rewriting routing/egress.
func (s *Store) SetNodeDNS(id int64, dns *string) error {
	var v sql.NullString
	if dns != nil {
		v = sql.NullString{String: *dns, Valid: true}
	}
	_, err := s.db.Exec(`UPDATE nodes SET xray_dns = ? WHERE id = ?`, v, id)
	return err
}

// SetNodeRealityDest persists a node's own REALITY masquerade donor (empty ⇒ inherit
// the panel's).
func (s *Store) SetNodeRealityDest(id int64, dest string) error {
	_, err := s.db.Exec(`UPDATE nodes SET reality_dest = ? WHERE id = ?`, dest, id)
	return err
}

// SaveNodeReality replaces a node's REALITY keypair/shortId/service (regeneration).
// The private key is encrypted at rest.
func (s *Store) SaveNodeReality(id int64, priv, pub, shortID, service string) error {
	_, err := s.db.Exec(`
		UPDATE nodes SET reality_private_key = ?, reality_public_key = ?,
			reality_short_id = ?, reality_service_name = ? WHERE id = ?`,
		encField(priv), pub, shortID, service, id,
	)
	return err
}

// SetNodeProtocols sets a node's protocol on/off flags explicitly (the node's own —
// no inheritance).
func (s *Store) SetNodeProtocols(id int64, vless, trojan, hysteria, reality bool) error {
	_, err := s.db.Exec(`
		UPDATE nodes SET vless_enabled = ?, trojan_enabled = ?, hysteria_enabled = ?,
			reality_enabled = ? WHERE id = ?`,
		boolToInt(vless), boolToInt(trojan), boolToInt(hysteria), boolToInt(reality), id,
	)
	return err
}

// SetNodeConnections persists a node's own transport config (JSON blob). A nil config
// clears it (the node reverts to inheriting the master's transport).
func (s *Store) SetNodeConnections(id int64, c *model.NodeConnections) error {
	blob := ""
	if c != nil {
		b, err := json.Marshal(c)
		if err != nil {
			return err
		}
		blob = string(b)
	}
	_, err := s.db.Exec(`UPDATE nodes SET connections_config = ? WHERE id = ?`, blob, id)
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
			cert_sha256 = ?, cert_self_signed = ?, cert_issuer = ?, cert_expires_at = ?,
			config_hash = ? WHERE id = ?`,
		st.LastSeen, st.NodeVersion, st.XrayVersion, boolToInt(st.XrayRunning),
		st.CertSHA256, boolToInt(st.CertSelfSigned), st.CertIssuer, st.CertExpiresAt,
		st.ConfigHash, id,
	)
	return err
}

// SetNodeACME persists a node's own ACME config: host (target), e-mail, provider and
// ZeroSSL EAB. The HMAC is encrypted at rest.
func (s *Store) SetNodeACME(id int64, host, email, provider, eabKID, eabHMAC string) error {
	_, err := s.db.Exec(`
		UPDATE nodes SET host = ?, acme_email = ?, acme_provider = ?,
			zerossl_eab_kid = ?, zerossl_eab_hmac = ? WHERE id = ?`,
		host, email, provider, eabKID, encField(eabHMAC), id,
	)
	return err
}

// SetNodeGeoRefresh persists a node's own geo auto-refresh cadence (hours; 0 ⇒ never).
func (s *Store) SetNodeGeoRefresh(id int64, hours int) error {
	if hours < 0 {
		hours = 0
	}
	_, err := s.db.Exec(`UPDATE nodes SET geo_refresh_hours = ? WHERE id = ?`, hours, id)
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
