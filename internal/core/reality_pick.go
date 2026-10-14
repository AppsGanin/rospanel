package core

import (
	"context"
	"crypto/rand"
	"math/big"
	"net"
	"strconv"
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
