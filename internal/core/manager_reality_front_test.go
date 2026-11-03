package core

import (
	"path/filepath"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// fakeSharer stands in for the front: it records the route the manager gave it.
type fakeSharer struct {
	target string
	snis   []string
	calls  int
}

func (f *fakeSharer) Share(target string, snis []string) {
	f.target, f.snis, f.calls = target, snis, f.calls+1
}

// shareFront keeps the front's REALITY route in step with the applied config: the
// donor names when the lane sits behind it, nothing when it has a port of its own.
func TestShareFrontFollowsTheConfig(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	dir := t.TempDir()
	m.sup = xray.NewSupervisor("", filepath.Join(dir, "config.json"), dir)
	t.Cleanup(m.sup.Stop)
	front := &fakeSharer{}
	m.SetFront(front)

	listen, port := xray.InnerListen(xray.RealityInnerAddr)
	behind := &xray.Config{Inbounds: []xray.Inbound{{
		Tag:    xray.TagReality,
		Listen: listen,
		Port:   port,
		StreamSettings: &xray.StreamSettings{
			RealitySettings: &xray.RealitySettings{ServerNames: []string{"max.ru"}},
		},
	}}}
	set := &model.Settings{VLESSPort: 443, RealityPort: 443, RealityDest: "max.ru"}
	m.shareFront(set, behind)
	if front.target != xray.RealityInnerAddr || len(front.snis) != 1 || front.snis[0] != "max.ru" {
		t.Fatalf("front got %q %v, want the REALITY address and its donor", front.target, front.snis)
	}

	// Back on its own port: the split is off, and the supervisor forgets the public
	// port so a removed user's REALITY sockets are not hunted on 443 any more.
	m.shareFront(set, &xray.Config{Inbounds: []xray.Inbound{{Tag: xray.TagReality, Listen: "0.0.0.0", Port: 8443}}})
	if front.target != "" || front.snis != nil {
		t.Fatalf("front still split: %q %v", front.target, front.snis)
	}
}

// Without a front — a process run with ROSPANEL_TLS_FRONT=off — there is nothing to
// tell, and nothing may panic.
func TestShareFrontWithoutAFront(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	m.shareFront(&model.Settings{VLESSPort: 443, RealityPort: 443}, &xray.Config{})
}

// Saving REALITY onto the TCP-TLS port through the master's own editor is a save like
// any other: the port is the shared one, and the donor list goes with it.
func TestMasterAcceptsRealityOnTheSharedPort(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	u := DefaultConnections()
	u.RealityPort = 443
	u.RealityDest = "max.ru"
	u.Protocols["reality"] = false // the live donor probe stays out of the test
	if err := m.ApplyConnections(u); err != nil {
		t.Fatalf("saving REALITY on 443: %v", err)
	}
	set, err := m.store.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !set.RealityShared() || set.RealityDest != "max.ru" {
		t.Fatalf("saved %d/%q, want REALITY on the TLS port with its donor", set.RealityPort, set.RealityDest)
	}
}

// The master refuses a mask that is its own name on the shared port: the decoy serves
// that name there, so the REALITY client would never reach the lane. On a port of its
// own the same list is exactly what the panel wants.
func TestMasterRefusesItsOwnNameOnTheSharedPort(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	if err := m.store.SetTLSMode(model.TLSModeACME, "vpn.example.com", "vpn.example.com", ""); err != nil {
		t.Fatal(err)
	}
	u := DefaultConnections()
	u.RealityPort = 443
	u.RealityDest = "vpn.example.com"
	u.Protocols["reality"] = false
	if err := m.ApplyConnections(u); errCode(err) != "err.realityOwnNameOn443" {
		t.Fatalf("own name on the shared port: %v (code %q)", err, errCode(err))
	}
	// The same mask is fine on a port of the lane's own.
	u.RealityPort = 9443
	if err := m.ApplyConnections(u); err != nil {
		t.Fatalf("own name on its own port: %v", err)
	}
}

// The quick donor edit on a server card goes through the same rule: on the shared port
// the lane masks as a donor, never as the server's own name.
func TestRealityDonorEditRefusesItsOwnNameOnTheSharedPort(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	if err := m.store.SetTLSMode(model.TLSModeACME, "vpn.example.com", "vpn.example.com", ""); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetRealityPorts(443, "max.ru"); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetProtocolEnabled("reality", false); err != nil { // no live donor probe
		t.Fatal(err)
	}
	if err := m.SetMasterReality("vpn.example.com", false); errCode(err) != "err.realityOwnNameOn443" {
		t.Fatalf("master donor edit: %v (code %q)", err, errCode(err))
	}
	if err := m.SetMasterReality("hh.ru", false); err != nil {
		t.Fatalf("a donor on the shared port: %v", err)
	}

	n, err := m.CreateNode("NL", "nl.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetNodeReality(n.ID, "nl.example.com", false); errCode(err) != "err.realityOwnNameOn443" {
		t.Fatalf("node donor edit: %v (code %q)", err, errCode(err))
	}
	if err := m.SetNodeReality(n.ID, "", false); err != nil {
		t.Fatalf("clearing the node's donor: %v", err)
	}
}
