package server

import (
	"crypto/subtle"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestSubtleCompare(t *testing.T) {
	if subtle.ConstantTimeCompare([]byte("secret"), []byte("secret")) != 1 {
		t.Error("equal secrets should compare equal")
	}
	if subtle.ConstantTimeCompare([]byte("secret"), []byte("different")) != 0 {
		t.Error("different secrets should not compare equal")
	}
}

func TestSessionManager(t *testing.T) {
	sm := NewSessionManager(time.Hour)
	tok := sm.Create()
	if tok == "" {
		t.Fatal("expected non-empty token")
	}
	if !sm.Validate(tok) {
		t.Error("expected valid session")
	}
	sm.Delete(tok)
	if sm.Validate(tok) {
		t.Error("expected invalid session after delete")
	}
}

func TestSessionManagerExpiry(t *testing.T) {
	// Already-expired maxAge: session invalid immediately.
	sm := NewSessionManager(-time.Second)
	tok := sm.Create()
	if sm.Validate(tok) {
		t.Error("expected expired session with negative maxAge")
	}

	// Long maxAge: session valid immediately and after a short sleep.
	sm2 := NewSessionManager(time.Hour)
	tok2 := sm2.Create()
	if !sm2.Validate(tok2) {
		t.Error("expected valid session with long maxAge")
	}
	time.Sleep(10 * time.Millisecond)
	if !sm2.Validate(tok2) {
		t.Error("expected session still valid after short sleep")
	}
}

func TestRateLimiter(t *testing.T) {
	rl := NewRateLimiter(rate.Every(time.Minute), 5)
	for i := 0; i < 5; i++ {
		if !rl.Allow("ip-1") {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
	}
	if rl.Allow("ip-1") {
		t.Error("6th attempt within burst should be denied")
	}
	// Different IP not affected.
	if !rl.Allow("ip-2") {
		t.Error("different IP should be allowed")
	}
}
