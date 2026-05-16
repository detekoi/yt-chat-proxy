# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Run locally (default port 8092)
go run main.go

# Build
go build -o server main.go

# Run tests
go test ./...

# Run a single package's tests
go test ./internal/youtube/...

# Tidy dependencies
go mod tidy
```

**Deploy to VPS** (CI runs on push to `main` via GitHub Actions — SSH into `/opt/yt-chat-proxy` and runs `docker compose -f docker-compose.prod.yml up -d`):
```bash
# Manual deploy to Cloud Run (legacy path)
./deploy.sh   # requires gcloud CLI authorized for project "chat-themer"
```

## Architecture

This is a stateless Go WebSocket proxy that bridges YouTube live chat (via the InnerTube API) to connected browser clients. There are no databases, queues, or persistent state beyond in-process maps.

**Request flow:**
1. Browser connects to `GET /ws`
2. Client sends `{"action":"JOIN","target":"@handle_or_videoId"}`
3. `server` subscribes the client in `hub`, which triggers `PollerManager.StartPoller`
4. Poller resolves the target → fetches the live chat continuation token → polls `get_live_chat` in a loop
5. Chat messages are broadcast to all clients subscribed to that target

**Package layout (`internal/`):**

- **`hub`** — manages the client registry and poller lifecycle. `Hub` holds `subsRegistry` (target → clients) and `graceTimers`. When the last client for a target disconnects, a 30-second grace timer fires before stopping the poller — so OBS scene switches don't kill the polling loop. `Client.WritePump` runs in its own goroutine and sends 30s WebSocket pings.

- **`server`** — thin HTTP layer. Two routes: `GET /health` and `GET /ws`. Accepts WebSocket upgrades with `InsecureSkipVerify: true` (CORS is open). Reads `JOIN` messages and calls `hub.Subscribe`.

- **`youtube`** — InnerTube API client and poller logic.
  - `InnerTubeClient` (`innertube.go`): three API calls — `resolve_url` (handle → videoId), `browse` (channel → live videoId via LIVE overlay badge), `next` (videoId → continuation token), then repeated `get_live_chat` polls. All requests carry browser-spoofed headers to avoid 403s on datacenter IPs.
  - `PollerManager` (`poller.go`): manages a `map[target]context.CancelFunc`. Retries `ResolveTarget` up to 40 times (15s apart = 10 min) for pre-stream loads. Deduplicates messages using `seenIDs` (reset at 5000 entries). Normalizes `AddChatItemAction` into a flat JSON map with `type`, `eventType` (chat/superchat/supersticker/membership), `username`, `message`, `emotes`, `tags`, `id`, `amount`, `subtext`, `bodyColor`, `headerColor`, `platform`.
  - `types.go`: all InnerTube response structs.

**Concurrency model:** `Hub` uses a single `sync.RWMutex`. `PollerManager` uses a separate `sync.Mutex`. Each `PollerManager.runPoller` runs in its own goroutine; each `Client.WritePump` runs in its own goroutine. No channels between hub and poller — poller calls `hub.Broadcast` directly.

**Production infra:** VPS running Docker Compose with two services — `proxy` (this binary on port 8080) and `caddy` (TLS termination, proxying `ytchat.wildcat.chat` → `proxy:8080`). The `clientVersion` constant in `innertube.go` may need periodic bumping if YouTube starts rejecting the WEB client.
