package xray

import (
	"testing"

	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
)

func TestRentalInboundClientIsolation(t *testing.T) {
	set := baseSettings()
	ownerUsers := []model.User{
		{ID: 1, Name: "owner_u1", UUID: "uuid-owner-1", Status: "active", Enabled: true},
		{ID: 2, Name: "owner_u2", UUID: "uuid-owner-2", Status: "active", Enabled: true},
	}

	rentalInbound := model.Inbound{
		ID:       10,
		ServerID: model.LocalNodeID,
		Enabled:  true,
		Name:     "Rental Inbound",
		Protocol: model.InbVLESS,
		Port:     8877,
		TenantID: "tenant_xyz",
		Opts: model.InboundOpts{
			Transport: model.TrTCP,
			Security:  model.SecTLS,
			Clients: []model.InboundClient{
				{ID: "uuid-renter-1", Email: "t_tenant_xyz_1"},
				{ID: "uuid-renter-2", Email: "t_tenant_xyz_2"},
			},
		},
	}

	opts := Options{
		PanelDest: "127.0.0.1:8080",
		Custom:    []model.Inbound{rentalInbound},
	}

	cfg, err := Generate(set, ownerUsers, opts, nil)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// 1. Check built-in VLESS inbound: must only have owner users
	vlessIn := findInbound(cfg, TagVLESS)
	if vlessIn == nil {
		t.Fatal("vless-in missing")
	}
	vlessSettings, ok := vlessIn.Settings.(VLESSInboundSettings)
	if !ok {
		t.Fatalf("unexpected type %T", vlessIn.Settings)
	}
	if len(vlessSettings.Clients) != 2 {
		t.Fatalf("expected 2 owner clients on built-in vless, got %d", len(vlessSettings.Clients))
	}
	for _, c := range vlessSettings.Clients {
		if c.ID != "uuid-owner-1" && c.ID != "uuid-owner-2" {
			t.Errorf("unexpected client on built-in vless: %s", c.ID)
		}
	}

	// 2. Check custom rental inbound: must only have tenant users
	customIn := findInbound(cfg, rentalInbound.Tag())
	if customIn == nil {
		t.Fatal("custom-10 inbound missing")
	}
	customSettings, ok := customIn.Settings.(VLESSInboundSettings)
	if !ok {
		t.Fatalf("unexpected type %T", customIn.Settings)
	}
	if len(customSettings.Clients) != 2 {
		t.Fatalf("expected 2 tenant clients on rental inbound, got %d", len(customSettings.Clients))
	}
	for _, c := range customSettings.Clients {
		if c.ID != "uuid-renter-1" && c.ID != "uuid-renter-2" {
			t.Errorf("unexpected client on rental inbound: %s", c.ID)
		}
	}

	// 3. Check UserInbounds (live add/remove) ignores rental inbound
	addedUsers := []model.User{
		{ID: 3, Name: "new_owner", UUID: "uuid-owner-3", Status: "active", Enabled: true},
	}
	stubs := UserInbounds(set, []model.Inbound{rentalInbound}, addedUsers, model.LocalNodeID, nil)
	for _, stub := range stubs {
		if stub.Tag == rentalInbound.Tag() {
			t.Errorf("UserInbounds produced stub for rental inbound %s", stub.Tag)
		}
	}
}
