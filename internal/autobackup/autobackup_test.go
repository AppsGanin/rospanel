package autobackup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Shu1t3/rospanel-shu1t3/internal/backup"
	"github.com/Shu1t3/rospanel-shu1t3/internal/datasec"
	"github.com/Shu1t3/rospanel-shu1t3/internal/store"
)

type mockPanel struct {
	loc *time.Location
	mf  backup.Manifest
}

func (m *mockPanel) BackupManifest() backup.Manifest {
	return m.mf
}

func (m *mockPanel) Location() *time.Location {
	if m.loc != nil {
		return m.loc
	}
	return time.UTC
}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "autobackup-datasec")
	if err != nil {
		panic(err)
	}
	if err := datasec.Init(dir); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func TestAutobackupRunOnce(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "rospanel.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open failed: %v", err)
	}
	defer st.Close()

	panel := &mockPanel{
		loc: time.UTC,
		mf: backup.Manifest{
			Domain:     "vpn.example.com",
			SecretPath: "secret",
		},
	}

	svc := New(panel, st, dataDir)
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)

	// RunOnce keeping 2
	archivePath, err := svc.RunOnce(now, 2)
	if err != nil {
		t.Fatalf("RunOnce failed: %v", err)
	}
	if _, err := os.Stat(archivePath); err != nil {
		t.Errorf("archive file missing at %q: %v", archivePath, err)
	}

	// RunOnce again with different time
	now2 := now.Add(time.Hour)
	archivePath2, err := svc.RunOnce(now2, 2)
	if err != nil {
		t.Fatalf("RunOnce 2 failed: %v", err)
	}
	if _, err := os.Stat(archivePath2); err != nil {
		t.Errorf("archive file 2 missing at %q: %v", archivePath2, err)
	}
}

func TestAutobackupRunCancel(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "rospanel.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open failed: %v", err)
	}
	defer st.Close()

	panel := &mockPanel{loc: time.UTC}
	svc := New(panel, st, dataDir)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	done := make(chan struct{})
	go func() {
		svc.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
		// Passed
	case <-time.After(2 * time.Second):
		t.Fatal("svc.Run did not terminate on cancelled context")
	}
}
