package server

import (
	"context"
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

	expectedBody := `{"status": "ok", "service": "yt-chat-proxy"}`
	body := strings.TrimSpace(rr.Body.String())
	if body != expectedBody {
		t.Errorf("expected body %q, got %q", expectedBody, body)
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
