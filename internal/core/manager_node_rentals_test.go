package core

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
	"github.com/Shu1t3/rospanel-shu1t3/internal/store"
	"github.com/Shu1t3/rospanel-shu1t3/internal/xray"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "core_test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	sup := xray.NewSupervisor("", filepath.Join(dir, "config.json"), dir)
	return New(st, sup, xray.Options{PanelDest: "127.0.0.1:8080"}, TLSPaths{}, dir)
}

func TestManagerNodeRentalFlow(t *testing.T) {
	mgr := newTestManager(t)

	// 1. Create owner node
	node, err := mgr.store.CreateNode("Germany Master Node", "de.ros.example.com", "test")
	if err != nil {
		t.Fatalf("CreateNode failed: %v", err)
	}
	vlessTrue := true
	_ = mgr.store.UpdateNode(node.ID, store.NodeEdit{
		Name:  node.Name,
		Host:  node.Host,
		VLESS: &vlessTrue,
	})

	// 2. Initial rental settings
	st, err := mgr.GetNodeRentalSettings(node.ID)
	if err != nil {
		t.Fatalf("GetNodeRentalSettings failed: %v", err)
	}
	if st.ShareEnabled {
		t.Errorf("want ShareEnabled = false initially")
	}

	// 3. Update rental settings
	updated, err := mgr.UpdateNodeRentalSettings(node.ID, model.NodeRentalSettings{
		ShareEnabled:      true,
		ShareQuotaPercent: 60,
		ShareSpeedLimit:   60000,
	})
	if err != nil {
		t.Fatalf("UpdateNodeRentalSettings failed: %v", err)
	}
	if !updated.ShareEnabled || updated.ShareQuotaPercent != 60 || updated.ShareSpeedLimit != 60000 {
		t.Errorf("unexpected updated settings: %+v", updated)
	}

	// 4. Generate Share Link
	link, err := mgr.GenerateNodeShareLink(node.ID)
	if err != nil {
		t.Fatalf("GenerateNodeShareLink failed: %v", err)
	}
	if link == "" {
		t.Fatalf("expected non-empty share link")
	}

	// 5. Import Rented Node on another panel (or same manager with distinct name)
	rentedNode, err := mgr.ImportRentedNode(link, "Rented Germany")
	if err != nil {
		t.Fatalf("ImportRentedNode failed: %v", err)
	}
	if !rentedNode.IsRented {
		t.Errorf("want rentedNode.IsRented = true")
	}
	if rentedNode.RentOwnerNodeID != node.ID {
		t.Errorf("want RentOwnerNodeID = %d, got %d", node.ID, rentedNode.RentOwnerNodeID)
	}

	// 6. Check that rented node cannot be re-shared
	_, err = mgr.GetNodeRentalSettings(rentedNode.ID)
	if err == nil {
		t.Errorf("GetNodeRentalSettings on rented node want error, got nil")
	}

	// 7. Register Tenants on owner node and test resource division
	_ = mgr.store.RegisterNodeTenant(node.ID, model.NodeTenant{TenantID: "t_1", Name: "Tenant 1", SpeedLimit: 30000})
	_ = mgr.store.RegisterNodeTenant(node.ID, model.NodeTenant{TenantID: "t_2", Name: "Tenant 2", SpeedLimit: 30000})

	quota, speed, err := mgr.CalculateTenantResourceShare(node.ID)
	if err != nil {
		t.Fatalf("CalculateTenantResourceShare failed: %v", err)
	}
	if quota != 30 { // 60% / 2
		t.Errorf("want quota = 30, got %d", quota)
	}
	if speed != 30000 { // 60000 / 2
		t.Errorf("want speed = 30000, got %d", speed)
	}

	// 8. Reserved ports
	ports, err := mgr.GetNodeReservedPorts(node.ID)
	if err != nil {
		t.Fatalf("GetNodeReservedPorts failed: %v", err)
	}
	if len(ports) == 0 {
		t.Errorf("want non-empty reserved ports")
	}

	// 9. NodeViews verification
	views, err := mgr.NodeViews()
	if err != nil {
		t.Fatalf("NodeViews failed: %v", err)
	}
	var foundOwner, foundRented bool
	for _, v := range views {
		if v.ID == node.ID {
			foundOwner = true
			if !v.ShareEnabled || v.ActiveTenants != 2 {
				t.Errorf("owner view mismatch: ShareEnabled=%v, ActiveTenants=%d", v.ShareEnabled, v.ActiveTenants)
			}
		}
		if v.ID == rentedNode.ID {
			foundRented = true
			if !v.IsRented {
				t.Errorf("rented view mismatch: IsRented=%v", v.IsRented)
			}
		}
	}
	if !foundOwner || !foundRented {
		t.Errorf("foundOwner=%v, foundRented=%v", foundOwner, foundRented)
	}

	// 10. Delete rented node (tenant detachment)
	err = mgr.DeleteNode(rentedNode.ID)
	if err != nil {
		t.Fatalf("DeleteNode on rented node failed: %v", err)
	}
	rentedCheck, _ := mgr.store.GetNode(rentedNode.ID)
	if rentedCheck != nil {
		t.Errorf("rented node should be deleted")
	}
}

func TestRentalSyncMasterHostResolution(t *testing.T) {
	mgr := newTestManager(t)

	// Set master panel host
	_ = mgr.store.SetTLS("master.ros.example.com", "master.ros.example.com", "manual", "", "")

	// Create a worker node with different host/IP
	node, err := mgr.store.CreateNode("Worker Node 1", "77.238.243.13", "test")
	if err != nil {
		t.Fatalf("CreateNode failed: %v", err)
	}
	vlessTrue := true
	_ = mgr.store.UpdateNode(node.ID, store.NodeEdit{
		Name:  node.Name,
		Host:  node.Host,
		VLESS: &vlessTrue,
	})

	_, _ = mgr.UpdateNodeRentalSettings(node.ID, model.NodeRentalSettings{
		ShareEnabled:      true,
		ShareQuotaPercent: 50,
		ShareSpeedLimit:   512000,
	})

	link, err := mgr.GenerateNodeShareLink(node.ID)
	if err != nil {
		t.Fatalf("GenerateNodeShareLink failed: %v", err)
	}

	payload, err := model.DecodeShareLink(link)
	if err != nil {
		t.Fatalf("DecodeShareLink failed: %v", err)
	}

	if payload.Host != "77.238.243.13" {
		t.Errorf("want Host = 77.238.243.13, got %s", payload.Host)
	}
	if payload.MasterHost != "master.ros.example.com" {
		t.Errorf("want MasterHost = master.ros.example.com, got %s", payload.MasterHost)
	}

	rented, err := mgr.ImportRentedNode(link, "Imported Worker")
	if err != nil {
		t.Fatalf("ImportRentedNode failed: %v", err)
	}

	storedNode, err := mgr.store.GetNode(rented.ID)
	if err != nil {
		t.Fatalf("GetNode failed: %v", err)
	}
	if storedNode.RentMasterHost != "master.ros.example.com" {
		t.Errorf("want storedNode.RentMasterHost = master.ros.example.com, got %s", storedNode.RentMasterHost)
	}
}

func TestTenantInboundClientsAuthentication(t *testing.T) {
	mgr := newTestManager(t)

	// Owner node
	ownerNode, err := mgr.store.CreateNode("Main Server", "owner.ros.example.com", "test")
	if err != nil {
		t.Fatalf("CreateNode failed: %v", err)
	}
	_, _ = mgr.UpdateNodeRentalSettings(ownerNode.ID, model.NodeRentalSettings{
		ShareEnabled:      true,
		ShareQuotaPercent: 50,
	})
	link, _ := mgr.GenerateNodeShareLink(ownerNode.ID)
	rentedNode, err := mgr.ImportRentedNode(link, "Rented Server")
	if err != nil {
		t.Fatalf("ImportRentedNode failed: %v", err)
	}

	// Create users on tenant side
	u1, _ := mgr.store.CreateUser("tenant_alice", "uuid-alice-1234", "pass-alice-5678", "tok1", 0, 0, 0)
	u2, _ := mgr.store.CreateUser("tenant_bob", "uuid-bob-1234", "pass-bob-5678", "tok2", 0, 0, 0)

	// Create custom inbound on rented node
	inboundID, err := mgr.store.CreateInbound(model.Inbound{
		ServerID: rentedNode.ID,
		Enabled:  true,
		Name:     "Trojan Tenant",
		Protocol: model.InbTrojan,
		Port:     2053,
		Opts: model.InboundOpts{
			Transport: model.TrTCP,
			Security:  model.SecTLS,
		},
	})
	if err != nil {
		t.Fatalf("CreateInbound failed: %v", err)
	}

	// Build rental sync request attaching tenant clients
	inbounds, _ := mgr.store.Inbounds(rentedNode.ID)
	users, _ := mgr.store.WorkingUsers(0)
	for i := range inbounds {
		in := &inbounds[i]
		for _, u := range users {
			in.Opts.Clients = append(in.Opts.Clients, model.InboundClient{
				Password: u.Password,
				Email:    u.Name,
			})
		}
	}

	syncReq := model.NodeRentalSyncReq{
		NodeID:     ownerNode.ID,
		ShareToken: rentedNode.RentShareKey,
		TenantID:   rentedNode.RentTenantID,
		TenantName: rentedNode.Name,
		Inbounds:   inbounds,
	}

	// Process sync on owner side
	resp, err := mgr.ProcessRentalSync(syncReq)
	if err != nil {
		t.Fatalf("ProcessRentalSync failed: %v", err)
	}
	if resp == nil {
		t.Fatalf("ProcessRentalSync returned nil resp")
	}

	// Verify owner store has tenant inbound with clients
	ownerInbounds, _ := mgr.store.Inbounds(ownerNode.ID)
	var foundInbound *model.Inbound
	for _, in := range ownerInbounds {
		if in.Port == 2053 && in.TenantID == rentedNode.RentTenantID {
			foundInbound = &in
			break
		}
	}
	if foundInbound == nil {
		t.Fatalf("owner did not store tenant inbound on port 2053")
	}
	if len(foundInbound.Opts.Clients) != 2 {
		t.Fatalf("want 2 tenant clients in inbound, got %d", len(foundInbound.Opts.Clients))
	}
	if foundInbound.Opts.Clients[0].Password != u1.Password || foundInbound.Opts.Clients[1].Password != u2.Password {
		t.Errorf("tenant client passwords mismatch: %+v", foundInbound.Opts.Clients)
	}
	_ = inboundID
}

func TestLocalNodeZeroRental(t *testing.T) {
	mgr := newTestManager(t)

	// Set master panel host
	_ = mgr.store.SetTLS("master.ros.example.com", "master.ros.example.com", "manual", "", "")

	// Update sharing settings on Node 0 (Local master)
	updated, err := mgr.UpdateNodeRentalSettings(model.LocalNodeID, model.NodeRentalSettings{
		ShareEnabled:      true,
		ShareQuotaPercent: 75,
		ShareSpeedLimit:   100000,
	})
	if err != nil {
		t.Fatalf("UpdateNodeRentalSettings for LocalNodeID failed: %v", err)
	}
	if !updated.ShareEnabled || updated.ShareQuotaPercent != 75 || updated.ShareSpeedLimit != 100000 {
		t.Errorf("unexpected settings for LocalNodeID: %+v", updated)
	}

	link, err := mgr.GenerateNodeShareLink(model.LocalNodeID)
	if err != nil {
		t.Fatalf("GenerateNodeShareLink for LocalNodeID failed: %v", err)
	}
	if link == "" {
		t.Fatalf("expected non-empty share link for LocalNodeID")
	}

	rented, err := mgr.ImportRentedNode(link, "Rented Master")
	if err != nil {
		t.Fatalf("ImportRentedNode failed: %v", err)
	}
	if rented.RentOwnerNodeID != model.LocalNodeID {
		t.Errorf("want RentOwnerNodeID = 0, got %d", rented.RentOwnerNodeID)
	}
}

func TestRentedNodeValidationAndProbe(t *testing.T) {
	mgr := newTestManager(t)

	// Create owner node
	ownerNode, err := mgr.store.CreateNode("Owner Node", "owner.ros.example.com", "test")
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	_, err = mgr.UpdateNodeRentalSettings(ownerNode.ID, model.NodeRentalSettings{
		ShareEnabled:      true,
		ShareQuotaPercent: 50,
		ShareSpeedLimit:   50000,
	})
	if err != nil {
		t.Fatalf("UpdateNodeRentalSettings: %v", err)
	}

	link, err := mgr.GenerateNodeShareLink(ownerNode.ID)
	if err != nil {
		t.Fatalf("GenerateNodeShareLink: %v", err)
	}

	// Import rented node on tenant panel
	rentedNode, err := mgr.ImportRentedNode(link, "Tenant Rented Node")
	if err != nil {
		t.Fatalf("ImportRentedNode: %v", err)
	}

	// Create inbound on rented node
	inbound := model.Inbound{
		ServerID: rentedNode.ID,
		Enabled:  true,
		Name:     "Tenant Inbound 8877",
		Protocol: model.InbVLESS,
		Port:     8877,
		Opts: model.InboundOpts{
			Transport:   model.TrTCP,
			Security:    model.SecReality,
			RealityDest: "google.com",
		},
	}

	saved, err := mgr.CreateInbound(context.Background(), inbound)
	if err != nil {
		t.Fatalf("CreateInbound on rented node failed: %v", err)
	}
	if saved.Port != 8877 {
		t.Errorf("want port 8877, got %d", saved.Port)
	}

	// Verify NodeLinkSettings has built-ins disabled for rented node
	linkSets, err := mgr.NodeLinkSettings()
	if err != nil {
		t.Fatalf("NodeLinkSettings failed: %v", err)
	}
	var rentedSettings *model.Settings
	for _, s := range linkSets {
		if s.ServerID == rentedNode.ID {
			rentedSettings = s
			break
		}
	}
	if rentedSettings == nil {
		t.Fatalf("rented node settings missing from NodeLinkSettings")
	}
	if rentedSettings.VLESSEnabled || rentedSettings.RealityEnabled || rentedSettings.HysteriaEnabled {
		t.Errorf("expected built-ins to be disabled for rented node in NodeLinkSettings, got: vless=%v, reality=%v, hy=%v",
			rentedSettings.VLESSEnabled, rentedSettings.RealityEnabled, rentedSettings.HysteriaEnabled)
	}
}

func TestTenantInboundProtection(t *testing.T) {
	mgr := newTestManager(t)

	// 1. Create owner node
	ownerNode, err := mgr.store.CreateNode("Owner Node", "owner.example.com", "secret")
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	// 2. Create an inbound on owner node belonging to a tenant
	tenantInb, err := mgr.store.CreateInbound(model.Inbound{
		ServerID: ownerNode.ID,
		Enabled:  true,
		Name:     "Tenant Inbound",
		Protocol: model.InbVLESS,
		Port:     9999,
		TenantID: "tenant_abc123",
		Opts: model.InboundOpts{
			Transport:   model.TrTCP,
			Security:    model.SecReality,
			RealityDest: "microsoft.com",
		},
	})
	if err != nil {
		t.Fatalf("CreateInbound: %v", err)
	}

	// 3. Attempt UpdateInbound on tenant inbound - should fail
	_, err = mgr.UpdateInbound(context.Background(), model.Inbound{
		ID:       tenantInb.ID,
		ServerID: ownerNode.ID,
		Enabled:  false,
		Name:     "Hacked Name",
		Protocol: model.InbVLESS,
		Port:     9999,
		Opts: model.InboundOpts{
			Transport:   model.TrTCP,
			Security:    model.SecReality,
			RealityDest: "microsoft.com",
		},
	})
	if err == nil {
		t.Fatalf("UpdateInbound on tenant inbound expected error, got nil")
	}
	var vErr *ValidationError
	if errors.As(err, &vErr) {
		if vErr.Code != "err.rentalInboundReadOnly" {
			t.Errorf("want err.rentalInboundReadOnly, got code %q", vErr.Code)
		}
	} else {
		t.Errorf("expected ValidationError, got %v", err)
	}

	// 4. Attempt RegenInboundReality on tenant inbound - should fail
	_, err = mgr.RegenInboundReality(tenantInb.ID)
	if err == nil {
		t.Fatalf("RegenInboundReality on tenant inbound expected error, got nil")
	}
	if errors.As(err, &vErr) {
		if vErr.Code != "err.rentalInboundReadOnly" {
			t.Errorf("want err.rentalInboundReadOnly, got code %q", vErr.Code)
		}
	} else {
		t.Errorf("expected ValidationError, got %v", err)
	}

	// 5. Attempt DeleteInbound on tenant inbound - should fail
	err = mgr.DeleteInbound(tenantInb.ID)
	if err == nil {
		t.Fatalf("DeleteInbound on tenant inbound expected error, got nil")
	}
	if errors.As(err, &vErr) {
		if vErr.Code != "err.rentalInboundReadOnly" {
			t.Errorf("want err.rentalInboundReadOnly, got code %q", vErr.Code)
		}
	} else {
		t.Errorf("expected ValidationError, got %v", err)
	}

	// 6. Create owner's own inbound (TenantID = "") and verify operations succeed
	ownerInb, err := mgr.store.CreateInbound(model.Inbound{
		ServerID: ownerNode.ID,
		Enabled:  true,
		Name:     "Owner Inbound",
		Protocol: model.InbVLESS,
		Port:     10001,
		TenantID: "",
		Opts: model.InboundOpts{
			Transport:   model.TrTCP,
			Security:    model.SecReality,
			RealityDest: "google.com",
		},
	})
	if err != nil {
		t.Fatalf("CreateInbound owner: %v", err)
	}

	// Owner can update
	updated, err := mgr.UpdateInbound(context.Background(), model.Inbound{
		ID:       ownerInb.ID,
		ServerID: ownerNode.ID,
		Enabled:  false,
		Name:     "Owner Inbound Updated",
		Protocol: model.InbVLESS,
		Port:     10001,
		Opts: model.InboundOpts{
			Transport:   model.TrTCP,
			Security:    model.SecReality,
			RealityDest: "google.com",
		},
	})
	if err != nil {
		t.Fatalf("UpdateInbound owner failed: %v", err)
	}
	if updated.Name != "Owner Inbound Updated" {
		t.Errorf("unexpected updated name: %s", updated.Name)
	}

	// Owner can delete
	if err := mgr.DeleteInbound(ownerInb.ID); err != nil {
		t.Fatalf("DeleteInbound owner failed: %v", err)
	}
}

