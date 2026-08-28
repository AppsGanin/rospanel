package core

import (
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Shu1t3/rospanel-shu1t3/internal/geo"
	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
)

// countryLookup returns an IP→country resolver built from geoip.dat, cached and
// rebuilt only when the file changes (a geo refresh downloads a new one). Returns nil
// when no database is present — callers treat that as "no country data", not an error.
func (m *Manager) countryLookup() *geo.CountryLookup {
	dir := m.assetDir()
	if dir == "" {
		return nil
	}
	path := filepath.Join(dir, "geoip.dat")
	fi, err := os.Stat(path)
	if err != nil {
		return nil
	}

	m.geoLookupMu.Lock()
	defer m.geoLookupMu.Unlock()
	if fi.ModTime().Equal(m.geoLookupMod) {
		return m.geoLookup // this file version already processed (table on success, unchanged on failure)
	}
	lk, err := geo.LoadCountryLookup(dir)
	m.geoLookupMod = fi.ModTime() // mark processed either way, so a corrupt file isn't re-parsed every call
	if err != nil {
		logErr("geo map: country lookup build failed", "err", err)
		return m.geoLookup // keep any previous table rather than losing the feature
	}
	m.geoLookup = lk
	return lk
}

// asnLookup returns an IP→ASN resolver built from ip2asn.tsv.gz, cached and rebuilt
// only when the file changes. Returns nil when the table isn't downloaded yet.
func (m *Manager) asnLookup() *geo.ASNLookup {
	dir := m.assetDir()
	if dir == "" {
		return nil
	}
	path := filepath.Join(dir, "ip2asn.tsv.gz")
	fi, err := os.Stat(path)
	if err != nil {
		return nil
	}
	m.asnLookupMu.Lock()
	defer m.asnLookupMu.Unlock()
	if fi.ModTime().Equal(m.asnTableMod) {
		return m.asnTable // this file version already processed (table on success, unchanged on failure)
	}
	lk, err := geo.LoadASNLookup(dir)
	m.asnTableMod = fi.ModTime() // mark processed either way, so a corrupt file isn't re-parsed every call
	if err != nil {
		logErr("geo map: ASN lookup build failed", "err", err)
		return m.asnTable
	}
	m.asnTable = lk
	return lk
}

// ConnectionASNs returns the breakdown of recent client connections by network
// operator (ASN): per ASN, how many distinct source IPs connected and how active they
// were. IPs no ASN range covers fall into the 0/"" bucket. Sorted by distinct IPs.
func (m *Manager) ConnectionASNs() ([]model.ASNStat, error) {
	lookup := m.asnLookup()
	since := time.Now().AddDate(0, 0, -model.ConnectionRetentionDays).Unix()
	stats, err := m.store.ConnectionIPStats(since)
	if err != nil {
		return nil, err
	}
	agg := make(map[uint32]*model.ASNStat)
	for _, s := range stats {
		var asn uint32
		var org string
		if lookup != nil {
			if addr, err := netip.ParseAddr(s.IP); err == nil {
				asn, org, _ = lookup.Lookup(addr)
			}
		}
		row := agg[asn]
		if row == nil {
			row = &model.ASNStat{ASN: asn, Org: org}
			agg[asn] = row
		}
		row.IPs++
		row.Hits += s.Hits
	}
	out := make([]model.ASNStat, 0, len(agg))
	for _, row := range agg {
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IPs != out[j].IPs {
			return out[i].IPs > out[j].IPs
		}
		return out[i].ASN < out[j].ASN
	})
	return out, nil
}

// ConnectionCountries returns the geo breakdown of recent client connections: per
// country, how many distinct source IPs connected and how active they were. IPs no
// country range covers (private/unknown) fall into the "" bucket. Sorted by distinct
// IPs, busiest first.
func (m *Manager) ConnectionCountries() ([]model.CountryStat, error) {
	lookup := m.countryLookup()
	since := time.Now().AddDate(0, 0, -model.ConnectionRetentionDays).Unix()
	stats, err := m.store.ConnectionIPStats(since)
	if err != nil {
		return nil, err
	}

	agg := make(map[string]*model.CountryStat)
	for _, s := range stats {
		code := ""
		if lookup != nil {
			addr, err := netip.ParseAddr(s.IP)
			if err == nil {
				if cc, ok := lookup.Lookup(addr); ok {
					code = cc
				}
			}
		}
		row := agg[code]
		if row == nil {
			row = &model.CountryStat{Code: code}
			agg[code] = row
		}
		row.IPs++
		row.Hits += s.Hits
	}

	out := make([]model.CountryStat, 0, len(agg))
	for _, row := range agg {
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IPs != out[j].IPs {
			return out[i].IPs > out[j].IPs
		}
		return out[i].Code < out[j].Code
	})
	return out, nil
}

// annotateProbes fills in the country and network operator of each scanning address,
// in place.
//
// A list of bare addresses is a list of numbers: the operator cannot tell a research
// scanner in a datacentre from a residential range in the country they actually serve,
// which is the difference between "ignore" and "this is aimed at me". Both tables are
// already loaded for the connection breakdowns, so this costs a lookup per row.
//
// Missing tables are not an error. Every field here is optional and the list is still
// worth showing without them — a panel that has not finished its first geo download
// must not answer with nothing.
func (m *Manager) annotateProbes(probes []model.ProbeHit) {
	if len(probes) == 0 {
		return
	}
	countries, asns := m.countryLookup(), m.asnLookup()
	if countries == nil && asns == nil {
		return
	}
	for i := range probes {
		addr, err := netip.ParseAddr(probes[i].IP)
		if err != nil {
			continue
		}
		if countries != nil {
			if cc, ok := countries.Lookup(addr); ok {
				// The table answers in lower case; ISO 3166-1 alpha-2 is written in
				// upper. Normalised here so the digest, the panel and the JSON all
				// carry the same spelling.
				probes[i].Country = strings.ToUpper(cc)
			}
		}
		if asns != nil {
			probes[i].ASN, probes[i].Org, _ = asns.Lookup(addr)
		}
	}
}

