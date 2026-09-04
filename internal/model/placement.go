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
	// TrafficLimit is how many bytes the server may carry in one TrafficPeriod;
	// 0 = no cap. It is measured against what the panel attributes to this server —
	// the users' traffic, not the interface counters — so it runs slightly under what
	// the hosting bills, which is the safe direction for a threshold.
	TrafficLimit int64 `json:"traffic_limit"`
	// TrafficPeriod is what the cap is measured over: TrafficMonth or TrafficDay.
	// Blank reads as TrafficMonth, the shape hosting actually sells.
	TrafficPeriod string `json:"traffic_period"`
	// HideWhenOver drops the server out of subscriptions once the cap is reached,
	// until the period rolls over — the same treatment HideWhenFull gives a server
	// that is out of user slots, and subject to the same rule that the subscription
	// is never emptied by it (see sub.Order).
	HideWhenOver bool `json:"hide_when_over"`
}

// Traffic-cap periods (Placement.TrafficPeriod).
const (
	TrafficMonth = "month" // since the 1st, in the operator timezone
	TrafficDay   = "day"   // since local midnight
)

// TrafficPeriodOr returns a valid cap period, defaulting to the month — blank is
// what every row written before the feature carries.
func TrafficPeriodOr(p string) string {
	if p == TrafficDay {
		return TrafficDay
	}
	return TrafficMonth
}

// ValidTrafficPeriod reports whether p is one the panel measures. Blank is valid:
// it means the default.
func ValidTrafficPeriod(p string) bool {
	return p == "" || p == TrafficMonth || p == TrafficDay
}

// TrafficCapped reports whether this placement states a cap at all.
func (p Placement) TrafficCapped() bool { return p.TrafficLimit > 0 }

// OverTrafficLimit reports whether used bytes have reached the cap. Uncapped servers
// are never over.
func (p Placement) OverTrafficLimit(used int64) bool {
	return p.TrafficLimit > 0 && used >= p.TrafficLimit
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
	// A petabyte a month is far past any hosting plan and far short of overflowing the
	// byte counter, so it catches a value typed in the wrong unit without ever refusing
	// a real one.
	if p.TrafficLimit < 0 || p.TrafficLimit > 1<<50 {
		return fieldErr("err.placementTrafficLimit", "лимит трафика: от 0 (не задан) до 1 ПБ", nil)
	}
	if !ValidTrafficPeriod(p.TrafficPeriod) {
		return fieldErr("err.placementTrafficPeriod", "период лимита: month или day", nil)
	}
	return nil
}

// Normalized is Validate's companion: the same value with the country in
// canonical form.
func (p Placement) Normalized() Placement {
	p.Country = NormalizeCountry(p.Country)
	// A negative cap would read as "over the limit" everywhere; an unrecognised period
	// would silently measure over the wrong window. Both collapse to the default.
	if p.TrafficLimit < 0 {
		p.TrafficLimit = 0
	}
	if !ValidTrafficPeriod(p.TrafficPeriod) {
		p.TrafficPeriod = ""
	}
	if p.TrafficLimit == 0 {
		// No cap means nothing to hide behind and no period to measure.
		p.TrafficPeriod, p.HideWhenOver = "", false
	}
	return p
}
