package model

import "testing"

// Where an entry stands decides which section of the status page it lands in.
func TestIncidentPhase(t *testing.T) {
	t.Parallel()
	const now = 1000
	cases := []struct {
		name string
		in   Incident
		want string
		end  int64
	}{
		{"open incident", Incident{Kind: IncidentKindIncident, StartsAt: 900}, IncidentActive, 0},
		{"resolved incident", Incident{Kind: IncidentKindIncident, StartsAt: 900, ResolvedAt: 950}, IncidentDone, 950},
		{"maintenance ahead", Incident{Kind: IncidentKindMaintenance, StartsAt: 1100, EndsAt: 1200}, IncidentScheduled, 0},
		{"maintenance under way", Incident{Kind: IncidentKindMaintenance, StartsAt: 900, EndsAt: 1200}, IncidentActive, 0},
		{"maintenance with no end", Incident{Kind: IncidentKindMaintenance, StartsAt: 900}, IncidentActive, 0},
		{"maintenance past its window", Incident{Kind: IncidentKindMaintenance, StartsAt: 800, EndsAt: 900}, IncidentDone, 900},
		{"maintenance finished early", Incident{Kind: IncidentKindMaintenance, StartsAt: 800, EndsAt: 1200, ResolvedAt: 950}, IncidentDone, 950},
		// Marked finished after the window closed by itself: it ended at the close.
		{"maintenance resolved late", Incident{Kind: IncidentKindMaintenance, StartsAt: 800, EndsAt: 900, ResolvedAt: 990}, IncidentDone, 900},
		{"maintenance resolved before it began", Incident{Kind: IncidentKindMaintenance, StartsAt: 1100, EndsAt: 1200, ResolvedAt: 950}, IncidentDone, 950},
	}
	for _, c := range cases {
		if got := c.in.Phase(now); got != c.want {
			t.Errorf("%s: phase %q, want %q", c.name, got, c.want)
		}
		if got := c.in.EndedAt(now); got != c.end {
			t.Errorf("%s: ended at %d, want %d", c.name, got, c.end)
		}
	}
}

func TestIncidentConcerns(t *testing.T) {
	t.Parallel()
	all := Incident{}
	if !all.Concerns(LocalNodeID) || !all.Concerns(7) {
		t.Error("an incident naming no server must cover every server")
	}
	some := Incident{NodeIDs: []int64{LocalNodeID, 3}}
	if !some.Concerns(LocalNodeID) || !some.Concerns(3) || some.Concerns(4) {
		t.Errorf("Concerns does not follow the named servers: %+v", some.NodeIDs)
	}
}
