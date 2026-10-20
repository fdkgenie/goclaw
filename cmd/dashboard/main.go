package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/time/rate"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/activity"
	"github.com/nextlevelbuilder/goclaw/internal/dashboard/aggregate"
	"github.com/nextlevelbuilder/goclaw/internal/dashboard/config"
	"github.com/nextlevelbuilder/goclaw/internal/dashboard/goclaw"
	"github.com/nextlevelbuilder/goclaw/internal/dashboard/live"
	"github.com/nextlevelbuilder/goclaw/internal/dashboard/server"
	"github.com/nextlevelbuilder/goclaw/pkg/protocol"
)

const (
	sessionMaxAge = 24 * time.Hour

	defaultReconnectMin = 500 * time.Millisecond
	defaultReconnectMax = 30 * time.Second

	// defaultBackfillTimeout bounds the activity backfill. The backfill
	// waits for gateway RPC answers, so a gateway that accepts the
	// connection but never answers must not hold the caller.
	defaultBackfillTimeout = 15 * time.Second
)

// serviceOptions tunes the runtime behaviour of the dashboard daemon. Zero
// values fall back to production defaults.
type serviceOptions struct {
	// ReconnectMin/ReconnectMax bound the gateway WebSocket reconnect
	// backoff.
	ReconnectMin time.Duration
	ReconnectMax time.Duration
	// BackfillTimeout bounds the activity backfill (startup and
	// reconnect). The backfill issues RPCs on the gateway connection and
	// waits for the answers, so the deadline is what keeps a silent
	// gateway from blocking the startup.
	BackfillTimeout time.Duration
}

func (o serviceOptions) withDefaults() serviceOptions {
	if o.ReconnectMin <= 0 {
		o.ReconnectMin = defaultReconnectMin
	}
	if o.ReconnectMax <= 0 {
		o.ReconnectMax = defaultReconnectMax
	}
	if o.BackfillTimeout <= 0 {
		o.BackfillTimeout = defaultBackfillTimeout
	}
	return o
}

func main() {
	webDir := flag.String("web-dir", "", "optional directory to serve dashboard UI from")
	flag.Parse()

	cfg := config.MustLoad()
	cfg.Sanitize()
	if *webDir != "" {
		cfg.WebDir = *webDir
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg); err != nil {
		slog.Error("goclaw-dashboard exited with error", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg *config.Config) error {
	return runWithOptions(ctx, cfg, serviceOptions{})
}

// runWithOptions wires the gateway client, the live pipeline, and the HTTP
// server, then blocks until ctx is cancelled.
//
// Fail-closed contract: the tenant canary must pass before any data is
// served. If the gateway connection drops, the circuit breaker opens and the
// data APIs and the SSE stream answer 503 until the canary re-verifies after
// a successful reconnect.
func runWithOptions(ctx context.Context, cfg *config.Config, opts serviceOptions) error {
	opts = opts.withDefaults()

	slog.Info("starting goclaw-dashboard", "addr", cfg.DashboardAddr, "tenant", cfg.TenantID)

	httpClient := goclaw.NewHTTPClient(cfg.GoClawAPIURL, cfg.GoClawAPIKey, cfg.TenantID.String(), 15*time.Second)
	state := live.NewState()
	hub := live.NewHub(state)

	// Activity feed: the history is loaded from the gateway, the live events
	// are appended by the gateway event pump.
	ring := activity.NewRingBuffer()

	// The window the feed lost while the gateway connection was down. The
	// gap marker is inserted when the connection is restored.
	var gapMu sync.Mutex
	var disconnectedAt time.Time

	// wsClient is declared before its constructor: the reconnect hook below
	// refers to it.
	var wsClient *goclaw.Client
	wsClient = goclaw.NewClient(goclaw.Config{
		WSURL:        cfg.GoClawWSURL,
		APIToken:     cfg.GoClawAPIKey,
		TenantID:     cfg.TenantID,
		UserID:       "dashboard",
		ReconnectMin: opts.ReconnectMin,
		ReconnectMax: opts.ReconnectMax,
		OnDisconnect: func(err error) {
			// The tenant binding is no longer verified: fail closed until the
			// reconnect canary passes.
			slog.Warn("dashboard websocket disconnected", "err", err)
			gapMu.Lock()
			disconnectedAt = time.Now()
			gapMu.Unlock()
			goclaw.SetDegraded("gateway disconnected: " + err.Error())
			hub.MarkDegradedAndPublish(err.Error())
		},
		OnReconnect: func() error {
			// Re-run the HTTP tenant canary; the breaker stays open until it
			// passes, so data APIs and SSE remain fail-closed in the meantime.
			if err := goclaw.VerifyTenantCanary(ctx, httpClient, cfg.TenantID); err != nil {
				return err
			}
			hub.MarkRecoveredAndPublish()
			// The feed lost the window the gateway was down: mark the gap and
			// reload the history that happened meanwhile.
			gapMu.Lock()
			since := disconnectedAt
			disconnectedAt = time.Time{}
			gapMu.Unlock()
			if !since.IsZero() {
				ring.InsertGapMarker(since)
			}
			// The backfill issues an RPC on the gateway connection, and this
			// hook runs on the goroutine that reads that connection: the call
			// would never be answered. It runs in its own goroutine instead.
			go runBackfill(ctx, opts.BackfillTimeout, ring, httpClient, wsClient)
			slog.Info("tenant canary re-verified after reconnect", "tenant", cfg.TenantID)
			return nil
		},
	})

	// Fail-closed: connect and verify tenant before serving any data.
	if err := wsClient.Connect(ctx); err != nil {
		return err
	}
	if !wsClient.Connected() {
		return slogError("gateway not connected")
	}

	if err := goclaw.VerifyTenantCanary(ctx, httpClient, cfg.TenantID); err != nil {
		return err
	}
	slog.Info("tenant canary verified", "tenant", cfg.TenantID)

	// Activity history (best-effort; live events are appended below).
	//
	// The backfill waits for gateway RPC answers, so it runs under a
	// deadline: a gateway that accepts the connection but never answers
	// must not keep the HTTP server from coming up. A backfill that fails
	// or times out is not fatal, the feed starts empty and the live events
	// still land.
	runBackfill(ctx, opts.BackfillTimeout, ring, httpClient, wsClient)

	reconciler := live.NewReconciler(state, httpClient, hub)

	// Wire the gateway event stream into the live pipeline:
	// WS event -> live state (via hub) -> SSE delta.
	pumpGatewayEvents(ctx, wsClient, reconciler, ring)

	// Start background reconcile loop.
	go reconciler.Run(ctx)

	// HTTP server.
	sm := server.NewSessionManager(sessionMaxAge)
	rl := server.NewRateLimiter(rate.Every(time.Minute), 5)
	router := server.NewRouter(cfg, state, wsClient, httpClient, hub, sm, rl, ring)

	srv := &http.Server{
		Addr:    cfg.DashboardAddr,
		Handler: router,
	}

	go func() {
		slog.Info("dashboard HTTP server listening", "addr", cfg.DashboardAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("dashboard HTTP server failed", "err", err)
		}
	}()

	// Wait for shutdown signal.
	<-ctx.Done()
	slog.Info("shutting down goclaw-dashboard")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		wsClient.Close()
		return err
	}
	if err := wsClient.Close(); err != nil {
		return err
	}
	slog.Info("goclaw-dashboard stopped")
	return nil
}

func slogError(msg string) error {
	return fmt.Errorf("%s", msg)
}

// runBackfill runs the activity backfill under a deadline.
//
// The backfill waits for gateway RPC answers, so a gateway that accepts the
// connection but never answers would hold the caller forever. The deadline
// releases it. The failure is observable (a warning is logged) but not fatal:
// the feed keeps whatever it already has, the live events still land, and
// the service keeps serving.
func runBackfill(ctx context.Context, timeout time.Duration, ring *activity.RingBuffer, httpClient *goclaw.HTTPClient, wsClient *goclaw.Client) {
	bctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	backfillActivity(bctx, ring, httpClient, wsClient)
	if errors.Is(bctx.Err(), context.DeadlineExceeded) {
		slog.Warn("activity backfill timed out", "timeout", timeout)
	}
}

// backfillActivity loads the gateway activity history into the feed: the
// audit log over HTTP and the team task events over the RPC. Both are best
// effort: a failure is logged and the feed keeps whatever it already has.
func backfillActivity(ctx context.Context, ring *activity.RingBuffer, httpClient *goclaw.HTTPClient, wsClient *goclaw.Client) {
	if err := ring.Backfill(ctx, httpClient); err != nil {
		slog.Warn("activity backfill failed", "err", err)
	}
	teams, err := aggregate.ListTeams(ctx, wsClient)
	if err != nil {
		slog.Warn("activity team backfill failed", "err", err)
		return
	}
	teamIDs := make([]string, 0, len(teams))
	for _, t := range teams {
		teamIDs = append(teamIDs, t.ID)
	}
	if err := ring.BackfillTeams(ctx, wsClient, teamIDs); err != nil {
		slog.Warn("activity team backfill failed", "err", err)
	}
}

// pumpGatewayEvents forwards gateway WebSocket events into the live pipeline.
//
// Agent lifecycle events are appended to the activity ring and applied to the
// live state through the reconciler, which publishes an SSE delta to every
// subscriber. Trace status events only update the live state. The team task
// events are appended to the activity ring. The pump runs until ctx is
// cancelled.
func pumpGatewayEvents(ctx context.Context, wsClient *goclaw.Client, reconciler *live.Reconciler, ring *activity.RingBuffer) {
	agentEvents := make(chan *goclaw.EventFrame, 256)
	traceEvents := make(chan *goclaw.EventFrame, 256)
	taskEvents := make(chan *goclaw.EventFrame, 256)
	wsClient.Subscribe(protocol.EventAgent, agentEvents)
	wsClient.Subscribe(protocol.EventTraceStatusChanged, traceEvents)
	// The team task events are a whole family (team.task.*): the wildcard
	// subscription keeps the feed complete when the gateway adds one.
	wsClient.SubscribeAll(taskEvents)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case frame := <-agentEvents:
				evt, ok := goclaw.AgentEventFromFrame(frame)
				if !ok {
					continue
				}
				if ring != nil {
					ring.Add(activity.ActivityItem{
						ID:        fmt.Sprintf("agent:%s:%s:%d", evt.RunID, evt.Event, evt.Timestamp.UnixNano()),
						Type:      activity.TypeAgentEvent,
						Timestamp: evt.Timestamp,
						AgentID:   evt.AgentID,
						TeamID:    evt.TeamID,
						Summary:   evt.Event,
						Details:   evt,
					})
				}
				reconciler.HandleEvent(evt)
			case frame := <-traceEvents:
				evt, ok := goclaw.TraceStatusFromFrame(frame)
				if !ok {
					continue
				}
				reconciler.HandleEvent(evt)
			case frame := <-taskEvents:
				if frame == nil || !strings.HasPrefix(frame.Event, "team.task.") {
					continue
				}
				if ring == nil {
					continue
				}
				it, ok := activity.FromTeamTaskEvent(frame.Event, frame.Payload)
				if !ok {
					continue
				}
				ring.Add(it)
			}
		}
	}()
}
