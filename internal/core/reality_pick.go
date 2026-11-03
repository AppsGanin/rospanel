package core

import (
	"context"
	"crypto/rand"
	"math/big"
	"net/netip"
	"slices"
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

// PickRealityDonors is what a fresh install's REALITY lane accepts as its mask: the
// drawn donor, and with it the same site's other name when that site's certificate
// covers it ("hh.ru" and "www.hh.ru" are one site). A shared 443 hands every listed
// name to REALITY, and REALITY relays a name it cannot authenticate to the ONE site in
// reality_dest — so a second site's name would be answered with the first site's
// certificate, which is a signature of its own. One site's names cost nothing.
func PickRealityDonors() []string {
	primary := PickRealityDonor()
	out := []string{primary}
	for _, name := range donorCandidates(primary)[1:] {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := validateRealityDest(ctx, name)
		cancel()
		if err == nil {
			out = append(out, name)
			continue
		}
		logWarn("reality: the donor's twin name is not covered by its certificate", "name", name, "err", err)
	}
	return out
}

// donorCandidates is the drawn name followed by the site's twin: the www. form of a
// bare name, the bare form of a www. one. Whether the twin is really the same site is
// decided by its certificate, not by its spelling — the caller checks it live.
func donorCandidates(primary string) []string {
	twin := "www." + primary
	if strings.HasPrefix(primary, "www.") {
		twin = strings.TrimPrefix(primary, "www.")
	}
	return []string{primary, twin}
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
//
// A lane sharing the TCP-TLS lane's port keeps its donor list as it is: on that port
// the server's own name is the decoy's, so it can neither be adopted nor left in the
// list (see model.RealityShareConflict). shared is that: this lane is on that port.
func realityDestAfterRename(dest, oldOwn, newOwn string, fresh, shared bool, pick func() string) string {
	if shared {
		return dest
	}
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
