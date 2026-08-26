package telegram

import (
	"errors"
	"fmt"
	"testing"
)

func TestBotPollErrorDeduplication(t *testing.T) {
	svc := &Service{}

	// Initial state: no error recorded.
	if svc.lastPollErr != "" {
		t.Fatalf("expected empty initial lastPollErr, got %q", svc.lastPollErr)
	}

	// First DNS failure: ephemeral port 38000.
	err1 := errors.New(`Get "https://api.telegram.org/bot<token hidden>/getUpdates?allowed_updates=%5B%22message%22%2C%22callback_query%22%5D&offset=0&timeout=25": dial tcp: lookup api.telegram.org on 127.0.0.53:53: read udp 127.0.0.1:38000->127.0.0.53:53: i/o timeout`)
	key1 := pollErrorKey(err1)
	if key1 == "" {
		t.Fatalf("expected non-empty key for err1")
	}

	// Should record error on first occurrence.
	loggedCount := 0
	if key1 != svc.lastPollErr {
		loggedCount++
		svc.lastPollErr = key1
	}
	if loggedCount != 1 {
		t.Fatalf("expected 1 log event, got %d", loggedCount)
	}

	// Subsequent DNS failures with different ephemeral ports must NOT re-log.
	ports := []int{14722, 41382, 47092, 59123}
	for _, p := range ports {
		errN := fmt.Errorf(`Get "https://api.telegram.org/bot<token hidden>/getUpdates?allowed_updates=%%5B%%22message%%22%%2C%%22callback_query%%22%%5D&offset=0&timeout=25": dial tcp: lookup api.telegram.org on 127.0.0.53:53: read udp 127.0.0.1:%d->127.0.0.53:53: i/o timeout`, p)
		keyN := pollErrorKey(errN)
		if keyN != svc.lastPollErr {
			loggedCount++
			svc.lastPollErr = keyN
		}
	}
	if loggedCount != 1 {
		t.Fatalf("expected loggedCount to remain 1 after repeated DNS timeouts, got %d", loggedCount)
	}

	// Recovery clears lastPollErr.
	recoveredLogged := false
	if svc.lastPollErr != "" {
		recoveredLogged = true
		svc.lastPollErr = ""
	}
	if !recoveredLogged {
		t.Fatalf("expected recovery log to trigger")
	}
	if svc.lastPollErr != "" {
		t.Fatalf("expected lastPollErr to be cleared after recovery")
	}

	// New distinct error (e.g. 502 Bad Gateway) should log.
	err502 := errors.New("telegram api 502: Bad Gateway")
	key502 := pollErrorKey(err502)
	if key502 != svc.lastPollErr {
		loggedCount++
		svc.lastPollErr = key502
	}
	if loggedCount != 2 {
		t.Fatalf("expected 2 total logs after 502 error, got %d", loggedCount)
	}
}

func TestClientForResetsLastPollErrOnTokenSwap(t *testing.T) {
	svc := &Service{
		lastPollErr: "old-error-key",
	}
	// Initial client setup
	c1 := svc.clientFor("token1", "")
	if c1 == nil {
		t.Fatalf("expected non-nil client")
	}
	if svc.lastPollErr != "" {
		t.Fatalf("expected lastPollErr cleared on client rebuild, got %q", svc.lastPollErr)
	}

	// Set an error for token1
	svc.lastPollErr = "token1-error"

	// Same token returns cached client, preserves lastPollErr
	c1Same := svc.clientFor("token1", "")
	if c1Same != c1 {
		t.Fatalf("expected same client returned for unchanged token")
	}
	if svc.lastPollErr != "token1-error" {
		t.Fatalf("expected lastPollErr preserved for unchanged token, got %q", svc.lastPollErr)
	}

	// Token swap rebuilds client and resets lastPollErr
	c2 := svc.clientFor("token2", "")
	if c2 == c1 {
		t.Fatalf("expected fresh client for new token")
	}
	if svc.lastPollErr != "" {
		t.Fatalf("expected lastPollErr reset on token swap, got %q", svc.lastPollErr)
	}
}

func TestUserBotClientForResetsLastPollErr(t *testing.T) {
	u := &UserService{
		lastPollErr: "some-error",
	}
	c := u.clientFor("user-token-1", "")
	if c == nil {
		t.Fatalf("expected non-nil client")
	}
	if u.lastPollErr != "" {
		t.Fatalf("expected lastPollErr reset, got %q", u.lastPollErr)
	}
}

func TestSupportBotClientForResetsLastPollErr(t *testing.T) {
	s := &SupportService{
		lastPollErr: "some-error",
	}
	c := s.clientFor("support-token-1", "")
	if c == nil {
		t.Fatalf("expected non-nil client")
	}
	if s.lastPollErr != "" {
		t.Fatalf("expected lastPollErr reset, got %q", s.lastPollErr)
	}
}
