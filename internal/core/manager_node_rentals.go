package core

import (
	"bytes"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Shu1t3/rospanel-shu1t3/internal/auth"
	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
	"github.com/Shu1t3/rospanel-shu1t3/internal/nodeapi"
	"github.com/Shu1t3/rospanel-shu1t3/internal/store"
)

// SetNodeHostStats records host stats in memory for a server/node.
func (m *Manager) SetNodeHostStats(id int64, h nodeapi.HostStats) {
	m.nodeGeoMu.Lock()
	defer m.nodeGeoMu.Unlock()
	m.nodeHostStats[id] = h
}

// GetNodeRentalSettings returns rental settings for a node.
func (m *Manager) GetNodeRentalSettings(nodeID int64) (*model.NodeRentalSettings, error) {
	node, err := m.store.GetNode(nodeID)
	if err != nil {
		return nil, err
	}
	if node == nil {
		return nil, invalidCode("err.nodeNotFound", "нода не найдена")
	}
	if node.IsRented {
		return nil, invalidCode("err.rentedNodeCannotBeShared", "арендованную ноду нельзя повторно передавать в аренду")
	}
	settings, err := m.store.GetNodeRentalSettings(nodeID)
	if err != nil {
		return nil, err
	}
	if settings == nil {
		settings = &model.NodeRentalSettings{
			ShareQuotaPercent: 100,
			MaxTenants:        10,
		}
	}
	return settings, nil
}

// UpdateNodeRentalSettings saves sharing configuration for an owner node.
func (m *Manager) UpdateNodeRentalSettings(nodeID int64, s model.NodeRentalSettings) (*model.NodeRentalSettings, error) {
	node, err := m.store.GetNode(nodeID)
	if err != nil {
		return nil, err
	}
	if node == nil {
		return nil, invalidCode("err.nodeNotFound", "нода не найдена")
	}
	if node.IsRented {
		return nil, invalidCode("err.rentedNodeCannotBeShared", "арендованную ноду нельзя повторно передавать в аренду")
	}

	if s.ShareQuotaPercent <= 0 || s.ShareQuotaPercent > 100 {
		s.ShareQuotaPercent = 100
	}
	if s.ShareSpeedLimit < 0 {
		s.ShareSpeedLimit = 0
	}

	if err := m.store.SetNodeRentalSettings(nodeID, s); err != nil {
		return nil, err
	}

	m.nodes.wakeOne(nodeID)
	return m.GetNodeRentalSettings(nodeID)
}

// GenerateNodeShareLink builds and encodes an encrypted share link for an owner node.
func (m *Manager) GenerateNodeShareLink(nodeID int64) (string, error) {
	node, err := m.store.GetNode(nodeID)
	if err != nil {
		return "", err
	}
	if node == nil {
		return "", invalidCode("err.nodeNotFound", "нода не найдена")
	}
	if node.IsRented {
		return "", invalidCode("err.rentedNodeCannotBeShared", "арендованную ноду нельзя повторно передавать в аренду")
	}
	if !node.ShareEnabled {
		return "", invalidCode("err.nodeSharingDisabled", "совместное использование для данной ноды отключено владельцем")
	}

	set, _ := m.store.GetSettings()
	var nodePath string
	if set != nil {
		nodePath = set.NodeAPIPath
	}

	token := node.ShareToken
	if token == "" {
		newToken, terr := auth.RandomToken()
		if terr != nil {
			return "", terr
		}
		token = "rpn_share_" + newToken
		_ = m.store.SetNodeRentalSettings(nodeID, model.NodeRentalSettings{
			ShareEnabled:      true,
			ShareQuotaPercent: node.ShareQuotaPercent,
			ShareSpeedLimit:   node.ShareSpeedLimit,
			ShareToken:        token,
		})
	}

	portsInfo, _ := m.GetNodeReservedPorts(nodeID)
	reserved := make([]int, 0, len(portsInfo))
	for _, p := range portsInfo {
		reserved = append(reserved, p.Port)
	}

	protos := []string{}
	if derefBool(node.VLESSEnabled) {
		protos = append(protos, "vless")
	}
	if derefBool(node.HysteriaEnabled) {
		protos = append(protos, "hysteria2")
	}
	if derefBool(node.RealityEnabled) {
		protos = append(protos, "reality")
	}

	hostStats, _ := m.NodeHostStats(nodeID)

	eff := nodeSettings(set, node)

	payload := model.NodeSharePayload{
		Version:          1,
		NodeID:           node.ID,
		Host:             node.Host,
		NodePath:         nodePath,
		Name:             node.Name,
		ShareToken:       token,
		QuotaPercent:     node.ShareQuotaPercent,
		SpeedLimit:       node.ShareSpeedLimit,
		ReservedPorts:    reserved,
		Protocols:        protos,
		NodeVersion:      node.NodeVersion,
		XrayVersion:      node.XrayVersion,
		CPUPercent:       hostStats.CPUPercent,
		MemUsed:          hostStats.MemUsed,
		MemTotal:         hostStats.MemTotal,
		DiskUsed:         hostStats.DiskUsed,
		DiskTotal:        hostStats.DiskTotal,
		HostUptime:       hostStats.HostUptime,
		RealityPublicKey: eff.RealityPublicKey,
		RealityShortID:   eff.RealityShortID,
		RealityPath:      eff.RealityPath,
		RealityDest:      eff.RealityDest,
		CertSHA256:       node.CertSHA256,
		CertSelfSigned:   node.CertSelfSigned,
		VLESSPort:        eff.VLESSPort,
		RealityPort:      eff.RealityPort,
		HysteriaPort:     eff.HysteriaPort,
		VLESSEnabled:     eff.VLESSEnabled,
		RealityEnabled:   eff.RealityEnabled && eff.RealityPublicKey != "",
		HysteriaEnabled:  eff.HysteriaEnabled,
	}

	return model.EncodeShareLink(payload)
}

// ImportRentedNode parses a share link and attaches the rented node to the local panel.
func (m *Manager) ImportRentedNode(shareLink string, customName string) (*model.Node, error) {
	payload, err := model.DecodeShareLink(shareLink)
	if err != nil {
		return nil, fromFieldErr(err)
	}

	name := strings.TrimSpace(customName)
	if name == "" {
		name = payload.Name
	}
	if name == "" {
		name = payload.Host
	}

	if taken, terr := m.store.NodeNameTaken(name, 0); terr != nil {
		return nil, terr
	} else if taken {
		return nil, invalidCode("err.nodeNameTaken", "нода с таким названием уже есть — имя должно быть уникальным")
	}

	tenantID, err := auth.RandomToken()
	if err != nil {
		return nil, err
	}
	tenantID = "t_" + tenantID[:16]

	node, err := m.store.CreateRentedNode(
		name,
		payload.Host,
		payload.NodeID,
		payload.ShareToken,
		tenantID,
		payload.QuotaPercent,
		payload.SpeedLimit,
		payload.NodeVersion,
		payload.XrayVersion,
		payload.RealityPublicKey,
		payload.RealityShortID,
		payload.RealityPath,
		payload.RealityDest,
		payload.CertSHA256,
		payload.CertSelfSigned,
		payload.VLESSEnabled,
		payload.RealityEnabled,
		payload.HysteriaEnabled,
	)
	if err != nil {
		if errors.Is(err, store.ErrNodeNameTaken) {
			return nil, invalidCode("err.nodeNameTaken", "нода с таким названием уже есть — имя должно быть уникальным")
		}
		return nil, err
	}

	if payload.MemTotal > 0 || payload.CPUPercent > 0 {
		m.SetNodeHostStats(node.ID, nodeapi.HostStats{
			CPUPercent: payload.CPUPercent,
			MemUsed:    payload.MemUsed,
			MemTotal:   payload.MemTotal,
			DiskUsed:   payload.DiskUsed,
			DiskTotal:  payload.DiskTotal,
			HostUptime: payload.HostUptime,
		})
	}

	go m.SyncRentedNode(node.ID)

	return node, nil
}

// ProcessRentalSync handles an incoming telemetry/inbound sync request from a tenant.
func (m *Manager) ProcessRentalSync(req model.NodeRentalSyncReq) (*model.NodeRentalSyncResp, error) {
	node, err := m.store.GetNode(req.NodeID)
	if err != nil {
		return nil, err
	}
	if node == nil {
		return nil, invalidCode("err.nodeNotFound", "нода не найдена")
	}
	if !node.ShareEnabled || node.ShareToken == "" || subtle.ConstantTimeCompare([]byte(node.ShareToken), []byte(req.ShareToken)) != 1 {
		return nil, invalidCode("err.invalidShareToken", "неверный или устаревший токен аренды")
	}

	speedLimit := model.CalculateTenantSpeed(node.ShareSpeedLimit, 1)
	_ = m.store.UpsertNodeTenant(node.ID, req.TenantID, req.TenantName, speedLimit)

	currentInbounds, _ := m.store.Inbounds(node.ID)
	tenantInbounds := make(map[int]bool)
	for _, in := range req.Inbounds {
		tenantInbounds[in.Port] = true
		in.ServerID = node.ID
		in.TenantID = req.TenantID
		_ = m.store.SaveTenantInbound(in)
	}
	for _, in := range currentInbounds {
		if in.TenantID == req.TenantID && !tenantInbounds[in.Port] {
			_ = m.store.DeleteInbound(in.ID)
		}
	}
	if m.nodes != nil {
		m.nodes.wakeOne(node.ID)
	}

	hostStats, _ := m.NodeHostStats(node.ID)
	ports, _ := m.GetNodeReservedPorts(node.ID)
	reserved := make([]int, 0, len(ports))
	for _, p := range ports {
		reserved = append(reserved, p.Port)
	}

	set, _ := m.store.GetSettings()
	eff := nodeSettings(set, node)

	return &model.NodeRentalSyncResp{
		Online:           node.Online(time.Now().Unix()),
		NodeVersion:      node.NodeVersion,
		XrayVersion:      node.XrayVersion,
		XrayRunning:      node.XrayRunning,
		CPUPercent:       hostStats.CPUPercent,
		MemUsed:          hostStats.MemUsed,
		MemTotal:         hostStats.MemTotal,
		DiskUsed:         hostStats.DiskUsed,
		DiskTotal:        hostStats.DiskTotal,
		HostUptime:       hostStats.HostUptime,
		ReservedPorts:    reserved,
		RealityPublicKey: eff.RealityPublicKey,
		RealityShortID:   eff.RealityShortID,
		RealityPath:      eff.RealityPath,
		RealityDest:      eff.RealityDest,
		CertSHA256:       node.CertSHA256,
		CertSelfSigned:   node.CertSelfSigned,
		VLESSPort:        eff.VLESSPort,
		RealityPort:      eff.RealityPort,
		HysteriaPort:     eff.HysteriaPort,
		VLESSEnabled:     eff.VLESSEnabled,
		RealityEnabled:   eff.RealityEnabled && eff.RealityPublicKey != "",
		HysteriaEnabled:  eff.HysteriaEnabled,
	}, nil
}

// SyncRentedNode reaches out to the owner panel to sync inbounds and fetch updated telemetry.
func (m *Manager) SyncRentedNode(nodeID int64) error {
	node, err := m.store.GetNode(nodeID)
	if err != nil || node == nil || !node.IsRented {
		return err
	}
	inbounds, _ := m.store.Inbounds(nodeID)
	reqBody := model.NodeRentalSyncReq{
		NodeID:     node.RentOwnerNodeID,
		ShareToken: node.RentShareKey,
		TenantID:   node.RentTenantID,
		TenantName: node.Name,
		Inbounds:   inbounds,
	}
	data, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	urls := []string{
		fmt.Sprintf("https://%s/api/nodes/rentals/sync", node.Host),
		fmt.Sprintf("http://%s/api/nodes/rentals/sync", node.Host),
	}

	client := &http.Client{
		Timeout: 4 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	var resp *http.Response
	for _, u := range urls {
		r, err := http.NewRequest(http.MethodPost, u, bytes.NewReader(data))
		if err != nil {
			continue
		}
		r.Header.Set("Content-Type", "application/json")
		resp, err = client.Do(r)
		if err == nil && resp.StatusCode == http.StatusOK {
			break
		}
		if resp != nil {
			resp.Body.Close()
			resp = nil
		}
	}

	if resp == nil {
		return errors.New("failed to connect to rented node owner")
	}
	defer resp.Body.Close()

	var syncResp model.NodeRentalSyncResp
	if err := json.NewDecoder(resp.Body).Decode(&syncResp); err != nil {
		return err
	}

	now := time.Now().Unix()
	_ = m.store.UpdateNodeStatus(node.ID, model.NodeStatusUpdate{
		LastSeen:    now,
		NodeVersion: syncResp.NodeVersion,
		XrayVersion: syncResp.XrayVersion,
		XrayRunning: syncResp.XrayRunning,
	})

	_ = m.store.UpdateRentedNodeSecurity(
		node.ID,
		syncResp.RealityPublicKey,
		syncResp.RealityShortID,
		syncResp.RealityPath,
		syncResp.RealityDest,
		syncResp.CertSHA256,
		syncResp.CertSelfSigned,
		syncResp.VLESSEnabled,
		syncResp.RealityEnabled,
		syncResp.HysteriaEnabled,
	)

	m.SetNodeHostStats(node.ID, nodeapi.HostStats{
		CPUPercent: syncResp.CPUPercent,
		MemUsed:    syncResp.MemUsed,
		MemTotal:   syncResp.MemTotal,
		DiskUsed:   syncResp.DiskUsed,
		DiskTotal:  syncResp.DiskTotal,
		HostUptime: syncResp.HostUptime,
	})

	return nil
}

// DeleteRentedNode detaches a rented node locally and wipes any custom inbounds created on it.
func (m *Manager) DeleteRentedNode(id int64) error {
	return m.store.DeleteRentedNode(id)
}

// ListNodeTenants returns all active tenants for a shared node.
func (m *Manager) ListNodeTenants(nodeID int64) ([]model.NodeTenant, error) {
	return m.store.ListNodeTenants(nodeID)
}

// DeleteNodeTenant removes an attached tenant and purges its inbounds.
func (m *Manager) DeleteNodeTenant(nodeID int64, tenantID string) error {
	err := m.store.DeleteNodeTenant(nodeID, tenantID)
	if err != nil {
		if errors.Is(err, store.ErrTenantNotFound) {
			return invalidCode("err.tenantNotFound", "арендатор не найден")
		}
		return err
	}
	m.nodes.wakeOne(nodeID)
	return nil
}

// GetNodeReservedPorts returns a breakdown of all ports currently in use on a server.
func (m *Manager) GetNodeReservedPorts(nodeID int64) ([]model.PortInfo, error) {
	set, err := m.store.GetSettings()
	if err != nil {
		return nil, err
	}

	var ports []model.PortInfo
	if nodeID == model.LocalNodeID {
		if set.VLESSEnabled || set.RealityEnabled {
			ports = append(ports, model.PortInfo{
				Port:     set.RealityPort,
				Protocol: "tcp",
				Service:  "VLESS / REALITY",
				IsOwner:  true,
			})
		}
		if set.HysteriaEnabled {
			ports = append(ports, model.PortInfo{
				Port:     set.HysteriaPort,
				Protocol: "udp",
				Service:  "Hysteria 2",
				IsOwner:  true,
			})
		}
		if set.ProxySocksEnabled && set.ProxySocksPort > 0 {
			ports = append(ports, model.PortInfo{
				Port:     set.ProxySocksPort,
				Protocol: "tcp",
				Service:  "System Proxy (SOCKS5)",
				IsOwner:  true,
			})
		}
		if set.ProxyHTTPEnabled && set.ProxyHTTPPort > 0 {
			ports = append(ports, model.PortInfo{
				Port:     set.ProxyHTTPPort,
				Protocol: "tcp",
				Service:  "System Proxy (HTTP)",
				IsOwner:  true,
			})
		}
	} else {
		node, nerr := m.store.GetNode(nodeID)
		if nerr != nil {
			return nil, nerr
		}
		if node == nil {
			return nil, invalidCode("err.nodeNotFound", "нода не найдена")
		}
		eff := nodeSettings(set, node)
		if derefBool(node.VLESSEnabled) || derefBool(node.RealityEnabled) {
			ports = append(ports, model.PortInfo{
				Port:     eff.RealityPort,
				Protocol: "tcp",
				Service:  "VLESS / REALITY",
				IsOwner:  !node.IsRented,
			})
		}
		if derefBool(node.HysteriaEnabled) {
			ports = append(ports, model.PortInfo{
				Port:     eff.HysteriaPort,
				Protocol: "udp",
				Service:  "Hysteria 2",
				IsOwner:  !node.IsRented,
			})
		}
		if node.Proxy.SocksEnabled && node.Proxy.SocksPort > 0 {
			ports = append(ports, model.PortInfo{
				Port:     node.Proxy.SocksPort,
				Protocol: "tcp",
				Service:  "System Proxy (SOCKS5)",
				IsOwner:  !node.IsRented,
			})
		}
		if node.Proxy.HTTPEnabled && node.Proxy.HTTPPort > 0 {
			ports = append(ports, model.PortInfo{
				Port:     node.Proxy.HTTPPort,
				Protocol: "tcp",
				Service:  "System Proxy (HTTP)",
				IsOwner:  !node.IsRented,
			})
		}
	}

	// Add custom inbounds on this server
	inbounds, _ := m.store.Inbounds(nodeID)
	for _, in := range inbounds {
		ports = append(ports, model.PortInfo{
			Port:     in.Port,
			Protocol: string(in.Opts.Transport),
			Service:  "Inbound: " + in.Name,
			IsOwner:  in.TenantID == "",
			TenantID: in.TenantID,
		})
	}

	return ports, nil
}

// CalculateTenantResourceShare calculates the per-tenant bandwidth limit and quota percent for a node.
func (m *Manager) CalculateTenantResourceShare(nodeID int64) (quotaPercent int, speedLimitKbps int, err error) {
	node, err := m.store.GetNode(nodeID)
	if err != nil {
		return 0, 0, err
	}
	if node == nil {
		return 0, 0, invalidCode("err.nodeNotFound", "нода не найдена")
	}
	tenants, err := m.store.ListNodeTenants(nodeID)
	if err != nil {
		return 0, 0, err
	}
	count := len(tenants)
	return model.CalculateTenantQuota(node.ShareQuotaPercent, count),
		model.CalculateTenantSpeed(node.ShareSpeedLimit, count),
		nil
}
