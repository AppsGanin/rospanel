package core

import (
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/AppsGanin/rospanel/internal/geo"
	"github.com/AppsGanin/rospanel/internal/model"
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
	if m.geoLookup != nil && fi.ModTime().Equal(m.geoLookupMod) {
		return m.geoLookup
	}
	lk, err := geo.LoadCountryLookup(dir)
	if err != nil {
		logErr("geo map: country lookup build failed", "err", err)
		return m.geoLookup // keep any previous table rather than losing the feature
	}
	m.geoLookup = lk
	m.geoLookupMod = fi.ModTime()
	return lk
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
