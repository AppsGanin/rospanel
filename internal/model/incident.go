package model

// Incident is an operator's notice on the public status page: something broke, or a
// planned maintenance window (migration 0127).
type Incident struct {
	ID    int64  `json:"id"`
	Kind  string `json:"kind"` // IncidentKind*
	Title string `json:"title"`
	Body  string `json:"body"`
	// NodeIDs are the servers it concerns (LocalNodeID is the panel's own); empty is
	// the whole service.
	NodeIDs []int64 `json:"node_ids"`
	// StartsAt is when an incident began, or when a maintenance window opens.
	StartsAt int64 `json:"starts_at"`
	// EndsAt is when a maintenance window closes, 0 when open-ended. An incident has
	// none: it is over when the operator says so.
	EndsAt     int64 `json:"ends_at"`
	ResolvedAt int64 `json:"resolved_at"`
	// HideInSub takes the servers a maintenance window names out of subscriptions
	// while it is under way (migration 0129). Never set on an incident.
	HideInSub bool `json:"hide_in_sub"`
	// CompensatedAt is when the users it hit were given time back for it, 0 when they
	// were not; CompensatedSeconds is how much each got, CompensatedUsers how many
	// (migration 0132).
	CompensatedAt      int64 `json:"compensated_at"`
	CompensatedSeconds int64 `json:"compensated_seconds"`
	CompensatedUsers   int   `json:"compensated_users"`
	// SnapshotAt is when the users it hits were recorded: those with a running term
	// as it began (migration 0134); 0 when it has not begun or predates the record.
	SnapshotAt int64 `json:"snapshot_at"`
	CreatedAt  int64 `json:"created_at"`
	UpdatedAt  int64 `json:"updated_at"`
	// Updates is its course so far, oldest first (migration 0128).
	Updates []IncidentUpdate `json:"updates"`
}

// IncidentUpdate is one step in an incident's course: what the operator learned or
// did, and when.
type IncidentUpdate struct {
	ID         int64  `json:"id"`
	IncidentID int64  `json:"incident_id"`
	Stage      string `json:"stage"` // IncidentStage*
	Body       string `json:"body"`
	CreatedAt  int64  `json:"created_at"`
}

// Stages of an incident's course. Maintenance takes only update and resolved.
const (
	IncidentStageInvestigating = "investigating"
	IncidentStageIdentified    = "identified"
	IncidentStageMonitoring    = "monitoring"
	IncidentStageUpdate        = "update"
	IncidentStageResolved      = "resolved" // the closing note, posted with the resolve
)

// ValidIncidentStage reports whether an update of the stage can be posted on an
// incident of the kind. resolved is not posted by hand: resolving writes it.
func ValidIncidentStage(kind, stage string) bool {
	switch stage {
	case IncidentStageUpdate:
		return true
	case IncidentStageInvestigating, IncidentStageIdentified, IncidentStageMonitoring:
		return kind == IncidentKindIncident
	}
	return false
}

const (
	IncidentKindIncident    = "incident"
	IncidentKindMaintenance = "maintenance"
)

// Where an incident stands at a given moment.
const (
	IncidentScheduled = "scheduled" // maintenance that has not started
	IncidentActive    = "active"
	IncidentDone      = "done"
)

// Limits on what the operator writes: a title is one line on the page, the text a
// short paragraph (and a Telegram message, which tops out at 4096).
const (
	IncidentTitleMax = 200
	IncidentBodyMax  = 2000
)

// ValidIncidentKind reports whether k is a known kind.
func ValidIncidentKind(k string) bool {
	return k == IncidentKindIncident || k == IncidentKindMaintenance
}

// Phase is where the incident stands at now (unix seconds). A maintenance window
// ends by itself at EndsAt; an incident only when resolved.
func (i *Incident) Phase(now int64) string {
	switch {
	case i.ResolvedAt > 0:
		return IncidentDone
	case i.Kind != IncidentKindMaintenance:
		return IncidentActive
	case now < i.StartsAt:
		return IncidentScheduled
	case i.EndsAt > 0 && now >= i.EndsAt:
		return IncidentDone
	}
	return IncidentActive
}

// EndedAt is when a finished incident ended: when it was resolved, or the close of
// its maintenance window. 0 while it is not over.
func (i *Incident) EndedAt(now int64) int64 {
	if i.Phase(now) != IncidentDone {
		return 0
	}
	if i.ResolvedAt > 0 && (i.EndsAt == 0 || i.ResolvedAt < i.EndsAt || i.Kind != IncidentKindMaintenance) {
		return i.ResolvedAt
	}
	return i.EndsAt
}

// Concerns reports whether the incident covers the server.
func (i *Incident) Concerns(nodeID int64) bool {
	if len(i.NodeIDs) == 0 {
		return true
	}
	for _, id := range i.NodeIDs {
		if id == nodeID {
			return true
		}
	}
	return false
}

// MonthReport is what the admin bot sums a month up with on the 1st.
type MonthReport struct {
	RevenueRub  int   // paid money, refunds by the payment system left out
	Payments    int   // orders paid with money (not from the balance)
	NewUsers    int   // accounts created in the month
	Users       int   // accounts now
	ActiveUsers int   // of them, active now
	Traffic     int64 // bytes, every server
	Incidents   int   // posted on the status page in the month
	Maintenance int
	Uptime      []MonthUptime // per server that was sampled
}

// MonthUptime is one server's samples over the month.
type MonthUptime struct {
	NodeID    int64
	Up, Total int
}
