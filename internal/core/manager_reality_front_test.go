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
