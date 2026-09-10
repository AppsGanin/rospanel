package awg

import (
	"fmt"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The store keeps a parameter block as model.AWGParams — strings and ints, no
// engine types — and this file is the only place the two forms meet. It used to be
// two hand-written field-by-field copies in the manager, which is a shape that
// rots: a field added to one side and forgotten on the other compiles fine and
// silently drops that parameter out of every config. TestParamsSurviveTheModelRoundTrip
// fills every field and fails if one does not come back.

// FromModel parses a stored parameter block. A malformed range is an error rather
// than a zero value: a zero block reads as "this server never had a tunnel", which
// would mint new keys over the top of working ones.
func FromModel(m model.AWGParams) (Params, error) {
	p := Params{
		Jc:        m.Jc,
		Jmin:      m.Jmin,
		Jmax:      m.Jmax,
		S1:        m.S1,
		S2:        m.S2,
		S3:        m.S3,
		S4:        m.S4,
		I1:        m.I1,
		I2:        m.I2,
		Imitation: m.Imitation,
		HeaderKey: m.HeaderKey,
		Trailers:  m.Trailers,
	}
	for _, f := range []struct {
		name string
		src  string
		dst  *Range
	}{
		{"h1", m.H1, &p.H1},
		{"h2", m.H2, &p.H2},
		{"h3", m.H3, &p.H3},
		{"h4", m.H4, &p.H4},
		{"padding", m.Padding, &p.Padding},
		{"rekey_after", m.RekeyAfter, &p.RekeyAfter},
		{"rekey_timeout", m.RekeyTimeout, &p.RekeyTimeout},
		{"reject_after", m.RejectAfter, &p.RejectAfter},
		{"keepalive", m.Keepalive, &p.Keepalive},
		{"handshakes", m.Handshakes, &p.Handshakes},
	} {
		if err := f.dst.parse(f.src); err != nil {
			return Params{}, fmt.Errorf("awg: %s: %w", f.name, err)
		}
	}
	return p, nil
}

// ToModel renders a parameter block for the store.
func ToModel(p Params) model.AWGParams {
	return model.AWGParams{
		Jc:           p.Jc,
		Jmin:         p.Jmin,
		Jmax:         p.Jmax,
		S1:           p.S1,
		S2:           p.S2,
		S3:           p.S3,
		S4:           p.S4,
		H1:           p.H1.String(),
		H2:           p.H2.String(),
		H3:           p.H3.String(),
		H4:           p.H4.String(),
		I1:           p.I1,
		I2:           p.I2,
		Imitation:    p.Imitation,
		HeaderKey:    p.HeaderKey,
		Padding:      p.Padding.String(),
		Trailers:     p.Trailers,
		RekeyAfter:   p.RekeyAfter.String(),
		RekeyTimeout: p.RekeyTimeout.String(),
		RejectAfter:  p.RejectAfter.String(),
		Keepalive:    p.Keepalive.String(),
		Handshakes:   p.Handshakes.String(),
	}
}
