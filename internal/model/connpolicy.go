package model

import (
	"fmt"
	"sort"
	"strings"
)

// ConnPolicy decides which source addresses may use the tunnels: where a client is
// allowed to connect FROM. It exists for one problem — an account sold on and used
// somewhere the operator never sells to — and it answers it with the fact the panel
// already knows about every connection: the country the address is in.
//
// It is not an authentication mechanism. Geo data is approximate, a VPN in front of
// the VPN defeats it, and a determined reseller can rent an address in the right
// country. It raises the cost of resale; it does not make it impossible.
type ConnPolicy struct {
	// Mode is what the country list means: "off" (no country rule at all), "allow"
	// (only these countries may connect) or "block" (these may not).
	Mode string `json:"mode"`
	// Countries are ISO 3166-1 alpha-2 codes, upper case.
	Countries []string `json:"countries"`
	// Enforce drops the offending address at the firewall. Off, a violation is only
	// recorded — which is how an operator sees what the rule WOULD have cut before
	// letting it cut anything.
	Enforce bool `json:"enforce"`
	// BlockHours is how long a dropped address stays dropped (0 = the blocker's
	// default). It self-expires in the kernel, so a lapsed block simply returns.
	BlockHours int `json:"block_hours"`
}

// Policy modes.
const (
	ConnPolicyOff   = "off"
	ConnPolicyAllow = "allow"
	ConnPolicyBlock = "block"
)

// MaxPolicyCountries bounds the list. Generous for the job (a seller covers a
// handful of countries) and small enough that the check stays a scan over a few
// entries on the connection path.
const MaxPolicyCountries = 64

// DefaultConnPolicy is the feature switched off.
func DefaultConnPolicy() ConnPolicy {
	return ConnPolicy{Mode: ConnPolicyOff, Countries: []string{}, BlockHours: 24}
}

// Active reports whether the policy can refuse anything at all.
func (p ConnPolicy) Active() bool {
	return (p.Mode == ConnPolicyAllow || p.Mode == ConnPolicyBlock) && len(p.Countries) > 0
}

// Normalized returns the policy in canonical form: countries upper-cased, deduped
// and sorted, an unknown mode read as off.
func (p ConnPolicy) Normalized() ConnPolicy {
	out := ConnPolicy{Mode: p.Mode, Enforce: p.Enforce, BlockHours: p.BlockHours}
	switch out.Mode {
	case ConnPolicyAllow, ConnPolicyBlock:
	default:
		out.Mode = ConnPolicyOff
	}
	seenCC := map[string]struct{}{}
	out.Countries = []string{}
	for _, c := range p.Countries {
		cc := NormalizeCountry(c)
		if cc == "" {
			continue
		}
		if _, dup := seenCC[cc]; dup {
			continue
		}
		seenCC[cc] = struct{}{}
		out.Countries = append(out.Countries, cc)
	}
	sort.Strings(out.Countries)
	return out
}

// Validate refuses what the operator cannot have meant: a malformed country, an
// allow-list with nothing in it (which would refuse every connection on the panel),
// or lists past the bounds above.
func (p ConnPolicy) Validate() error {
	for _, c := range p.Countries {
		if NormalizeCountry(c) == "" {
			return fieldErr("err.policyCountry", "страна «{{value}}»: две латинские буквы (RU, KZ)",
				map[string]any{"value": strings.TrimSpace(c)})
		}
	}
	n := p.Normalized()
	if n.Mode == ConnPolicyAllow && len(n.Countries) == 0 {
		return fieldErr("err.policyAllowEmpty", "режим «только эти страны» без единой страны отрезал бы всех", nil)
	}
	if len(n.Countries) > MaxPolicyCountries {
		return fieldErr("err.policyTooManyCountries", "стран не больше {{max}}", map[string]any{"max": MaxPolicyCountries})
	}
	if p.BlockHours < 0 || p.BlockHours > 24*365 {
		return fieldErr("err.policyBlockHours", "срок блокировки: от 0 (по умолчанию) до 8760 часов", nil)
	}
	return nil
}

// Policy verdict reasons, as stored on a block and rendered by the panel. The
// network reason is no longer produced — the panel dropped the ASN list — but blocks
// recorded under it are still on file, so the constant stays to name them.
const (
	PolicyReasonCountry = "country" // the address is somewhere the policy does not serve
	PolicyReasonASN     = "asn"     // the address belonged to a refused network
)

// Verdict is what the policy says about one address.
type Verdict struct {
	Refused bool
	Reason  string // PolicyReason*, empty when allowed
	Country string
	ASN     uint32
	Org     string
}

// Decide applies the policy to one connection. country is the ISO-2 code the geo
// table gave (empty when it knows nothing about the address) and asn/org the
// network it belongs to.
//
// An address the geo table cannot place is NEVER refused, even under an allow-list.
// The table is incomplete and lags reality by weeks, so treating "unknown" as
// "somewhere else" would cut real users off a working service — the failure this
// feature must not have.
func (p ConnPolicy) Decide(country string, asn uint32, org string) Verdict {
	// asn/org are carried into the verdict for the record — which network a refused
	// address was on is worth having in the audit — but they decide nothing.
	v := Verdict{Country: country, ASN: asn, Org: org}
	if country == "" || len(p.Countries) == 0 {
		return v
	}
	listed := false
	for _, c := range p.Countries {
		if c == country {
			listed = true
			break
		}
	}
	switch p.Mode {
	case ConnPolicyAllow:
		v.Refused = !listed
	case ConnPolicyBlock:
		v.Refused = listed
	}
	if v.Refused {
		v.Reason = PolicyReasonCountry
	}
	return v
}

// String is the operator-facing summary of a verdict, for a log line.
func (v Verdict) String() string {
	switch v.Reason {
	case PolicyReasonCountry:
		return fmt.Sprintf("country %s", v.Country)
	case PolicyReasonASN:
		return fmt.Sprintf("AS%d %s", v.ASN, v.Org)
	}
	return "allowed"
}

// BlockedIP is one address the policy refused, as the panel records it.
type BlockedIP struct {
	IP      string `json:"ip"`
	Reason  string `json:"reason"` // PolicyReason*
	Country string `json:"country"`
	ASN     uint32 `json:"asn"`
	Org     string `json:"org"`
	UserID  int64  `json:"user_id"` // who was connecting, 0 when unknown
	At      int64  `json:"at"`
	Until   int64  `json:"until"`
}
