package main

import (
	"log/slog"
	"net/http"
	"os"

	"github.com/detekoi/yt-chat-proxy/internal/hub"
	"github.com/detekoi/yt-chat-proxy/internal/server"
	"github.com/detekoi/yt-chat-proxy/internal/youtube"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	var h *hub.Hub
	var pm *youtube.PollerManager

	h = hub.New(
		func(target string) {
			if pm != nil {
				pm.StartPoller(target)
			}
		},
		func(target string) {
			if pm != nil {
				pm.StopPoller(target)
			}
		},
	)

	pm = youtube.NewPollerManager(h)

	srv := server.New(h)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8092"
	}

	slog.Info("yt-chat-proxy starting", "port", port)
	if err := http.ListenAndServe(":"+port, srv); err != nil {
		slog.Error("server failed", "err", err)
		os.Exit(1)
	}
}
