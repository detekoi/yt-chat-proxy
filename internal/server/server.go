package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/detekoi/yt-chat-proxy/internal/hub"
	"nhooyr.io/websocket"
)

type Server struct {
	router *http.ServeMux
	hub    *hub.Hub
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
		fmt.Fprint(w, `{"status": "ok", "service": "yt-chat-proxy"}`)
	}
}

type connectMessage struct {
	Action string `json:"action"` // "JOIN"
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
					// Send ACK
					client.Send(map[string]any{
						"type":   "system",
						"status": "connected",
						"target": msg.Target,
					})
				}
			}
		}
	}
}
