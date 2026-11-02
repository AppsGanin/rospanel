package core

import (
	"context"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// A copied inbound keeps everything the operator set, CDN included, and loses what
// belongs to the source machine: its keys, its WireGuard loopback port, its row, and
// an SNI or Host that names the source server itself.
func TestCopiedInboundDropsMachineMaterial(t *testing.T) {
	t.Parallel()
	in := model.Inbound{
		ID: 7, ServerID: 2, Sort: 3, CreatedAt: 99, Enabled: true,
		Name: "ws", Protocol: model.InbVLESS, Port: 8443,
		Opts: model.InboundOpts{
			Transport: "ws", Security: model.SecReality,
			SNI: "NL.example.com", Host: "nl.example.com", Path: "/p",
			RealityDest: "www.example.org", RealityPrivateKey: "priv", RealityPublicKey: "pub", RealityShortID: "ab",
			ShadowKey: "sk", WGPrivateKey: "wpriv", WGPublicKey: "wpub", WGLocalPort: 40000, TurnMaskKey: "mask",
			EncMode: "native", Decryption: "dec", Encryption: "enc",
			CDNHost: "cdn.example.net", CDNPort: 8443, Obfs: "obfs",
		},
	}
	got := copiedInbound(in, 0, "nl.example.com")
	if got.ID != 0 || got.ServerID != 0 || got.Sort != 0 || got.CreatedAt != 0 {
		t.Errorf("row identity kept: %+v", got)
	}
	o := got.Opts
	if o.RealityPrivateKey != "" || o.RealityPublicKey != "" || o.RealityShortID != "" ||
		o.ShadowKey != "" || o.WGPrivateKey != "" || o.WGPublicKey != "" || o.WGLocalPort != 0 ||
		o.TurnMaskKey != "" || o.Decryption != "" || o.Encryption != "" {
		t.Errorf("machine material kept: %+v", o)
	}
	if o.SNI != "" || o.Host != "" {
		t.Errorf("the source's own name kept: sni=%q host=%q", o.SNI, o.Host)
	}
	if o.CDNHost != "cdn.example.net" || o.CDNPort != 8443 || o.Obfs != "obfs" || o.EncMode != "native" ||
		o.RealityDest != "www.example.org" || o.Path != "/p" || got.Port != 8443 || got.Name != "ws" {
		t.Errorf("operator settings lost: %+v", got)
	}
	if in.Opts.RealityPrivateKey != "priv" {
		t.Error("the source inbound was changed")
	}

	// A name of its own is not the source's address and stays.
	other := copiedInbound(in, 0, "de.example.com")
	if other.Opts.SNI != "NL.example.com" || other.Opts.Host != "nl.example.com" {
		t.Errorf("an unrelated SNI/Host was cleared: %+v", other.Opts)
	}
}

func TestCopyInboundsRefusesSameServer(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	if _, _, err := m.CopyInbounds(context.Background(), 0, 0, []int64{1}); err == nil {
		t.Fatal("copying a server onto itself was accepted")
	}
	// Picked inbounds that are no longer there: an error, not an empty success.
	n, err := m.store.CreateNode("NL", "203.0.113.7", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.CopyInbounds(context.Background(), 0, n.ID, []int64{12345}); err == nil {
		t.Fatal("copying vanished inbounds reported success")
	}
}
