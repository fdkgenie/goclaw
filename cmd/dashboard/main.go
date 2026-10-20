package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/time/rate"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/activity"
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
)

// serviceOptions tunes the runtime behaviour of the dashboard daemon. Zero
// values fall back to production defaults.
type serviceOptions struct {
	// ReconnectMin/ReconnectMax bound the gateway WebSocket reconnect
	// backoff.
	ReconnectMin time.Duration
	ReconnectMax time.Duration
}

func (o serviceOptions) withDefaults() serviceOptions {
	if o.ReconnectMin <= 0 {
		o.ReconnectMin = defaultReconnectMin
	}
	if o.ReconnectMax <= 0 {
		o.ReconnectMax = defaultReconnectMax
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
	wsClient := goclaw.NewClient(goclaw.Config{
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
	ring := activity.NewRingBuffer()
	if err := ring.Backfill(ctx, httpClient); err != nil {
		slog.Warn("activity backfill failed", "err", err)
	}

	reconciler := live.NewReconciler(state, httpClient, hub)

	// Wire the gateway event stream into the live pipeline:
	// WS event -> live state (via hub) -> SSE delta.
	agentEvents := make(chan *goclaw.EventFrame, 256)
	traceEvents := make(chan *goclaw.EventFrame, 256)
	wsClient.Subscribe(protocol.EventAgent, agentEvents)
	wsClient.Subscribe(protocol.EventTraceStatusChanged, traceEvents)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case frame := <-agentEvents:
				if evt, ok := goclaw.AgentEventFromFrame(frame); ok {
					ring.Add(activity.ActivityItem{
						ID:        fmt.Sprintf("agent:%s:%s:%d", evt.RunID, evt.Event, evt.Timestamp.UnixNano()),
						Type:      activity.TypeAgentEvent,
						Timestamp: evt.Timestamp,
						AgentID:   evt.AgentID,
						TeamID:    evt.TeamID,
						Summary:   evt.Event,
						Details:   evt,
					})
					reconciler.HandleEvent(evt)
				}
			case frame := <-traceEvents:
				if evt, ok := goclaw.TraceStatusFromFrame(frame); ok {
					reconciler.HandleEvent(evt)
				}
			}
		}
	}()

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
