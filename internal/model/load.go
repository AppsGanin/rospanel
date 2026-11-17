package model

// Load history per server (migration 0146): the charts behind a server card.

// LoadBucket is the width of one stored load row, in seconds.
const LoadBucket = 300

// LoadRetentionDays is how much load history is kept: the longest range the charts
// offer. Outages are kept as long as the status page's uptime (UptimeRetentionDays).
const LoadRetentionDays = 30

// LoadSample is one server's state at a watch tick.
type LoadSample struct {
	NodeID int64
	Up     bool
	// HasStats is whether CPU and Mem were reported. A node that is down, or one
	// that never sent host stats, has none.
	HasStats bool
	CPU      float64 // percent
	Mem      float64 // percent of RAM in use
	Disk     float64 // percent of the data disk in use
	// RX and TX are bytes per second in and out, averaged over the last minute.
	RX, TX float64
	// Online is how many distinct users the server saw inside the online window.
	// Counted whether or not the server reported stats: it is the panel's own view.
	Online int
}

// LoadPoint is one point of a chart: the stored rows of one step folded together.
type LoadPoint struct {
	TS     int64   `json:"t"`
	CPU    float64 `json:"cpu"`     // average, percent
	CPUMax float64 `json:"cpu_max"` // peak sample, percent
	Mem    float64 `json:"mem"`
	MemMax float64 `json:"mem_max"`
	// Disk and network are averaged over the samples that carried them (Ext of
	// them; Ext stays 0 on rows older than migration 0148), online over all.
	Disk      float64 `json:"disk"`
	DiskMax   float64 `json:"disk_max"`
	RX        float64 `json:"rx"`
	RXMax     float64 `json:"rx_max"`
	TX        float64 `json:"tx"`
	TXMax     float64 `json:"tx_max"`
	Online    float64 `json:"online"`
	OnlineMax int     `json:"online_max"`
	RXSum     float64 `json:"-"` // sums of the minute rates, for traffic totals
	TXSum     float64 `json:"-"`
	Ext       int     `json:"-"`
	ExtStats  int     `json:"-"` // samples with Ext figures that also carried stats
	Stats     int     `json:"-"`
	Up        int     `json:"-"`
	Total     int     `json:"-"`
}

// Outage is one stretch a server was down. EndedAt is 0 while it lasts.
type Outage struct {
	ID        int64 `json:"id"`
	NodeID    int64 `json:"node_id"`
	StartedAt int64 `json:"started_at"`
	EndedAt   int64 `json:"ended_at"`
}
