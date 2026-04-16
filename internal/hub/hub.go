package hub

import (
	"log/slog"
	"sync"
)

type Hub struct {
	mu           sync.RWMutex
	clients      map[*Client]bool
	subsRegistry map[string]map[*Client]bool // target -> clients
	startPoller  func(target string)
	stopPoller   func(target string)
}

func New(startFn, stopFn func(string)) *Hub {
	return &Hub{
		clients:      make(map[*Client]bool),
		subsRegistry: make(map[string]map[*Client]bool),
		startPoller:  startFn,
		stopPoller:   stopFn,
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
				slog.Info("no more clients for target", "target", target)
				if h.stopPoller != nil {
					h.stopPoller(target)
				}
			}
		}
	}
}

func (h *Hub) Subscribe(c *Client, target string) {
	h.mu.Lock()
	defer h.mu.Unlock()

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
