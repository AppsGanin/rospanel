package core

import (
	"context"
	"crypto/rand"
	"math/big"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// PickRealityDonor draws a donor for a fresh install from model.RealityDonors, the
// first that passes the donor check from this server — a site that refuses this
// network would leave REALITY dead on arrival. Tries a few within a short budget; with
// no network at all it takes the first of the draw, which the operator's next save
// checks anyway.
func PickRealityDonor() string {
	order := shuffled(model.RealityDonors)
	for i, host := range order {
		if i == 4 {
			break
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := validateRealityDest(ctx, host)
		cancel()
		if err == nil {
			return host
		}
		logWarn("reality: donor refused at first run, trying another", "donor", host, "err", err)
	}
	return order[0]
}

// PickRealityPort draws a free TCP port for a fresh install's REALITY inbound, off
// the ports anything well known sits on.
func PickRealityPort() int {
	for range 20 {
		n, err := rand.Int(rand.Reader, big.NewInt(40000))
		if err != nil {
			break
		}
		p := 20000 + int(n.Int64())
		if p == model.DefaultRealityPort {
			continue
		}
		ln, err := net.Listen("tcp", ":"+strconv.Itoa(p))
		if err != nil {
			continue
		}
		_ = ln.Close()
		return p
	}
	return model.DefaultRealityPort
}

func shuffled(in []string) []string {
	out := append([]string(nil), in...)
	for i := len(out) - 1; i > 0; i-- {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return out
		}
		j := int(n.Int64())
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// realityDestAfterRename is a server's donor list once its own name moved from oldOwn
// to newOwn ("" for a bare address). The old name in the list becomes the new one, or
// drops out for an address; a list left empty takes pick(). A server still in its
// first-run wizard takes its new name outright — no link carries the donor yet.
func realityDestAfterRename(dest, oldOwn, newOwn string, fresh bool, pick func() string) string {
	if fresh && newOwn != "" {
		return newOwn
	}
	var out []string
	for _, d := range strings.Split(dest, ",") {
		if d = strings.TrimSpace(d); d == "" {
			continue
		}
		if oldOwn != "" && strings.EqualFold(d, oldOwn) {
			if d = newOwn; d == "" {
				continue
			}
		}
		if !slices.ContainsFunc(out, func(o string) bool { return strings.EqualFold(o, d) }) {
			out = append(out, d)
		}
	}
	if len(out) == 0 {
		return pick()
	}
	return strings.Join(out, ",")
}

// hostDomain is host when it is a domain, "" for an address.
func hostDomain(host string) string {
	host = strings.TrimSpace(host)
	if _, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		return ""
	}
	return host
}
