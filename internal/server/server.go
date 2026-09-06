package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/detekoi/yt-chat-proxy/internal/hub"
	"nhooyr.io/websocket"
)

type Server struct {
	router *http.ServeMux
	hub    *hub.Hub
	// pollerStatus, when set, is included in /health so poller health can be
	// inspected on a deployed instance.
	pollerStatus func() any
	// isStreaming, when set, reports whether the poller for a target is already
	// attached to a live chat, so a late-joining client can be told immediately.
	isStreaming func(target string) bool
}

// SetStreamingFunc registers a predicate used to tell late-joining clients that
// the stream they subscribed to is already connected.
func (s *Server) SetStreamingFunc(fn func(target string) bool) {
	s.isStreaming = fn
}

// SetPollerStatusFunc registers a provider whose result is embedded in /health as "pollers".
func (s *Server) SetPollerStatusFunc(fn func() any) {
	s.pollerStatus = fn
}

func New(h *hub.Hub) *Server {
	s := &Server{
		router: http.NewServeMux(),
		hub:    h,
	}
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    // Add CORS headers broadly for Dev overlay testing
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	s.router.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.router.HandleFunc("GET /health", s.handleHealth())
	s.router.HandleFunc("GET /ws", s.handleWS())
}

func (s *Server) handleHealth() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := map[string]any{
			"status":      "ok",
			"service":     "yt-chat-proxy",
			"subscribers": s.hub.SubscriberCounts(),
		}
		if s.pollerStatus != nil {
			body["pollers"] = s.pollerStatus()
		}
		if err := json.NewEncoder(w).Encode(body); err != nil {
			slog.Error("health encode error", "err", err)
		}
	}
}

type connectMessage struct {
	Action string `json:"action"` // "JOIN" or "PING"
	Target string `json:"target"` // video ID or @handle
}

func (s *Server) handleWS() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true, // Allow connection from any origin
		})
		if err != nil {
			slog.Error("websocket accept error", "error", err)
			return
		}
		defer conn.Close(websocket.StatusInternalError, "internal error")

		client := hub.NewClient(s.hub, conn)
		s.hub.AddClient(client)
		defer s.hub.RemoveClient(client)

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		// Start write pump
		go client.WritePump(ctx)

		// Read loop
		for {
			_, msgBytes, err := conn.Read(ctx)
			if err != nil {
				if websocket.CloseStatus(err) != -1 {
					slog.Info("websocket closed normal", "err", err)
				} else {
					slog.Error("websocket read error", "err", err)
				}
				break
			}

			var msg connectMessage
			if err := json.Unmarshal(msgBytes, &msg); err == nil {
				if msg.Action == "JOIN" && msg.Target != "" {
					slog.Info("client requesting JOIN", "target", msg.Target)
					s.hub.Subscribe(client, msg.Target)
					// Send ACK. Note this only confirms the subscription; the poller
					// announces "Connected to YouTube stream." separately once it is
					// attached to a live chat.
					client.Send(map[string]any{
						"type":   "system",
						"status": "connected",
						"target": msg.Target,
					})
					// A client joining an already-running poller would otherwise never
					// hear that announcement (it was broadcast before they subscribed).
					if s.isStreaming != nil && s.isStreaming(msg.Target) {
						client.Send(map[string]any{
							"type":    "system",
							"status":  "connected",
							"message": "Connected to YouTube stream.",
						})
					}
				} else if msg.Action == "PING" {
					// Application-level heartbeat. Browser clients cannot observe
					// WebSocket-level pings, so this reply is what lets the overlay
					// detect a socket that has silently gone stale.
					client.Send(map[string]any{"type": "pong"})
				}
			}
		}
	}
}
