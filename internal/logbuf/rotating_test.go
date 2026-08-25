package logbuf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotatingFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "sub", "test.log")

	// 100 bytes max, 3 backups
	rf, err := NewRotatingFile(logPath, 100, 3)
	if err != nil {
		t.Fatalf("NewRotatingFile failed: %v", err)
	}
	defer rf.Close()

	// Write 60 bytes
	payload1 := strings.Repeat("A", 60)
	n, err := rf.Write([]byte(payload1))
	if err != nil || n != 60 {
		t.Fatalf("Write 1 failed: %d, %v", n, err)
	}

	// Write another 60 bytes (total 120 > 100) -> should trigger rotate
	payload2 := strings.Repeat("B", 60)
	n, err = rf.Write([]byte(payload2))
	if err != nil || n != 60 {
		t.Fatalf("Write 2 failed: %d, %v", n, err)
	}

	// test.log should have payload2 (60 bytes)
	curContent, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile active failed: %v", err)
	}
	if string(curContent) != payload2 {
		t.Errorf("active file = %q; want %q", string(curContent), payload2)
	}

	// test.log.1 should have payload1 (60 bytes)
	backup1Content, err := os.ReadFile(logPath + ".1")
	if err != nil {
		t.Fatalf("ReadFile backup 1 failed: %v", err)
	}
	if string(backup1Content) != payload1 {
		t.Errorf("backup.1 = %q; want %q", string(backup1Content), payload1)
	}

	// Trigger 3 more rotations
	for i := 0; i < 3; i++ {
		p := strings.Repeat("X", 105)
		_, err := rf.Write([]byte(p))
		if err != nil {
			t.Fatalf("write rotate loop %d failed: %v", i, err)
		}
	}

	// Check max backups: .1, .2, .3 exist; .4 must not exist
	if _, err := os.Stat(logPath + ".1"); err != nil {
		t.Errorf("backup.1 missing: %v", err)
	}
	if _, err := os.Stat(logPath + ".2"); err != nil {
		t.Errorf("backup.2 missing: %v", err)
	}
	if _, err := os.Stat(logPath + ".3"); err != nil {
		t.Errorf("backup.3 missing: %v", err)
	}
	if _, err := os.Stat(logPath + ".4"); err == nil {
		t.Errorf("backup.4 should not exist (maxBackups=3)")
	}
}
