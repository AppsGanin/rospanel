package store

import (
	"testing"

	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
)

func TestNodeRentalSettings(t *testing.T) {
	st := openNodeStore(t)

	// Create owner node
	node, err := st.CreateNode("Germany Owner", "de.example.com", "test")
	if err != nil {
		t.Fatalf("CreateNode failed: %v", err)
	}

	// Default rental settings
	rSettings, err := st.GetNodeRentalSettings(node.ID)
	if err != nil {
		t.Fatalf("GetNodeRentalSettings failed: %v", err)
	}
	if rSettings.ShareEnabled {
		t.Errorf("want ShareEnabled = false initially")
	}

	// Update rental settings (enable share, 50% quota, 50000 kbps)
	err = st.SetNodeRentalSettings(node.ID, model.NodeRentalSettings{
		ShareEnabled:      true,
		ShareQuotaPercent: 50,
		ShareSpeedLimit:   50000,
	})
	if err != nil {
		t.Fatalf("SetNodeRentalSettings failed: %v", err)
	}

	updated, err := st.GetNodeRentalSettings(node.ID)
	if err != nil {
		t.Fatalf("GetNodeRentalSettings after update failed: %v", err)
	}
	if !updated.ShareEnabled {
		t.Errorf("want ShareEnabled = true")
	}
	if updated.ShareQuotaPercent != 50 {
		t.Errorf("want ShareQuotaPercent = 50, got %d", updated.ShareQuotaPercent)
	}
	if updated.ShareSpeedLimit != 50000 {
		t.Errorf("want ShareSpeedLimit = 50000, got %d", updated.ShareSpeedLimit)
	}
	if updated.ShareToken == "" {
		t.Errorf("want generated ShareToken, got empty")
	}

	// Register a tenant
	tenant := model.NodeTenant{
		TenantID:   "tenant_abc123",
		Name:       "Partner Panel",
		SpeedLimit: 25000,
	}
	if err := st.RegisterNodeTenant(node.ID, tenant); err != nil {
		t.Fatalf("RegisterNodeTenant failed: %v", err)
	}

	// List tenants
	tenants, err := st.ListNodeTenants(node.ID)
	if err != nil {
		t.Fatalf("ListNodeTenants failed: %v", err)
	}
	if len(tenants) != 1 {
		t.Fatalf("want 1 tenant, got %d", len(tenants))
	}
	if tenants[0].TenantID != "tenant_abc123" || tenants[0].Name != "Partner Panel" {
		t.Errorf("unexpected tenant data: %+v", tenants[0])
	}

	// Update tenant traffic
	if err := st.UpdateTenantTraffic(node.ID, "tenant_abc123", 1024, 2048); err != nil {
		t.Fatalf("UpdateTenantTraffic failed: %v", err)
	}
	tenants, _ = st.ListNodeTenants(node.ID)
	if tenants[0].TrafficUp != 1024 || tenants[0].TrafficDown != 2048 {
		t.Errorf("unexpected traffic: up=%d down=%d", tenants[0].TrafficUp, tenants[0].TrafficDown)
	}

	// Delete tenant
	if err := st.DeleteNodeTenant(node.ID, "tenant_abc123"); err != nil {
		t.Fatalf("DeleteNodeTenant failed: %v", err)
	}
	tenants, _ = st.ListNodeTenants(node.ID)
	if len(tenants) != 0 {
		t.Errorf("want 0 tenants after deletion, got %d", len(tenants))
	}
}

func TestRentedNodeLifecycleAndInbounds(t *testing.T) {
	st := openNodeStore(t)

	// Create rented node
	rented, err := st.CreateRentedNode("Rented US", "us.example.com", 100, "token_123", "tenant_xyz", 60, 40000)
	if err != nil {
		t.Fatalf("CreateRentedNode failed: %v", err)
	}
	if !rented.IsRented {
		t.Errorf("want IsRented = true")
	}
	if rented.RentOwnerNodeID != 100 {
		t.Errorf("want RentOwnerNodeID = 100, got %d", rented.RentOwnerNodeID)
	}

	// Cannot enable sharing on a rented node
	err = st.SetNodeRentalSettings(rented.ID, model.NodeRentalSettings{ShareEnabled: true})
	if err != ErrRentedNodeCannotShare {
		t.Errorf("SetNodeRentalSettings on rented node want ErrRentedNodeCannotShare, got %v", err)
	}

	// Create an inbound belonging to tenant
	inbound, err := st.CreateInbound(model.Inbound{
		ServerID: rented.ID,
		Enabled:  true,
		Name:     "Tenant VLESS",
		Protocol: model.InbVLESS,
		Port:     1443,
		TenantID: "tenant_xyz",
		Opts:     model.InboundOpts{Transport: model.TrTCP, Security: model.SecNone},
	})
	if err != nil {
		t.Fatalf("CreateInbound failed: %v", err)
	}
	if inbound.TenantID != "tenant_xyz" {
		t.Errorf("want TenantID = tenant_xyz, got %q", inbound.TenantID)
	}

	// Check that inbound is listed
	inbs, err := st.Inbounds(rented.ID)
	if err != nil || len(inbs) != 1 {
		t.Fatalf("Inbounds failed, len = %d, err = %v", len(inbs), err)
	}

	// Delete rented node
	if err := st.DeleteRentedNode(rented.ID); err != nil {
		t.Fatalf("DeleteRentedNode failed: %v", err)
	}

	// Node should no longer exist
	n, err := st.GetNode(rented.ID)
	if err != nil {
		t.Fatalf("GetNode failed: %v", err)
	}
	if n != nil {
		t.Errorf("want node nil, got %+v", n)
	}

	// Inbound should be wiped
	inbs, _ = st.Inbounds(rented.ID)
	if len(inbs) != 0 {
		t.Errorf("want 0 inbounds after rented node deletion, got %d", len(inbs))
	}
}
