package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/importer"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// The export is an admin-level download of every credential, it comes back as a
// file the import reads, and it leaves a row in the panel log.
func TestExportUsersEndpoint(t *testing.T) {
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()
	u, err := rt.mgr.CreateUser(t.Context(), "exported", 1024, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.mgr.SetUserTags(t.Context(), u.ID, []string{"vip"}); err != nil {
		t.Fatal(err)
	}

	// An operator may create users but not download every credential at once.
	operator := signIn(t, st, "support", model.RoleOperator, false)
	if code := call(h, http.MethodGet, "/api/users/export", operator); code != http.StatusForbidden {
		t.Errorf("operator export: %d, want 403", code)
	}
	if code := call(h, http.MethodGet, "/api/users/export", nil); code != http.StatusUnauthorized {
		t.Errorf("anonymous export: %d, want 401", code)
	}

	admin := signIn(t, st, "admin", model.RoleAdmin, false)
	req := httptest.NewRequest(http.MethodGet, "/api/users/export", nil)
	req.AddCookie(admin)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("export: %d %s", rec.Code, rec.Body.String())
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "rospanel-users-") || !strings.HasSuffix(cd, `.json"`) {
		t.Errorf("content-disposition: %q", cd)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("the file carries credentials and must not be cached: %q", cc)
	}
	var file importer.Export
	if err := json.Unmarshal(rec.Body.Bytes(), &file); err != nil {
		t.Fatalf("decode: %v\n%s", err, rec.Body.String())
	}
	if file.Format != importer.Format || file.Version != importer.FormatVersion || file.ExportedAt == 0 {
		t.Errorf("header: %+v", file)
	}
	if len(file.Users) != 1 {
		t.Fatalf("want the one user, got %d", len(file.Users))
	}
	fresh, _ := st.GetUser(u.ID)
	c := file.Users[0]
	if c.Name != "exported" || c.UUID != fresh.UUID || c.Password != fresh.Password ||
		c.SubToken != fresh.SubToken || c.DataLimit != 1024 || strings.Join(c.Tags, ",") != "vip" {
		t.Errorf("exported user: %+v", c)
	}

	rows, err := st.ListAdminAudit(store.AdminAuditFilter{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	logged := false
	for _, r := range rows {
		if r.Action == model.AuditUsersExported && r.ActorName == "admin" {
			logged = true
		}
	}
	if !logged {
		t.Error("the export left no row in the panel log")
	}
}
