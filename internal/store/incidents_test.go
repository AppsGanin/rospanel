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
	if ok, err := s.ResolveIncident(in.ID, 150); err != nil || !ok {
		t.Fatalf("resolve: %v %v", ok, err)
	}
	if ok, _ := s.ResolveIncident(in.ID, 160); ok {
		t.Error("an incident resolved twice")
	}
	if again, _ = s.GetIncident(in.ID); again.ResolvedAt != 150 {
		t.Errorf("resolved at %d, want 150", again.ResolvedAt)
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
