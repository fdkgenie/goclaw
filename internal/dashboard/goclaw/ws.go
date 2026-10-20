package goclaw

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/nextlevelbuilder/goclaw/pkg/protocol"
)

const (
	defaultReconnectMin = 500 * time.Millisecond
	defaultReconnectMax = 30 * time.Second
	readDeadline        = 60 * time.Second
	writeDeadline       = 15 * time.Second
	dialTimeout         = 10 * time.Second

	// wildcardSubscription is the subscription key that receives every event
	// frame. The dashboard uses it for the event families it consumes by
	// prefix (team.task.*), so a new gateway event of a family is picked up
	// without touching the subscription list.
	wildcardSubscription = "*"
)

// Client is a long-lived WebSocket client to the GoClaw Gateway. It performs
// the connect handshake with fail-closed security checks, multiplexes RPC
// requests and event subscriptions over a single connection, and reconnects
// with exponential backoff on disconnect.
type Client struct {
	cfg    Config
	dialer *websocket.Dialer

	connMu    sync.Mutex // guards conn
	conn      *websocket.Conn
	connected bool

	pendingMu sync.Mutex
	pending   map[string]chan *ResponseFrame

	subsMu sync.Mutex
	subs   map[string][]chan *EventFrame

	connectMu   sync.RWMutex
	connectResp *ConnectResponse

	startMu   sync.Mutex
	started   bool
	closeOnce sync.Once

	onDisconnect func(error)

	// runCancel stops the supervisor goroutine; runDone is closed when it
	// returns, so Close can wait for a deterministic shutdown.
	runCancel context.CancelFunc
	runDone   chan struct{}

	// closed is set by Close so the read loop does not report a disconnect
	// during a deliberate shutdown.
	closed atomic.Bool
}

// Config configures the WS client.
type Config struct {
	WSURL        string
	APIToken     string
	TenantID     uuid.UUID
	UserID       string
	ReconnectMin time.Duration
	ReconnectMax time.Duration
	OnDisconnect func(error)
	// OnReconnect runs after every successful reconnect handshake (not the
	// initial connect). A non-nil error fails the reconnect and keeps the
	// client backing off, so the service stays fail-closed until verification
	// passes.
	OnReconnect func() error
}

// ResponseFrame is a protocol response received from the gateway.
type ResponseFrame = protocol.ResponseFrame

// EventFrame is a protocol event received from the gateway.
type EventFrame = protocol.EventFrame

// NewClient creates a WS client but does not connect. Call Connect.
func NewClient(cfg Config) *Client {
	if cfg.ReconnectMin == 0 {
		cfg.ReconnectMin = defaultReconnectMin
	}
	if cfg.ReconnectMax == 0 {
		cfg.ReconnectMax = defaultReconnectMax
	}
	return &Client{
		cfg:     cfg,
		dialer:  &websocket.Dialer{},
		pending: make(map[string]chan *ResponseFrame),
		subs:    make(map[string][]chan *EventFrame),
	}
}

// Connect performs the connect handshake with fail-closed validation, then
// starts a background supervisor that runs the read loop and reconnects with
// exponential backoff on disconnect. Returns nil once the initial handshake
// succeeds, or an error if the handshake fails or ctx is cancelled.
func (c *Client) Connect(ctx context.Context) error {
	c.startMu.Lock()
	if c.started {
		c.startMu.Unlock()
		return fmt.Errorf("already started")
	}
	if c.closed.Load() {
		c.startMu.Unlock()
		return fmt.Errorf("client is closed")
	}
	c.started = true
	c.startMu.Unlock()

	if err := c.connectOnce(ctx); err != nil {
		return err
	}

	// Background supervisor: read loop + reconnect on disconnect.
	runCtx, cancel := context.WithCancel(ctx)
	c.startMu.Lock()
	c.runCancel = cancel
	c.runDone = make(chan struct{})
	c.startMu.Unlock()
	go c.run(runCtx)

	return nil
}

// run supervises the read loop: on disconnect it backs off and re-performs
// the connect handshake (re-verifying capabilities) until ctx is done. After
// every successful reconnect handshake it runs the OnReconnect verification
// hook; a hook error fails the reconnect, so the caller stays fail-closed
// until verification passes.
func (c *Client) run(ctx context.Context) {
	defer close(c.runDone)

	delay := c.cfg.ReconnectMin
	for {
		c.readLoop(ctx)

		select {
		case <-ctx.Done():
			return
		default:
		}

		// Disconnected: back off, then reconnect and re-verify capabilities.
		if err := c.sleepWithBackoff(ctx, &delay); err != nil {
			return
		}
		if err := c.connectOnce(ctx); err != nil {
			// Handshake failed; keep backing off and retrying.
			continue
		}
		if c.cfg.OnReconnect != nil {
			if err := c.cfg.OnReconnect(); err != nil {
				c.closeConn()
				c.setConnected(false)
				if c.cfg.OnDisconnect != nil && !c.closed.Load() {
					c.cfg.OnDisconnect(err)
				}
				continue
			}
		}
		delay = c.cfg.ReconnectMin
	}
}

// connectOnce dials and performs one connect handshake. On failure it invokes
// OnDisconnect and returns the error.
func (c *Client) connectOnce(ctx context.Context) error {
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	conn, _, err := c.dialer.DialContext(dialCtx, c.cfg.WSURL, nil)
	if err != nil {
		if c.cfg.OnDisconnect != nil {
			c.cfg.OnDisconnect(fmt.Errorf("ws dial: %w", err))
		}
		return fmt.Errorf("ws dial: %w", err)
	}

	c.setConn(conn)

	reqID := uuid.NewString()
	params := map[string]any{
		"tenant_id": c.cfg.TenantID.String(),
		"token":     c.cfg.APIToken,
		"user_id":   c.cfg.UserID,
	}
	req := protocol.RequestFrame{
		Type:   protocol.FrameTypeRequest,
		ID:     reqID,
		Method: "connect",
		Params: mustJSON(params),
	}

	if err := c.writeJSON(req); err != nil {
		c.closeConn()
		if c.cfg.OnDisconnect != nil {
			c.cfg.OnDisconnect(fmt.Errorf("ws write connect: %w", err))
		}
		return fmt.Errorf("ws write connect: %w", err)
	}

	// Read the connect response directly from the connection (readLoop is not
	// running yet during the handshake).
	conn.SetReadDeadline(time.Now().Add(readDeadline))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		c.closeConn()
		if c.cfg.OnDisconnect != nil {
			c.cfg.OnDisconnect(fmt.Errorf("ws read connect response: %w", err))
		}
		return fmt.Errorf("ws read connect response: %w", err)
	}
	var res protocol.ResponseFrame
	if err := json.Unmarshal(msg, &res); err != nil {
		c.closeConn()
		if c.cfg.OnDisconnect != nil {
			c.cfg.OnDisconnect(fmt.Errorf("ws parse connect: %w", err))
		}
		return fmt.Errorf("ws parse connect: %w", err)
	}
	if res.ID != reqID {
		c.closeConn()
		return fmt.Errorf("ws connect response id mismatch: got %q want %q", res.ID, reqID)
	}

	var connectResp ConnectResponse
	if err := unmarshalPayload(res.Payload, &connectResp); err != nil {
		c.closeConn()
		if c.cfg.OnDisconnect != nil {
			c.cfg.OnDisconnect(fmt.Errorf("ws parse connect payload: %w", err))
		}
		return fmt.Errorf("ws parse connect payload: %w", err)
	}

	// Fail-closed validation.
	if err := ValidateConnect(connectResp, c.cfg.TenantID); err != nil {
		c.closeConn()
		if c.cfg.OnDisconnect != nil {
			c.cfg.OnDisconnect(err)
		}
		return err
	}

	c.setConnectResp(&connectResp)
	return nil
}

// readLoop reads frames from the connection until it closes or ctx is done.
// On exit it closes disconnectCh (once) to signal the supervisor and reports
// the underlying read error through OnDisconnect.
func (c *Client) readLoop(ctx context.Context) {
	var readErr error
	defer func() {
		c.closeConn()
		c.setConnected(false)
		if c.cfg.OnDisconnect != nil && !c.closed.Load() {
			if readErr == nil {
				readErr = context.Canceled
			}
			c.cfg.OnDisconnect(readErr)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		conn := c.getConn()
		if conn == nil {
			return
		}

		conn.SetReadDeadline(time.Now().Add(readDeadline))
		_, msg, err := conn.ReadMessage()
		if err != nil {
			readErr = err
			return // disconnect
		}

		var raw protocol.RawFrame
		if err := json.Unmarshal(msg, &raw); err != nil {
			continue
		}

		switch raw.Type {
		case protocol.FrameTypeResponse:
			var res protocol.ResponseFrame
			if err := json.Unmarshal(msg, &res); err != nil {
				continue
			}
			c.deliverResponse(&res)
		case protocol.FrameTypeEvent:
			var evt protocol.EventFrame
			if err := json.Unmarshal(msg, &evt); err != nil {
				continue
			}
			c.deliverEvent(&evt)
		}
	}
}

// sleepWithBackoff sleeps with exponential backoff + jitter, doubling delay
// (capped at ReconnectMax) after each call.
func (c *Client) sleepWithBackoff(ctx context.Context, delay *time.Duration) error {
	jitter := time.Duration(rand.Int64N(int64(*delay / 2)))
	timer := time.NewTimer(*delay + jitter)
	select {
	case <-ctx.Done():
		timer.Stop()
		return ctx.Err()
	case <-timer.C:
	}
	*delay *= 2
	if *delay > c.cfg.ReconnectMax {
		*delay = c.cfg.ReconnectMax
	}
	return nil
}

// Call sends an RPC request and waits for the matching response.
//
// The method must be on the read-only allowlist: the dashboard never
// mutates gateway state, so a non-allowlisted method (an agent/exec
// mutation) is rejected before the frame is written and the gateway never
// sees the call.
func (c *Client) Call(ctx context.Context, method string, params any) (*ResponseFrame, error) {
	if !IsAllowedRPCMethod(method) {
		LogBlocked("rpc", method, "")
		return nil, fmt.Errorf("rpc method not allowed: %s", method)
	}

	reqID := uuid.NewString()
	req := protocol.RequestFrame{
		Type:   protocol.FrameTypeRequest,
		ID:     reqID,
		Method: method,
		Params: mustJSON(params),
	}

	ch := make(chan *ResponseFrame, 1)
	c.pendingMu.Lock()
	if !c.isConnected() {
		c.pendingMu.Unlock()
		return nil, fmt.Errorf("not connected")
	}
	c.pending[reqID] = ch
	c.pendingMu.Unlock()

	if err := c.writeJSON(req); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, reqID)
		c.pendingMu.Unlock()
		return nil, fmt.Errorf("ws write rpc %s: %w", method, err)
	}

	select {
	case <-ctx.Done():
		c.pendingMu.Lock()
		delete(c.pending, reqID)
		c.pendingMu.Unlock()
		return nil, ctx.Err()
	case res := <-ch:
		return res, nil
	}
}

// Subscribe registers a channel to receive events of the given name.
func (c *Client) Subscribe(event string, ch chan *EventFrame) func() {
	c.subsMu.Lock()
	c.subs[event] = append(c.subs[event], ch)
	c.subsMu.Unlock()
	return func() {
		c.subsMu.Lock()
		if lst, ok := c.subs[event]; ok {
			for i, c2 := range lst {
				if c2 == ch {
					c.subs[event] = append(lst[:i], lst[i+1:]...)
					break
				}
			}
			if len(c.subs[event]) == 0 {
				delete(c.subs, event)
			}
		}
		c.subsMu.Unlock()
	}
}

// SubscribeAll registers a channel to receive every event frame, whatever its
// name. The subscriber filters the event families it consumes.
func (c *Client) SubscribeAll(ch chan *EventFrame) func() {
	return c.Subscribe(wildcardSubscription, ch)
}

// ConnectResponse returns the last validated connect response, or nil.
func (c *Client) ConnectResponse() *ConnectResponse {
	c.connectMu.RLock()
	defer c.connectMu.RUnlock()
	return c.connectResp
}

// Connected reports whether the client is currently connected.
func (c *Client) Connected() bool {
	c.connectMu.RLock()
	defer c.connectMu.RUnlock()
	return c.connected
}

// Close shuts down the client: it stops the reconnect supervisor, closes the
// connection, and waits for the supervisor goroutine to return so no
// reconnect can be attempted after Close.
func (c *Client) Close() error {
	c.startMu.Lock()
	cancel := c.runCancel
	done := c.runDone
	c.startMu.Unlock()

	c.closeOnce.Do(func() {
		c.closed.Store(true)
		if cancel != nil {
			cancel()
		}
	})
	c.closeConn()
	if done != nil {
		<-done
	}
	return nil
}

// ----- internal -----

func (c *Client) setConn(conn *websocket.Conn) {
	c.connMu.Lock()
	c.conn = conn
	c.connMu.Unlock()
}

func (c *Client) getConn() *websocket.Conn {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	return c.conn
}

func (c *Client) closeConn() {
	c.connMu.Lock()
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	c.connMu.Unlock()
}

func (c *Client) setConnected(v bool) {
	c.connectMu.Lock()
	c.connected = v
	c.connectMu.Unlock()
}

func (c *Client) isConnected() bool {
	c.connectMu.Lock()
	defer c.connectMu.Unlock()
	return c.connected
}

func (c *Client) setConnectResp(resp *ConnectResponse) {
	c.connectMu.Lock()
	c.connectResp = resp
	c.connected = true
	c.connectMu.Unlock()
}

func (c *Client) writeJSON(v any) error {
	conn := c.getConn()
	if conn == nil {
		return fmt.Errorf("no connection")
	}
	conn.SetWriteDeadline(time.Now().Add(writeDeadline))
	return conn.WriteJSON(v)
}

func (c *Client) deliverResponse(res *ResponseFrame) {
	c.pendingMu.Lock()
	ch, ok := c.pending[res.ID]
	if ok {
		delete(c.pending, res.ID)
	}
	c.pendingMu.Unlock()
	if !ok {
		return
	}
	select {
	case ch <- res:
	default:
	}
}

func (c *Client) deliverEvent(evt *EventFrame) {
	c.subsMu.Lock()
	lst := make([]chan *EventFrame, 0, len(c.subs[evt.Event])+len(c.subs[wildcardSubscription]))
	lst = append(lst, c.subs[evt.Event]...)
	lst = append(lst, c.subs[wildcardSubscription]...)
	c.subsMu.Unlock()
	for _, ch := range lst {
		select {
		case ch <- evt:
		default:
		}
	}
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func unmarshalPayload(payload any, out any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}
