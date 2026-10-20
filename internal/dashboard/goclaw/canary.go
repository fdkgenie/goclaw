package goclaw

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// CircuitBreaker tracks the tenant canary degradation state. When degraded
// (tenant mismatch on startup or reconnect), the dashboard must fail closed:
// data APIs return 503 and SSE emits connection: down. It resets on a
// successful reconnect with a matching tenant.
type CircuitBreaker struct {
	mu       sync.RWMutex
	degraded bool
	reason   string
	at       time.Time
}

// IsDegraded reports whether the breaker is currently open (degraded).
func (b *CircuitBreaker) IsDegraded() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.degraded
}

// Reason returns the last degradation reason (empty if not degraded).
func (b *CircuitBreaker) Reason() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.reason
}

// SetDegraded opens the breaker with the given reason.
func (b *CircuitBreaker) SetDegraded(reason string) {
	b.mu.Lock()
	b.degraded = true
	b.reason = reason
	b.at = time.Now()
	b.mu.Unlock()
	slog.Warn("security.dashboard.tenant_mismatch", "reason", reason, "at", b.at)
}

// Reset closes the breaker, restoring normal operation (successful reconnect).
func (b *CircuitBreaker) Reset() {
	b.mu.Lock()
	b.degraded = false
	b.reason = ""
	b.at = time.Time{}
	b.mu.Unlock()
}

// defaultBreaker is the package-level breaker used by the router and SSE hub
// to enforce fail-closed behavior across the daemon.
var defaultBreaker = &CircuitBreaker{}

// IsDegraded reports the package-level degradation state.
func IsDegraded() bool { return defaultBreaker.IsDegraded() }

// Reason returns the package-level degradation reason.
func Reason() string { return defaultBreaker.Reason() }

// SetDegraded opens the package-level breaker.
func SetDegraded(reason string) { defaultBreaker.SetDegraded(reason) }

// Reset closes the package-level breaker.
func Reset() { defaultBreaker.Reset() }

// agentEntry is a single agent returned by /v1/agents.
type agentEntry struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	TenantID  string `json:"tenant_id"`
	Role      string `json:"role"`
	Model     string `json:"model"`
	Status    string `json:"status"`
	UpdatedAt string `json:"updated_at"`
}

// agentsResponse is the /v1/agents response envelope.
type agentsResponse struct {
	Agents []agentEntry `json:"agents"`
}

// VerifyTenantCanary fetches /v1/agents and asserts every agent's tenant_id
// matches expectedTenantID. Empty agent list is valid (empty tenant). On
// mismatch it opens the circuit breaker and returns an error identifying the
// security failure. Fail-closed.
func VerifyTenantCanary(ctx context.Context, httpClient *HTTPClient, expectedTenantID uuid.UUID) error {
	var resp agentsResponse
	if err := httpClient.Get(ctx, "/v1/agents", &resp); err != nil {
		SetDegraded(fmt.Sprintf("canary request failed: %v", err))
		return fmt.Errorf("tenant canary request failed: %w", err)
	}

	for _, a := range resp.Agents {
		tid, err := uuid.Parse(a.TenantID)
		if err != nil {
			SetDegraded(fmt.Sprintf("agent %s has invalid tenant_id %q", a.ID, a.TenantID))
			return fmt.Errorf("tenant canary: agent %s has invalid tenant_id %q: %w", a.ID, a.TenantID, err)
		}
		if tid != expectedTenantID {
			SetDegraded(fmt.Sprintf("agent %s tenant %s != expected %s", a.ID, a.TenantID, expectedTenantID))
			return fmt.Errorf("tenant canary: agent %s tenant %s != expected %s (security failure)", a.ID, a.TenantID, expectedTenantID)
		}
	}

	Reset()
	return nil
}
