package hub

import (
	"log/slog"
	"sync"
	"time"
)

type Hub struct {
	mu           sync.RWMutex
	clients      map[*Client]bool
	subsRegistry map[string]map[*Client]bool // target -> clients
	startPoller  func(target string)
	stopPoller   func(target string)
	graceTimers  map[string]*time.Timer // grace period before stopping poller
}

func New(startFn, stopFn func(string)) *Hub {
	return &Hub{
		clients:      make(map[*Client]bool),
		subsRegistry: make(map[string]map[*Client]bool),
		startPoller:  startFn,
		stopPoller:   stopFn,
		graceTimers:  make(map[string]*time.Timer),
	}
}

func (h *Hub) AddClient(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = true
}

func (h *Hub) RemoveClient(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.clients, c)
	for target, clients := range h.subsRegistry {
		if clients[c] {
			delete(clients, c)
			if len(clients) == 0 {
				delete(h.subsRegistry, target)
				slog.Info("no more clients for target, starting 30s grace period", "target", target)

				// Start a grace timer instead of stopping immediately
				t := time.AfterFunc(30*time.Second, func() {
					h.mu.Lock()
					defer h.mu.Unlock()

					// Only stop if nobody reconnected during grace period
					if h.subsRegistry[target] == nil || len(h.subsRegistry[target]) == 0 {
						slog.Info("grace period expired, stopping poller", "target", target)
						delete(h.graceTimers, target)
						if h.stopPoller != nil {
							h.stopPoller(target)
						}
					} else {
						slog.Info("client reconnected during grace period, keeping poller", "target", target)
						delete(h.graceTimers, target)
					}
				})
				h.graceTimers[target] = t
			}
		}
	}
}

func (h *Hub) Subscribe(c *Client, target string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Cancel any pending grace timer for this target
	if t, ok := h.graceTimers[target]; ok {
		t.Stop()
		delete(h.graceTimers, target)
		slog.Info("cancelled grace timer, client reconnected", "target", target)
	}

	if h.subsRegistry[target] == nil {
		h.subsRegistry[target] = make(map[*Client]bool)
		h.subsRegistry[target][c] = true
		slog.Info("first client for target, starting poller", "target", target)
		if h.startPoller != nil {
			h.startPoller(target)
		}
	} else {
		h.subsRegistry[target][c] = true
	}
}

func (h *Hub) Broadcast(target string, message any) {
	h.mu.RLock()
	clients := h.subsRegistry[target]
	var active []*Client
	for c := range clients {
		active = append(active, c)
	}
	h.mu.RUnlock()

	for _, c := range active {
		c.Send(message)
	}
}
