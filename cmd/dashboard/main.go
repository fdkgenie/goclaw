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
)

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
	slog.Info("starting goclaw-dashboard", "addr", cfg.DashboardAddr, "tenant", cfg.TenantID)

	httpClient := goclaw.NewHTTPClient(cfg.GoClawAPIURL, cfg.GoClawAPIKey, cfg.TenantID.String(), 15*time.Second)
	wsClient := goclaw.NewClient(goclaw.Config{
		WSURL:        cfg.GoClawWSURL,
		APIToken:     cfg.GoClawAPIKey,
		TenantID:     cfg.TenantID,
		UserID:       "dashboard",
		ReconnectMin: 500 * time.Millisecond,
		ReconnectMax: 30 * time.Second,
		OnDisconnect: func(err error) {
			slog.Warn("dashboard websocket disconnected", "err", err)
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

	// Live state + reconciler + SSE hub.
	state := live.NewState()
	hub := live.NewHub(state)
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
		return err
	}
	wsClient.Close()
	slog.Info("goclaw-dashboard stopped")
	return nil
}

func slogError(msg string) error {
	return fmt.Errorf("%s", msg)
}
