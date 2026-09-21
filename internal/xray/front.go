package xray

import (
	"encoding/json"
	"strconv"
)

// FrontVLESSRaw is Options.FrontVLESS for a config that arrives finished — a node's,
// which the panel generates with the lane on its public port so that an agent without
// a front keeps working. It returns the config with the TCP-TLS lane moved to
// VLESSInnerAddr and taking a PROXY header, and the public port it had, for the front.
//
// 0 and the config as given when there is no such lane, when another inbound already
// holds VLESSInnerPort (a custom inbound from before the port was reserved), or when
// the config does not parse: the lane then stays where it was, on its own.
func FrontVLESSRaw(data []byte) ([]byte, int) {
	cfg, err := decodeRaw(data)
	if err != nil {
		return data, 0
	}
	inbounds, _ := cfg["inbounds"].([]any)
	var lane map[string]any
	for _, raw := range inbounds {
		in, tag := asInbound(raw)
		if tag == TagVLESS {
			lane = in
			continue
		}
		if p, ok := inboundPort(in); ok && p == VLESSInnerPort {
			return data, 0
		}
	}
	if lane == nil {
		return data, 0
	}
	public, ok := inboundPort(lane)
	if !ok || public == VLESSInnerPort {
		return data, 0
	}
	stream, _ := lane["streamSettings"].(map[string]any)
	if stream == nil {
		return data, 0
	}
	sockopt, _ := stream["sockopt"].(map[string]any)
	if sockopt == nil {
		sockopt = map[string]any{}
	}
	sockopt["acceptProxyProtocol"] = true
	stream["sockopt"] = sockopt
	lane["listen"], lane["port"] = "127.0.0.1", VLESSInnerPort
	out, err := json.Marshal(cfg)
	if err != nil {
		return data, 0
	}
	return out, public
}

// inboundPort reads a single-port inbound's port, number or string.
func inboundPort(in map[string]any) (int, bool) {
	var s string
	switch v := in["port"].(type) {
	case json.Number:
		s = v.String()
	case string:
		s = v
	case float64:
		return int(v), true
	default:
		return 0, false
	}
	p, err := strconv.Atoi(s)
	return p, err == nil
}
