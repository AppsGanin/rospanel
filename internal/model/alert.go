package model

import "math"

// Alert rules: an operator-defined threshold on a server's figure ("CPU above 90%
// for 10 minutes"). The panel evaluates them once a minute (core.sweepAlertRules),
// tells the admin chats and the webhooks when one starts and when it clears, and
// keeps every firing in alert_events.

// Metrics an alert rule can watch. Keys are stable: they are stored and sent over
// the API.
const (
	AlertMetricCPU      = "cpu_percent"
	AlertMetricMem      = "mem_percent"
	AlertMetricDisk     = "disk_percent"
	AlertMetricNetUp    = "net_up_mbps"
	AlertMetricNetDown  = "net_down_mbps"
	AlertMetricOnlineUs = "online_users"
)

// AlertMetricDef is one metric the rule form offers.
type AlertMetricDef struct {
	Key  string `json:"key"`
	Unit string `json:"unit"` // "%", "Mbit/s", "users"
}

// AlertMetricCatalog lists the metrics in display order.
var AlertMetricCatalog = []AlertMetricDef{
	{AlertMetricCPU, "%"},
	{AlertMetricMem, "%"},
	{AlertMetricDisk, "%"},
	{AlertMetricNetUp, "Mbit/s"},
	{AlertMetricNetDown, "Mbit/s"},
	{AlertMetricOnlineUs, "users"},
}

// AlertMetricUnit is the unit of a metric, "" for an unknown key.
func AlertMetricUnit(key string) string {
	for _, m := range AlertMetricCatalog {
		if m.Key == key {
			return m.Unit
		}
	}
	return ""
}

// Comparison of a rule: the figure above or below the threshold.
const (
	AlertOpAbove = "gt"
	AlertOpBelow = "lt"
)

// AlertEveryServer is the NodeID of a rule that watches every server; 0 is the
// master (LocalNodeID), a positive id one node.
const AlertEveryServer int64 = -1

// AlertEventRetentionDays is how long finished firings are kept.
const AlertEventRetentionDays = 30

// AlertRuleInput is what the operator sets on a rule.
type AlertRuleInput struct {
	Name      string  `json:"name"`
	Enabled   bool    `json:"enabled"`
	Metric    string  `json:"metric"`
	Op        string  `json:"op"` // AlertOpAbove | AlertOpBelow
	Threshold float64 `json:"threshold"`
	ForSecs   int     `json:"for_secs"` // how long the condition holds before it fires
	NodeID    int64   `json:"node_id"`  // AlertEveryServer, 0 (master) or a node id
}

// AlertRule is a stored rule.
type AlertRule struct {
	ID int64 `json:"id"`
	AlertRuleInput
	Firing    int   `json:"firing"` // open firings right now
	CreatedAt int64 `json:"created_at"`
}

// AlertEvent is one firing of a rule on one server; ResolvedAt 0 while it lasts.
// Name and condition are copied from the rule so the history survives its deletion.
type AlertEvent struct {
	ID         int64   `json:"id"`
	RuleID     int64   `json:"rule_id"`
	RuleName   string  `json:"rule_name"`
	NodeID     int64   `json:"node_id"`
	NodeName   string  `json:"node_name"`
	Metric     string  `json:"metric"`
	Op         string  `json:"op"`
	Threshold  float64 `json:"threshold"`
	Value      float64 `json:"value"` // the figure when it fired
	Peak       float64 `json:"peak"`  // the worst figure since
	FiredAt    int64   `json:"fired_at"`
	ResolvedAt int64   `json:"resolved_at"`
}

// AlertHolds reports whether the figure meets the rule's condition.
func AlertHolds(op string, value, threshold float64) bool {
	switch op {
	case AlertOpAbove:
		return value > threshold
	case AlertOpBelow:
		return value < threshold
	}
	return false
}

// AlertFinite is false for NaN and infinities, which a JSON number cannot be but a
// computed ratio can.
func AlertFinite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
