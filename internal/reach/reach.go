// Package reach asks Globalping (globalping.io) whether a server answers from inside
// given countries — the one thing the panel cannot see from where it stands. Its own
// checks run from the server or from the panel, and a server blocked in the country
// its users live in still looks healthy from both.
//
// A check is a TCP ping to the server's main port from probes in each country. A
// probe that completed the handshake counts as reachable, one that sent its packets
// and got nothing back as unreachable. A probe that failed on its own side (offline,
// timed out before it ran) says nothing about the server and is counted apart, so a
// flaky probe network does not read as a block.
package reach

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// API is the Globalping endpoint.
const API = "https://api.globalping.io"

// MaxProbes is how many probes one measurement may ask for without a token.
const MaxProbes = 50

// ErrRateLimited is the answer to a measurement over the hourly limit (250 tests an
// hour per address without a token, 500 with one).
var ErrRateLimited = errors.New("globalping: rate limit reached")

// pollEvery is how often a running measurement is read (the API allows 2 a second).
var pollEvery = 2 * time.Second

const (
	pollTimeout = 75 * time.Second // the probe-side timeout is up to 30 s, plus finalisation
	maxBody     = 4 << 20
)

// Probe states.
const (
	StateOK      = "ok"      // the TCP handshake completed
	StateBlocked = "blocked" // packets sent, nothing came back
	StateFailed  = "failed"  // the probe itself failed; says nothing about the server
)

// Probe is one probe's outcome.
type Probe struct {
	Country  string  `json:"country"`
	City     string  `json:"city"`
	Network  string  `json:"network"`
	ASN      int     `json:"asn"`
	State    string  `json:"state"`
	RTT      float64 `json:"rtt_ms,omitempty"`
	Resolved string  `json:"resolved,omitempty"` // the address the probe resolved the host to
}

// Country sums the probes of one country.
type Country struct {
	Code    string  `json:"code"`
	OK      int     `json:"ok"`
	Blocked int     `json:"blocked"`
	Failed  int     `json:"failed"`
	Probes  []Probe `json:"probes"`
}

// Answered is how many probes said something about the server.
func (c Country) Answered() int { return c.OK + c.Blocked }

// Percent is the share of answering probes that reached the server, or -1 when none
// answered.
func (c Country) Percent() int {
	if c.Answered() == 0 {
		return -1
	}
	return c.OK * 100 / c.Answered()
}

// Client talks to the Globalping API.
type Client struct {
	Base  string // API when empty
	Token string // optional: raises the hourly limit
	HTTP  *http.Client
}

func (c *Client) base() string {
	if c.Base != "" {
		return strings.TrimRight(c.Base, "/")
	}
	return API
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

type location struct {
	Country string `json:"country"`
	Limit   int    `json:"limit"`
}

type measureReq struct {
	Type      string     `json:"type"`
	Target    string     `json:"target"`
	Locations []location `json:"locations"`
	Options   struct {
		Protocol string `json:"protocol"`
		Port     int    `json:"port"`
		Packets  int    `json:"packets"`
	} `json:"measurementOptions"`
}

type measureResp struct {
	Status  string `json:"status"`
	Results []struct {
		Probe struct {
			Country string `json:"country"`
			City    string `json:"city"`
			Network string `json:"network"`
			ASN     int    `json:"asn"`
		} `json:"probe"`
		Result struct {
			Status          string `json:"status"`
			ResolvedAddress string `json:"resolvedAddress"`
			Stats           *struct {
				Avg *float64 `json:"avg"`
				Rcv int      `json:"rcv"`
			} `json:"stats"`
		} `json:"result"`
	} `json:"results"`
}

// apiError is Globalping's error body.
type apiError struct {
	Error struct {
		Type    string            `json:"type"`
		Message string            `json:"message"`
		Params  map[string]string `json:"params"`
	} `json:"error"`
}

// TCP pings host:port from perCountry probes in each of countries (ISO codes) and
// returns the outcome per country, in the order asked.
func (c *Client) TCP(ctx context.Context, host string, port int, countries []string, perCountry int) ([]Country, error) {
	if host == "" || port <= 0 || len(countries) == 0 || perCountry <= 0 {
		return nil, errors.New("globalping: nothing to measure")
	}
	req := measureReq{Type: "ping", Target: host}
	for _, cc := range countries {
		req.Locations = append(req.Locations, location{Country: strings.ToUpper(cc), Limit: perCountry})
	}
	req.Options.Protocol, req.Options.Port, req.Options.Packets = "TCP", port, 2
	id, err := c.create(ctx, req)
	if err != nil {
		return nil, err
	}
	res, err := c.wait(ctx, id)
	if err != nil {
		return nil, err
	}
	return tally(countries, res), nil
}

func (c *Client) create(ctx context.Context, body measureReq) (string, error) {
	b, _ := json.Marshal(body)
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+"/v1/measurements", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	r.Header.Set("Content-Type", "application/json")
	data, status, err := c.do(r)
	if err != nil {
		return "", err
	}
	if status == http.StatusTooManyRequests {
		return "", ErrRateLimited
	}
	if status != http.StatusAccepted && status != http.StatusOK {
		return "", apiErr(status, data)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &out); err != nil || out.ID == "" {
		return "", fmt.Errorf("globalping: no measurement id (HTTP %d)", status)
	}
	return out.ID, nil
}

func (c *Client) wait(ctx context.Context, id string) (*measureResp, error) {
	ctx, cancel := context.WithTimeout(ctx, pollTimeout)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return nil, errors.New("globalping: no result in time")
		case <-time.After(pollEvery):
		}
		r, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base()+"/v1/measurements/"+id, nil)
		if err != nil {
			return nil, err
		}
		data, status, err := c.do(r)
		if err != nil {
			continue // a dropped poll is retried; the deadline bounds it
		}
		if status != http.StatusOK {
			if status == http.StatusTooManyRequests {
				continue
			}
			return nil, apiErr(status, data)
		}
		var m measureResp
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("globalping: bad result: %w", err)
		}
		if m.Status != "in-progress" {
			return &m, nil
		}
	}
}

func (c *Client) do(r *http.Request) ([]byte, int, error) {
	r.Header.Set("Accept", "application/json")
	r.Header.Set("User-Agent", "rospanel-reach")
	if c.Token != "" {
		r.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.http().Do(r)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	return data, resp.StatusCode, err
}

func apiErr(status int, data []byte) error {
	var e apiError
	if json.Unmarshal(data, &e) == nil && e.Error.Message != "" {
		msg := e.Error.Message
		for k, v := range e.Error.Params {
			msg += "; " + k + ": " + v
		}
		return fmt.Errorf("globalping: %s", msg)
	}
	return fmt.Errorf("globalping: HTTP %d", status)
}

// tally sorts the probes into their countries, keeping the order asked for.
func tally(countries []string, m *measureResp) []Country {
	out := make([]Country, len(countries))
	at := make(map[string]int, len(countries))
	for i, cc := range countries {
		cc = strings.ToUpper(cc)
		out[i] = Country{Code: cc, Probes: []Probe{}}
		at[cc] = i
	}
	for _, r := range m.Results {
		p := Probe{
			Country:  strings.ToUpper(r.Probe.Country),
			City:     r.Probe.City,
			Network:  r.Probe.Network,
			ASN:      r.Probe.ASN,
			Resolved: r.Result.ResolvedAddress,
		}
		switch {
		case r.Result.Status == "finished" && r.Result.Stats != nil && r.Result.Stats.Rcv > 0:
			p.State = StateOK
			if r.Result.Stats.Avg != nil {
				p.RTT = *r.Result.Stats.Avg
			}
		case r.Result.Status == "finished" && r.Result.Stats != nil:
			p.State = StateBlocked
		default:
			p.State = StateFailed
		}
		i, ok := at[p.Country]
		if !ok {
			continue // a probe outside the countries asked for: not ours to count
		}
		c := &out[i]
		switch p.State {
		case StateOK:
			c.OK++
		case StateBlocked:
			c.Blocked++
		default:
			c.Failed++
		}
		c.Probes = append(c.Probes, p)
	}
	return out
}
