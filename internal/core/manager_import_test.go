package core

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/importer"
	"github.com/AppsGanin/rospanel/internal/model"
)

func marzbanFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "db.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, s := range []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY, username TEXT, status TEXT, used_traffic INTEGER, data_limit INTEGER, expire INTEGER, note TEXT)`,
		`CREATE TABLE proxies (id INTEGER PRIMARY KEY, user_id INTEGER, type TEXT, settings TEXT)`,
		`INSERT INTO users VALUES (1, 'alice', 'active', 500, 1073741824, 1900000000, 'vip'), (2, 'bob', 'disabled', 0, 0, 0, '')`,
		`INSERT INTO proxies VALUES (1, 1, 'VLESS', '{"id": "6bbd16cd-dfc1-47c4-9426-59b57b92b173"}'),
			(2, 1, 'Trojan', '{"password": "s3cret"}'), (3, 2, 'VLESS', '{"id": "1fa689ce-9afd-415e-b250-dcf755ff8b4e"}')`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// The whole point of an import: the same credentials on this side, the limits and
// usage carried over, a second run of the same file doubling nobody.
func TestImportKeepsCredentialsAndSkipsWhatIsHere(t *testing.T) {
	m := bulkTestManager(t)
	ctx := adminCtx()
	// "alice" already exists by name (not by UUID): allowed, flagged.
	if _, err := m.CreateUser(ctx, "alice", 0, 0); err != nil {
		t.Fatal(err)
	}

	preview, err := m.ImportPreview(marzbanFixture(t))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.Source != importer.SourceMarzban || len(preview.Users) != 2 {
		t.Fatalf("preview: %+v", preview)
	}
	if !preview.Users[0].NameTaken || preview.Users[0].Exists || preview.Users[1].NameTaken {
		t.Errorf("flags: %+v", preview.Users)
	}

	cands := make([]importer.Candidate, 0, 2)
	for _, u := range preview.Users {
		cands = append(cands, u.Candidate)
	}
	res, err := m.ImportUsers(ctx, ImportRequest{Source: "marzban", Users: cands, Tags: []string{"Import-Marzban"}})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Created != 2 || res.Skipped != 0 || len(res.Failed) != 0 {
		t.Fatalf("result: %+v", res)
	}
	users, _ := m.store.ListUsers()
	var alice, bob *model.User
	for i := range users {
		switch users[i].UUID {
		case "6bbd16cd-dfc1-47c4-9426-59b57b92b173":
			alice = &users[i]
		case "1fa689ce-9afd-415e-b250-dcf755ff8b4e":
			bob = &users[i]
		}
	}
	if alice == nil || bob == nil {
		t.Fatal("imported users not found by their original UUIDs")
	}
	if alice.Password != "s3cret" || alice.DataLimit != 1073741824 || alice.ExpireAt != 1900000000 ||
		alice.UsedDown != 500 || !alice.Enabled || alice.Note != "vip" || strings.Join(alice.Tags, ",") != "import-marzban" {
		t.Errorf("alice: %+v", alice)
	}
	if bob.Enabled || bob.SubToken == "" || bob.Password == "" {
		t.Errorf("bob: %+v", bob)
	}
	// The journal says where they came from.
	found := false
	for _, e := range trail(t, m, alice.ID) {
		if e.Action == model.EventUserCreated && strings.Contains(fmtDetails(e.Details), "marzban") {
			found = true
		}
	}
	if !found {
		t.Error("no user.created row naming the source")
	}

	// Same file again: preview marks both as here, import skips both.
	again, _ := m.ImportPreview(marzbanFixture(t))
	if !again.Users[0].Exists || !again.Users[1].Exists || again.Users[0].ExistingID != alice.ID {
		t.Errorf("second preview should flag both as existing: %+v", again.Users)
	}
	res, err = m.ImportUsers(ctx, ImportRequest{Source: "marzban", Users: cands})
	if err != nil || res.Created != 0 || res.Skipped != 2 {
		t.Errorf("second import: %+v %v", res, err)
	}
	if all, _ := m.store.ListUsers(); len(all) != 3 {
		t.Errorf("want 3 users (1 by hand + 2 imported), got %d", len(all))
	}
}

func TestImportReportsBadRowsAndKeepsTheRest(t *testing.T) {
	m := bulkTestManager(t)
	res, err := m.ImportUsers(adminCtx(), ImportRequest{Source: "3x-ui", Users: []importer.Candidate{
		{Name: "ok", UUID: "4372c397-2284-4cdd-9092-cfd0edc32092", Password: "p", Enabled: true},
		{Name: "bad-uuid", UUID: "not-a-uuid", Enabled: true},
		{Name: "", UUID: "b6e0a97f-cb56-42b3-ad5b-7e8eb3e872f0", Enabled: true},
		{Name: "bad-limit", UUID: "73ab3f5f-209b-4423-9fad-263e5baa37c4", DataLimit: -1, Enabled: true},
		// Same UUID as "ok", inside one batch: a skip, not a failure.
		{Name: "dup", UUID: "4372C397-2284-4CDD-9092-CFD0EDC32092", Enabled: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 1 || res.Skipped != 1 || len(res.Failed) != 3 {
		t.Fatalf("result: %+v", res)
	}
	codes := map[string]string{}
	for _, f := range res.Failed {
		codes[f.Name] = f.Code
	}
	if codes["bad-uuid"] != "err.importBadUUID" || codes[""] != "err.nameRequired" || codes["bad-limit"] != "err.badTrafficLimit" {
		t.Errorf("failure codes: %v", codes)
	}
	if _, err := m.ImportUsers(adminCtx(), ImportRequest{}); err == nil {
		t.Error("an empty import should be refused")
	}
	if _, err := m.ImportUsers(adminCtx(), ImportRequest{Users: []importer.Candidate{{Name: "x"}}, Tags: []string{"a,b"}}); err == nil {
		t.Error("a bad tag should be refused before anything is written")
	}
}

func fmtDetails(d any) string { return strings.ToLower(fmt.Sprint(d)) }

// The panel's own export round-trips: a user leaves with their credentials,
// subscription link, limits, usage and annotations, and comes back the same. A
// token another user already holds is the one thing that cannot be reused.
func TestExportImportRoundTrip(t *testing.T) {
	src := bulkTestManager(t)
	ctx := adminCtx()
	u, err := src.CreateUser(ctx, "traveller", 1<<30, 1900000000)
	if err != nil {
		t.Fatal(err)
	}
	if err := src.SetUserLimits(ctx, u.ID, 1<<30, 1900000000, 3); err != nil {
		t.Fatal(err)
	}
	if err := src.SetUserSpeedLimit(ctx, u.ID, 4096); err != nil {
		t.Fatal(err)
	}
	if err := src.SetResetPeriod(ctx, u.ID, "monthly"); err != nil {
		t.Fatal(err)
	}
	if err := src.SetUserNote(ctx, u.ID, "pays on the 5th"); err != nil {
		t.Fatal(err)
	}
	if err := src.SetUserTags(ctx, u.ID, []string{"vip"}); err != nil {
		t.Fatal(err)
	}
	if err := src.store.UpdateTraffic(u.ID, 111, 222, 111, 222); err != nil {
		t.Fatal(err)
	}
	before, _ := src.store.GetUser(u.ID)

	exp, err := src.ExportUsers()
	if err != nil {
		t.Fatal(err)
	}
	if exp.Format != importer.Format || exp.Version != importer.FormatVersion || len(exp.Users) != 1 {
		t.Fatalf("export: %+v", exp)
	}

	// Import it into a second, empty panel.
	dst := bulkTestManager(t)
	res, err := dst.ImportUsers(adminCtx(), ImportRequest{Source: string(importer.SourceRosPanel), Users: exp.Users})
	if err != nil || res.Created != 1 || len(res.Failed) != 0 {
		t.Fatalf("import: %+v %v", res, err)
	}
	list, _ := dst.store.ListUsers()
	after := list[0]
	if after.Name != before.Name || after.UUID != before.UUID || after.Password != before.Password ||
		after.SubToken != before.SubToken || after.DataLimit != before.DataLimit ||
		after.ExpireAt != before.ExpireAt || after.UsedUp != before.UsedUp || after.UsedDown != before.UsedDown ||
		after.DeviceLimit != before.DeviceLimit || after.SpeedLimit != before.SpeedLimit ||
		after.ResetPeriod != before.ResetPeriod || after.Note != before.Note ||
		strings.Join(after.Tags, ",") != strings.Join(before.Tags, ",") {
		t.Errorf("round trip changed the user:\n before %+v\n after  %+v", before, after)
	}

	// Importing the same file into the SAME panel: the UUID is already here.
	res, err = src.ImportUsers(adminCtx(), ImportRequest{Source: "rospanel", Users: exp.Users})
	if err != nil || res.Created != 0 || res.Skipped != 1 {
		t.Errorf("re-import into the source panel: %+v %v", res, err)
	}

	// A different user holding that subscription token: the import gets a fresh one
	// rather than failing on the unique index.
	third := bulkTestManager(t)
	other, _ := third.CreateUser(adminCtx(), "squatter", 0, 0)
	if err := third.store.SetSubToken(other.ID, before.SubToken); err != nil {
		t.Fatal(err)
	}
	res, err = third.ImportUsers(adminCtx(), ImportRequest{Source: "rospanel", Users: exp.Users})
	if err != nil || res.Created != 1 || len(res.Failed) != 0 {
		t.Fatalf("import against a taken token: %+v %v", res, err)
	}
	users, _ := third.store.ListUsers()
	for _, x := range users {
		if x.UUID == before.UUID && x.SubToken == before.SubToken {
			t.Error("two users ended up with the same subscription token")
		}
		if x.UUID == before.UUID && x.SubToken == "" {
			t.Error("the imported user got no subscription token at all")
		}
	}
}
