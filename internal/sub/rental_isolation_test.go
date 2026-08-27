package sub

import (
	"strings"
	"testing"

	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
)

func TestRentalInboundIsolation(t *testing.T) {
	u := model.User{ID: 1, Name: "owner_user", UUID: "uuid-owner", Password: "pw"}
	ownerSet := testSet("owner.example.com")
	ownerSet.ServerID = model.LocalNodeID
	ownerSet.IsRented = false

	rentedSet := testSet("rented.example.com")
	rentedSet.ServerID = 4
	rentedSet.IsRented = true
	// On rented node, NodeLinkSettings disables built-ins
	rentedSet.VLESSEnabled = false
	rentedSet.RealityEnabled = false
	rentedSet.HysteriaEnabled = false

	ownerInbound := model.Inbound{
		ID: 101, ServerID: model.LocalNodeID, Enabled: true, Name: "Owner Custom",
		Protocol: model.InbVLESS, Port: 7001,
		TenantID: "",
		Opts:     model.InboundOpts{Transport: model.TrTCP, Security: model.SecTLS},
	}
	tenantInboundOnOwner := model.Inbound{
		ID: 102, ServerID: model.LocalNodeID, Enabled: true, Name: "Tenant Custom On Owner",
		Protocol: model.InbVLESS, Port: 8877,
		TenantID: "tenant_abc",
		Opts:     model.InboundOpts{Transport: model.TrTCP, Security: model.SecTLS},
	}
	tenantInboundOnRented := model.Inbound{
		ID: 201, ServerID: 4, Enabled: true, Name: "Tenant Custom On Rented",
		Protocol: model.InbVLESS, Port: 8877,
		TenantID: "", // on tenant's panel, it was created for rented node
		Opts:     model.InboundOpts{Transport: model.TrTCP, Security: model.SecTLS},
	}

	customMap := map[int64][]model.Inbound{
		model.LocalNodeID: {ownerInbound, tenantInboundOnOwner},
		4:                 {tenantInboundOnRented},
	}

	// 1. Check Owner Panel subscription: must NOT contain tenantInboundOnOwner
	ownerServers := Servers([]*model.Settings{ownerSet}, customMap, model.UnrestrictedAccess())
	ownerLinks := ShareLinksAll(u, ownerServers)

	for _, l := range ownerLinks {
		if strings.Contains(l, ":8877") {
			t.Errorf("Owner user received tenant inbound port 8877: %s", l)
		}
	}

	hasOwnerCustom := false
	for _, l := range ownerLinks {
		if strings.Contains(l, ":7001") {
			hasOwnerCustom = true
			break
		}
	}
	if !hasOwnerCustom {
		t.Errorf("Owner user missing owner custom inbound port 7001")
	}

	// 2. Check Tenant Panel subscription for rented node: must ONLY contain tenant inbounds, NOT owner built-ins
	rentedServers := Servers([]*model.Settings{rentedSet}, customMap, model.UnrestrictedAccess())
	rentedLinks := ShareLinksAll(u, rentedServers)

	if len(rentedLinks) != 1 {
		t.Fatalf("Tenant rented server should produce exactly 1 custom link, got %d:\n%s",
			len(rentedLinks), strings.Join(rentedLinks, "\n"))
	}
	if !strings.Contains(rentedLinks[0], ":8877") {
		t.Errorf("Tenant rented server link does not point to port 8877: %s", rentedLinks[0])
	}
}

func TestInboundScopeModel(t *testing.T) {
	ownerInb := model.Inbound{ID: 1, TenantID: ""}
	if ownerInb.Scope() != model.ScopeOwner {
		t.Errorf("want ScopeOwner, got %v", ownerInb.Scope())
	}
	if !ownerInb.IsOwner() || ownerInb.IsRental() {
		t.Errorf("IsOwner/IsRental mismatch for ownerInb")
	}

	rentalInb := model.Inbound{ID: 2, TenantID: "tenant_1"}
	if rentalInb.Scope() != model.ScopeRental {
		t.Errorf("want ScopeRental, got %v", rentalInb.Scope())
	}
	if rentalInb.IsOwner() || !rentalInb.IsRental() {
		t.Errorf("IsOwner/IsRental mismatch for rentalInb")
	}
}
