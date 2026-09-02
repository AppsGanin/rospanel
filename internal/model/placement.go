package model

import (
	"regexp"
	"strings"
)

// Placement is what decides where a server lands in a subscription and whether
// it appears at all: the country it is in, a manual weight, and how many users it
// is meant to carry. Every server has one — the master's lives in the settings row,
// a node's on its own row — and the subscription orders servers by it together
// with the client's own country and each server's live load (see sub.Order).
type Placement struct {
	// Country is the ISO 3166-1 alpha-2 code of the server's location ("NL"), upper
	// case. Blank means unknown: the server is never "nearest" to anyone, and sorts
	// after those that are.
	Country string `json:"country"`
	// Weight is the operator's manual priority: higher sorts first among servers
	// that are otherwise equal. 0 is the default.
	Weight int `json:"sort_weight"`
	// Capacity is how many users the server is meant to carry at once; 0 = no
	// stated capacity. Load is measured against it, and with HideWhenFull a server
	// at or over it drops out of the subscription until it has room again.
	Capacity     int  `json:"capacity"`
	HideWhenFull bool `json:"hide_when_full"`
}

// Subscription server ordering modes (Settings → Subscriptions → sub_order_mode).
const (
	OrderManual      = "manual"       // the operator's order: weight, then the list
	OrderNearest     = "nearest"      // the client's country first, then manual
	OrderLoad        = "load"         // least loaded first, then manual
	OrderNearestLoad = "nearest_load" // the client's country first, least loaded within
)

var orderModes = map[string]bool{
	OrderManual: true, OrderNearest: true, OrderLoad: true, OrderNearestLoad: true,
}

// OrderModeOr returns a valid ordering mode, falling back to manual for blank or
// unknown values — a settings row written by an older build has none.
func OrderModeOr(mode string) string {
	if orderModes[mode] {
		return mode
	}
	return OrderManual
}

// ValidOrderMode reports whether mode is one of the four.
func ValidOrderMode(mode string) bool { return orderModes[mode] }

var countryCodeRe = regexp.MustCompile(`^[A-Z]{2}$`)

// NormalizeCountry upper-cases and trims a country code; anything that is not two
// letters becomes blank rather than an error, since the value is a hint the
// ordering degrades gracefully without.
func NormalizeCountry(cc string) string {
	cc = strings.ToUpper(strings.TrimSpace(cc))
	if !countryCodeRe.MatchString(cc) {
		return ""
	}
	return cc
}

// Validate refuses what the ordering cannot use: a malformed country (two letters
// or nothing), a negative capacity or an absurd weight.
func (p Placement) Validate() error {
	if cc := strings.TrimSpace(p.Country); cc != "" && NormalizeCountry(cc) == "" {
		return fieldErr("err.placementCountry", "страна: две латинские буквы (NL, DE) или пусто", nil)
	}
	if p.Capacity < 0 || p.Capacity > 1_000_000 {
		return fieldErr("err.placementCapacity", "вместимость: от 0 (не задана) до 1 000 000", nil)
	}
	if p.Weight < -1000 || p.Weight > 1000 {
		return fieldErr("err.placementWeight", "вес: от -1000 до 1000", nil)
	}
	return nil
}

// Normalized is Validate's companion: the same value with the country in
// canonical form.
func (p Placement) Normalized() Placement {
	p.Country = NormalizeCountry(p.Country)
	return p
}
