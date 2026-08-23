package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/AppsGanin/rospanel/internal/core"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/xray"
)

func TestLoginSemaphoreBlocksWhenSaturated(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "login.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	sup := xray.NewSupervisor("", filepath.Join(dir, "config.json"), dir)
	mgr := core.New(st, sup, xray.Options{}, core.TLSPaths{}, dir)

	sem := make(chan struct{}, 1)
	rt := &Router{
		mgr:     mgr,
		dataDir: dir,
		limiter: newLoginLimiter(),
		authSem: sem,
	}

	// Fill the semaphore
	sem <- struct{}{}

	// An incoming login request must be rejected immediately with 429
	req := httptest.NewRequest("POST", "/api/login", bytes.NewBufferString(`{"username":"admin","password":"password123"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	rt.login(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("login while sem full = %d, want %d", rec.Code, http.StatusTooManyRequests)
	}

	// Release semaphore
	<-sem

	// Now it should pass the semaphore check (and fail with bad credentials)
	req2 := httptest.NewRequest("POST", "/api/login", bytes.NewBufferString(`{"username":"admin","password":"password123"}`))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	rt.login(rec2, req2)

	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("login after sem released = %d, want %d", rec2.Code, http.StatusUnauthorized)
	}
}
