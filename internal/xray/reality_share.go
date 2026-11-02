package xray

import (
	"encoding/json"

	"github.com/AppsGanin/rospanel/internal/model"
)

// REALITY on the TCP-TLS lane's port. Two TLS listeners cannot share a port, but the
// front in front of that port can tell them apart by the name in the ClientHello: a
// connection naming one of the REALITY donor names goes to the REALITY listener, every
// other one — the panel's own name, an address with no name at all — the way it always
// went (see tlsfront.Front.Share). An operator asks for it by setting the REALITY port
// to the TLS lane's: the panel then moves the REALITY inbound behind the front, and
// what clients see in their links is still that port.

// RealityRoute names the donor SNIs the front must hand to REALITY, and whether the
// lane is behind the front at all — it is not when REALITY has a port of its own.
func RealityRoute(cfg *Config) (snis []string, behind bool) {
	listen, port := InnerListen(RealityInnerAddr)
	for _, in := range cfg.Inbounds {
		if in.Tag != TagReality || in.Listen != listen || in.Port != port {
			continue
		}
		if in.StreamSettings == nil || in.StreamSettings.RealitySettings == nil {
			continue
		}
		return in.StreamSettings.RealitySettings.ServerNames, true
	}
	return nil, false
}

// RealityRouteRaw is RealityRoute for a config as JSON, the form a node holds.
func RealityRouteRaw(data []byte) (snis []string, behind bool) {
	listen, port := InnerListen(RealityInnerAddr)
	var doc struct {
		Inbounds []struct {
			Tag            string `json:"tag"`
			Listen         string `json:"listen"`
			Port           int    `json:"port"`
			StreamSettings struct {
				RealitySettings *struct {
					ServerNames []string `json:"serverNames"`
				} `json:"realitySettings"`
			} `json:"streamSettings"`
		} `json:"inbounds"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return nil, false
	}
	for _, in := range doc.Inbounds {
		if in.Tag != TagReality || in.Listen != listen || in.Port != port {
			continue
		}
		if in.StreamSettings.RealitySettings == nil {
			return nil, false
		}
		return in.StreamSettings.RealitySettings.ServerNames, true
	}
	return nil, false
}

// realityListen is where the REALITY inbound listens: behind the front on the inner
// address when its port is shared with the TCP-TLS lane's, on the public wildcard
// otherwise.
func realityListen(set *model.Settings) (string, int) {
	if set.RealityShared() {
		return InnerListen(RealityInnerAddr)
	}
	return "0.0.0.0", set.RealityPort
}
