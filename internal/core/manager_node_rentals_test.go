package core

import (
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
	return New(st, sup, xray.Options{}, TLSPaths{}, dir)
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
