package awg

import (
	"fmt"
	"sort"
	"strings"
)

// peerUpdates changes only peers that joined, left, or changed address. A full
// replace_peers operation discards every peer's endpoint and session keys, even
// when the supplied list is identical to the one already running.
func peerUpdates(previous, next []Peer) (string, error) {
	old := make(map[string]Peer, len(previous))
	want := make(map[string]Peer, len(next))
	for _, p := range previous {
		old[p.PublicKey] = p
	}
	for _, p := range next {
		want[p.PublicKey] = p
	}
	var removed, changed []string
	for key := range old {
		if _, exists := want[key]; !exists {
			removed = append(removed, key)
		}
	}
	for key, p := range want {
		if was, exists := old[key]; !exists || was.Addr != p.Addr {
			changed = append(changed, key)
		}
	}
	sort.Strings(removed)
	sort.Strings(changed)
	var b strings.Builder
	for _, key := range removed {
		h, err := keyHex(key)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "public_key=%s\nremove=true\n", h)
	}
	for _, key := range changed {
		p := want[key]
		h, err := keyHex(key)
		if err != nil {
			return "", err
		}
		if !p.Addr.IsValid() {
			return "", fmt.Errorf("peer %s: no address", p.Email)
		}
		fmt.Fprintf(&b, "public_key=%s\nreplace_allowed_ips=true\nallowed_ip=%s/32\n", h, p.Addr)
	}
	return b.String(), nil
}
