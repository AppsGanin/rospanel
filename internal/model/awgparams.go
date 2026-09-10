package model

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// AWGParams are a server's AmneziaWG obfuscation parameters as the store keeps
// them — the same fields as awg.Params, kept here so the model does not pull the
// tunnel engine into every package that reads a settings row. See internal/awg
// for what each one does and which protocol generation it belongs to.
//
// The ranges are strings in exactly the form both the engine's IPC and the client
// config file want ("110-130", or a bare "110" when the ends meet), so this type
// stores what it will emit and never has to reconstruct it. awg.Range parses
// either form, and a bare number is what every row written before 3.1 holds.
type AWGParams struct {
	Jc   int `json:"jc"`
	Jmin int `json:"jmin"`
	Jmax int `json:"jmax"`
	S1   int `json:"s1"`
	S2   int `json:"s2"`
	S3   int `json:"s3,omitempty"`
	S4   int `json:"s4,omitempty"`

	H1 string `json:"h1"`
	H2 string `json:"h2"`
	H3 string `json:"h3"`
	H4 string `json:"h4"`

	I1 string `json:"i1,omitempty"`
	I2 string `json:"i2,omitempty"`

	HeaderKey string `json:"header_key,omitempty"`
	Padding   string `json:"padding,omitempty"`
	Trailers  bool   `json:"trailers,omitempty"`

	RekeyAfter   string `json:"rekey_after,omitempty"`
	RekeyTimeout string `json:"rekey_timeout,omitempty"`
	RejectAfter  string `json:"reject_after,omitempty"`
	Keepalive    string `json:"keepalive,omitempty"`
	Handshakes   string `json:"handshakes,omitempty"`
}

// IsZero reports a parameter block that was never generated.
func (p AWGParams) IsZero() bool { return p == AWGParams{} }

// UnmarshalJSON accepts the two shapes a stored parameter block can have: the
// current one, where every range is a string, and the one every row written
// before AmneziaWG 3.1 holds, where h1–h4 were plain numbers. Reading the old
// shape is not a nicety — a failed unmarshal would leave the block zero, and a
// zero block reads as "this server never had a tunnel", which would silently mint
// new keys and invalidate every config already handed out.
func (p *AWGParams) UnmarshalJSON(b []byte) error {
	type stored AWGParams // no methods, so this does not recurse
	var raw struct {
		stored
		H1 json.RawMessage `json:"h1"`
		H2 json.RawMessage `json:"h2"`
		H3 json.RawMessage `json:"h3"`
		H4 json.RawMessage `json:"h4"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*p = AWGParams(raw.stored)
	for i, src := range []json.RawMessage{raw.H1, raw.H2, raw.H3, raw.H4} {
		v, err := headerString(src)
		if err != nil {
			return fmt.Errorf("awg params: h%d: %w", i+1, err)
		}
		switch i {
		case 0:
			p.H1 = v
		case 1:
			p.H2 = v
		case 2:
			p.H3 = v
		case 3:
			p.H4 = v
		}
	}
	return nil
}

// headerString reads one header field as either a quoted range or a bare number.
func headerString(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	var n uint32
	if err := json.Unmarshal(raw, &n); err != nil {
		return "", fmt.Errorf("neither a range nor a number: %s", raw)
	}
	return strconv.FormatUint(uint64(n), 10), nil
}
