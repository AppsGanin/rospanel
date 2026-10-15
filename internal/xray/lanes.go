package xray

import (
	"encoding/binary"
	"strings"
)

// Traffic by protocol. Xray counts every inbound's bytes on its own (policy.system
// statsInbound*), which is the per-protocol split of a server's traffic — the per-user
// counters cannot give it, being one number per user across every inbound.

// LaneAWG is the lane key of the AmneziaWG tunnel, which runs outside Xray.
const LaneAWG = "awg"

// laneTags are the built-in inbounds whose traffic is a lane's; custom inbounds count
// under their own tags (custom-<id>). The API, the system proxies and the panel's
// egress carry no user's tunnel and are left out.
var laneTags = map[string]bool{TagVLESS: true, TagReality: true, TagHysteria: true, TagNaive: true, TagMieru: true, TagAnyTLS: true, TagMTProto: true}

// IsLaneTag reports whether an inbound tag is a lane the split shows.
func IsLaneTag(tag string) bool {
	return laneTags[tag] || strings.HasPrefix(tag, customTagPrefix)
}

// QueryLanesLive reads, and zeroes, the per-inbound counters through the gRPC API: what
// each lane carried since the previous read. NaiveProxy reaches naive-in through the
// fallback of the TCP-TLS inbound, and so does AnyTLS, which counts those bytes too; they are taken off
// VLESS so a tunnel is not counted on two lanes. The web traffic of the decoy and the
// panel behind :443 stays in VLESS — it is not a lane of its own.
func (s *Supervisor) QueryLanesLive(apiAddr string) (map[string]Traffic, error) {
	s.mu.Lock()
	if s.statsAPI == nil || s.statsAPI.addr != apiAddr {
		if s.statsAPI != nil {
			s.statsAPI.close()
		}
		s.statsAPI = newXrayAPI(apiAddr, s.waitFn)
	}
	api := s.statsAPI
	s.mu.Unlock()
	raw, err := api.queryStatsReset("inbound>>>")
	if err != nil {
		return nil, err
	}
	return lanesOf(raw), nil
}

// lanesOf keeps the lane inbounds and takes the shares of the lanes behind the TCP-TLS
// fallback — NaiveProxy and AnyTLS — off VLESS.
func lanesOf(raw map[string]Traffic) map[string]Traffic {
	out := make(map[string]Traffic, len(raw))
	for tag, t := range raw {
		if IsLaneTag(tag) && (t.Up > 0 || t.Down > 0) {
			out[tag] = t
		}
	}
	for _, behind := range []string{TagNaive, TagAnyTLS} {
		n, ok := out[behind]
		if !ok {
			continue
		}
		if v, ok := out[TagVLESS]; ok {
			v.Up, v.Down = max(v.Up-n.Up, 0), max(v.Down-n.Down, 0)
			if v.Up == 0 && v.Down == 0 {
				delete(out, TagVLESS)
			} else {
				out[TagVLESS] = v
			}
		}
	}
	return out
}

// decodeInboundStats reads a QueryStatsResponse of inbound counters into per-tag
// traffic ("inbound>>>TAG>>>traffic>>>uplink|downlink").
func decodeInboundStats(msg []byte) (map[string]Traffic, error) {
	out := map[string]Traffic{}
	for len(msg) > 0 {
		field, stat, rest, err := pbNext(msg)
		if err != nil {
			return nil, err
		}
		msg = rest
		if field != 1 {
			continue
		}
		var name string
		var value int64
		for len(stat) > 0 {
			f, v, r, err := pbNext(stat)
			if err != nil {
				return nil, err
			}
			stat = r
			switch f {
			case 1:
				name = string(v)
			case 2:
				n, _ := binary.Uvarint(v)
				value = int64(n)
			}
		}
		foldInboundStat(out, name, value)
	}
	return out, nil
}

func foldInboundStat(out map[string]Traffic, name string, val int64) {
	parts := strings.Split(name, ">>>")
	if len(parts) != 4 || parts[0] != "inbound" || parts[2] != "traffic" {
		return
	}
	t := out[parts[1]]
	switch parts[3] {
	case "uplink":
		t.Up = val
	case "downlink":
		t.Down = val
	}
	out[parts[1]] = t
}
