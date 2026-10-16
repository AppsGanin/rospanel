package store

import (
	"slices"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

func TestIncidentsCRUD(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	in := model.Incident{Kind: model.IncidentKindMaintenance, Title: "NL", Body: "works",
		NodeIDs: []int64{0, 2}, StartsAt: 100, EndsAt: 200, CreatedAt: 50, UpdatedAt: 50}
	if err := s.CreateIncident(&in); err != nil || in.ID == 0 {
		t.Fatalf("create: %v (id %d)", err, in.ID)
	}
	got, err := s.GetIncident(in.ID)
	if err != nil || got == nil {
		t.Fatalf("get: %v %v", got, err)
	}
	if got.Title != "NL" || got.Body != "works" || !slices.Equal(got.NodeIDs, []int64{0, 2}) ||
		got.StartsAt != 100 || got.EndsAt != 200 || got.CreatedAt != 50 {
		t.Errorf("read back %+v", got)
	}

	got.Title, got.NodeIDs, got.ResolvedAt = "NL again", nil, 999
	if ok, err := s.UpdateIncident(got); err != nil || !ok {
		t.Fatalf("update: %v %v", ok, err)
	}
	again, _ := s.GetIncident(in.ID)
	if again.Title != "NL again" || again.ResolvedAt != 0 || again.NodeIDs == nil || len(again.NodeIDs) != 0 {
		t.Errorf("after update %+v (an edit must not resolve)", again)
	}
	// Resolving is a one-way step of its own: the first call wins, a second changes
	// nothing.
	if ok, err := s.ResolveIncident(in.ID, 150, ""); err != nil || !ok {
		t.Fatalf("resolve: %v %v", ok, err)
	}
	if ok, _ := s.ResolveIncident(in.ID, 160, "late"); ok {
		t.Error("an incident resolved twice")
	}
	if again, _ = s.GetIncident(in.ID); again.ResolvedAt != 150 || len(again.Updates) != 0 {
		t.Errorf("resolved at %d, want 150; a refused resolve wrote its note: %+v", again.ResolvedAt, again.Updates)
	}
	// The closing note lands with the resolve, in the same step.
	other := model.Incident{Kind: model.IncidentKindIncident, Title: "y", StartsAt: 1, CreatedAt: 1, UpdatedAt: 1}
	if err := s.CreateIncident(&other); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ResolveIncident(other.ID, 300, "fixed"); err != nil || !ok {
		t.Fatalf("resolve with note: %v %v", ok, err)
	}
	if got, _ := s.GetIncident(other.ID); len(got.Updates) != 1 || got.Updates[0].Stage != model.IncidentStageResolved ||
		got.Updates[0].Body != "fixed" || got.Updates[0].CreatedAt != 300 {
		t.Errorf("closing note %+v", got.Updates)
	}

	if ok, err := s.DeleteIncident(in.ID); err != nil || !ok {
		t.Fatalf("delete: %v %v", ok, err)
	}
	if gone, _ := s.GetIncident(in.ID); gone != nil {
		t.Error("deleted incident still there")
	}
	if ok, _ := s.DeleteIncident(in.ID); ok {
		t.Error("an incident deleted twice")
	}
	if ok, _ := s.UpdateIncident(&model.Incident{ID: in.ID, Title: "x"}); ok {
		t.Error("an update of a deleted incident claimed success")
	}
}

// The page reads what is not over yet and what ended within its history window —
// nothing older, however it ended.
func TestIncidentsForPage(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	const now, since = 10_000, 5_000
	add := func(title, kind string, starts, ends, resolved int64) {
		t.Helper()
		in := model.Incident{Kind: kind, Title: title, StartsAt: starts, EndsAt: ends, ResolvedAt: resolved}
		if err := s.CreateIncident(&in); err != nil {
			t.Fatal(err)
		}
	}
	inc, mnt := model.IncidentKindIncident, model.IncidentKindMaintenance
	add("open", inc, 9_000, 0, 0)
	add("resolved lately", inc, 6_000, 0, 7_000)
	add("resolved long ago", inc, 1_000, 0, 2_000)
	add("planned", mnt, 20_000, 21_000, 0)
	add("under way", mnt, 9_500, 11_000, 0)
	add("open-ended", mnt, 9_000, 0, 0)
	add("window closed lately", mnt, 6_000, 8_000, 0)
	add("window closed long ago", mnt, 1_000, 2_000, 0)
	add("finished early lately", mnt, 6_000, 30_000, 8_000)

	list, err := s.IncidentsForPage(now, since)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, in := range list {
		titles = append(titles, in.Title)
	}
	slices.Sort(titles)
	want := []string{"finished early lately", "open", "open-ended", "planned", "resolved lately", "under way", "window closed lately"}
	if !slices.Equal(titles, want) {
		t.Errorf("page incidents = %v, want %v", titles, want)
	}
}

// A course is read back with its incident, in order, and goes with it.
func TestIncidentUpdates(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	in := model.Incident{Kind: model.IncidentKindIncident, Title: "x", StartsAt: 100, CreatedAt: 100, UpdatedAt: 100}
	if err := s.CreateIncident(&in); err != nil {
		t.Fatal(err)
	}
	for i, st := range []string{model.IncidentStageInvestigating, model.IncidentStageIdentified} {
		u := model.IncidentUpdate{IncidentID: in.ID, Stage: st, Body: st, CreatedAt: int64(200 + i)}
		if ok, err := s.AddIncidentUpdate(&u); err != nil || !ok || u.ID == 0 {
			t.Fatalf("add %s: %v %v", st, ok, err)
		}
	}
	if ok, err := s.AddIncidentUpdate(&model.IncidentUpdate{IncidentID: in.ID + 99, Stage: "update", Body: "x", CreatedAt: 1}); err != nil || ok {
		t.Errorf("an update for no incident: %v %v", ok, err)
	}
	got, _ := s.GetIncident(in.ID)
	if len(got.Updates) != 2 || got.Updates[0].Stage != model.IncidentStageInvestigating || got.UpdatedAt != 100 {
		t.Errorf("course %+v, updated_at %d", got.Updates, got.UpdatedAt)
	}
	list, _ := s.ListIncidents(10)
	if len(list) != 1 || len(list[0].Updates) != 2 {
		t.Errorf("list carries no course: %+v", list)
	}
	if incident, err := s.DeleteIncidentUpdate(got.Updates[0].ID); err != nil || incident != in.ID {
		t.Errorf("delete update: %d %v", incident, err)
	}
	if incident, _ := s.DeleteIncidentUpdate(got.Updates[0].ID); incident != 0 {
		t.Error("an update deleted twice")
	}
	if _, err := s.DeleteIncident(in.ID); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM incident_updates`).Scan(&left); err != nil || left != 0 {
		t.Errorf("updates outlived their incident: %d (%v)", left, err)
	}
}
