package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/detekoi/yt-chat-proxy/internal/hub"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

func TestHealthEndpoint(t *testing.T) {
	h := hub.New(nil, nil)
	srv := New(h)

	req := httptest.NewRequest("GET", "/health", nil)
	rr := httptest.NewRecorder()

	srv.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	contentType := rr.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", contentType)
	}

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("health body is not JSON: %v: %s", err, rr.Body.String())
	}
	if body["status"] != "ok" || body["service"] != "yt-chat-proxy" {
		t.Errorf("unexpected health body: %s", rr.Body.String())
	}
	if _, ok := body["subscribers"]; !ok {
		t.Errorf("expected subscribers in health body: %s", rr.Body.String())
	}
	if _, ok := body["pollers"]; ok {
		t.Errorf("did not expect pollers without a status provider: %s", rr.Body.String())
	}
}

func TestHealthEndpointIncludesPollerStatus(t *testing.T) {
	h := hub.New(nil, nil)
	srv := New(h)
	srv.SetPollerStatusFunc(func() any {
		return []map[string]any{{"target": "parfaitfair", "phase": "polling"}}
	})

	req := httptest.NewRequest("GET", "/health", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	var body struct {
		Pollers []map[string]any `json:"pollers"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("health body is not JSON: %v", err)
	}
	if len(body.Pollers) != 1 || body.Pollers[0]["target"] != "parfaitfair" {
		t.Errorf("expected poller status in health body, got %s", rr.Body.String())
	}
}

// A client that subscribes while the poller is already attached to a live chat
// must be told so: the poller's own announcement went out before they joined.
func TestWSEndpoint_LateJoinerToldStreamIsConnected(t *testing.T) {
	h := hub.New(nil, nil)
	h.GracePeriod = 5 * time.Millisecond
	srv := New(h)
	srv.SetStreamingFunc(func(target string) bool { return target == "@parfaitfair" })
	testServer := httptest.NewServer(srv)
	defer testServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(testServer.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusInternalError, "closing test conn")

	if err := wsjson.Write(ctx, conn, map[string]string{"action": "JOIN", "target": "@parfaitfair"}); err != nil {
		t.Fatalf("write join: %v", err)
	}
	var ack, announce map[string]any
	if err := wsjson.Read(ctx, conn, &ack); err != nil {
		t.Fatalf("read ack: %v", err)
	}
	if err := wsjson.Read(ctx, conn, &announce); err != nil {
		t.Fatalf("read stream announcement: %v", err)
	}
	if ack["message"] != nil || ack["status"] != "connected" {
		t.Errorf("unexpected ack: %v", ack)
	}
	if announce["status"] != "connected" || announce["message"] != "Connected to YouTube stream." {
		t.Errorf("expected stream-connected announcement, got %v", announce)
	}
}

func TestCORSHeaders(t *testing.T) {
	h := hub.New(nil, nil)
	srv := New(h)

	req := httptest.NewRequest("OPTIONS", "/health", nil)
	rr := httptest.NewRecorder()

	srv.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	if rr.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("expected Access-Control-Allow-Origin to be *")
	}
	if rr.Header().Get("Access-Control-Allow-Methods") != "GET, POST, OPTIONS" {
		t.Errorf("expected Access-Control-Allow-Methods to be GET, POST, OPTIONS")
	}
}

func TestWSEndpoint(t *testing.T) {
	var startPollerCalled bool
	var startPollerTarget string

	h := hub.New(func(target string) {
		startPollerCalled = true
		startPollerTarget = target
	}, nil)
	h.GracePeriod = 5 * time.Millisecond

	srv := New(h)
	testServer := httptest.NewServer(srv)
	defer testServer.Close()

	wsURL := "ws" + strings.TrimPrefix(testServer.URL, "http") + "/ws"

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusInternalError, "closing test conn")

	// 1. Send JOIN message
	joinMsg := map[string]string{
		"action": "JOIN",
		"target": "my-channel",
	}
	err = wsjson.Write(ctx, conn, joinMsg)
	if err != nil {
		t.Fatalf("failed to write join message: %v", err)
	}

	// 2. Read ACK from server
	var ackMsg map[string]any
	err = wsjson.Read(ctx, conn, &ackMsg)
	if err != nil {
		t.Fatalf("failed to read ack message: %v", err)
	}

	if ackMsg["type"] != "system" || ackMsg["status"] != "connected" || ackMsg["target"] != "my-channel" {
		t.Errorf("unexpected ack message: %v", ackMsg)
	}

	// 3. PING must be answered with a pong so browser clients can detect stale sockets
	if err := wsjson.Write(ctx, conn, map[string]string{"action": "PING"}); err != nil {
		t.Fatalf("failed to write ping message: %v", err)
	}
	var pongMsg map[string]any
	if err := wsjson.Read(ctx, conn, &pongMsg); err != nil {
		t.Fatalf("failed to read pong message: %v", err)
	}
	if pongMsg["type"] != "pong" {
		t.Errorf("expected pong reply, got %v", pongMsg)
	}

	// Verify poller was started
	if !startPollerCalled || startPollerTarget != "my-channel" {
		t.Errorf("expected startPoller to be called for my-channel, got called=%v, target=%q", startPollerCalled, startPollerTarget)
	}

	// 3. Broadcast message via hub and verify client receives it
	broadcastMsg := map[string]any{
		"type":    "message",
		"message": "hello world",
	}
	h.Broadcast("my-channel", broadcastMsg)

	var receivedMsg map[string]any
	err = wsjson.Read(ctx, conn, &receivedMsg)
	if err != nil {
		t.Fatalf("failed to read broadcast message: %v", err)
	}

	if receivedMsg["type"] != "message" || receivedMsg["message"] != "hello world" {
		t.Errorf("unexpected broadcast message received: %v", receivedMsg)
	}
}
