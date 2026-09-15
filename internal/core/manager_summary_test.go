package core

import (
	"sync"
	"testing"
	"time"
)

// The dashboard's system status reuses the user counts for summaryTTL, callers at the
// same moment share one count, and the API's summary still counts afresh.
func TestSystemStatusReusesTheUserCounts(t *testing.T) {
	m, st := closeTestManager(t)
	defer st.Close()
	defer m.Close()
	if _, err := st.CreateUser("a", "uuid-a", "pw", "tok-a", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	first, err := m.SystemStatus()
	if err != nil || first.Users != 1 {
		t.Fatalf("first status: %+v %v", first, err)
	}
	if _, err := st.CreateUser("b", "uuid-b", "pw", "tok-b", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s, err := m.SystemStatus(); err != nil || s.Users != 1 {
				t.Errorf("status within the TTL: users=%d err=%v", s.Users, err)
			}
		}()
	}
	wg.Wait()
	if s, err := m.Summary(); err != nil || s.Users != 2 {
		t.Fatalf("the summary did not count afresh: %+v %v", s, err)
	}

	m.summaryMu.Lock()
	m.summaryAt = time.Now().Add(-summaryTTL - time.Second)
	m.summaryMu.Unlock()
	if s, err := m.SystemStatus(); err != nil || s.Users != 2 {
		t.Fatalf("counts past the TTL were reused: %+v %v", s, err)
	}
}
