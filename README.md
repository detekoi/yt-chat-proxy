# YouTube Chat Proxy (`yt-chat-proxy`)

A lightweight, stateless Go-based WebSocket proxy server that bridges live YouTube chat messages (via the InnerTube API) to connected browser clients. It was specifically built as the bridging backend for the [`wildcat-home`](https://github.com/detekoi/wildcat-home) chat overlay (live at [wildcat.chat/chat-overlay](https://wildcat.chat/chat-overlay/)).

## Features
- **Idempotent WebSocket Hub**: Multiple browser/OBS instances can connect to the same stream Target simultaneously. The server handles multiplexing so that only *one* outgoing polling loop is active per YouTube channel, minimizing API footprint.
- **Pre-Stream Retry Logic**: The server features a 10-minute backoff retry mechanism upon connection. If a user loads the chat overlay *before* they actually click "Go Live" on YouTube, the proxy will silently keep checking every 15 seconds. Once the stream goes live, the UI updates to "Connected" automatically without requiring a manual browser refresh.
- **Server-Side Deduplication**: Keeps track of `seenIds` to prevent the InnerTube API's "replay buffer" from flooding clients with historical messages on reconnects or page refreshes.
- **Resilient Connection Lifecycle**: Features a 30-second teardown "grace period." If the chat overlay refreshes (e.g. via OBS reloading or switching scenes), the poller stream remains alive seamlessly instead of terminating and restarting from scratch.
- **Continuous Keepalives**: Built-in server-side 30s `PING/PONG` logic strictly maintains the connection state through restrictive reverse proxies or cloud gateways.

## How it Works
The frontend establishes a `/ws` WebSocket connection. To target a stream, it sends a JSON `JOIN` action containing either the public `@handle` string (e.g., `@parfaitfair`) for public live events, or a direct 11-character video ID / URL for unlisted live events. The server spins up a native InnerTube API poller mapping to YouTube's internal `resolve_url` and `next` endpoints, dynamically locating the live broadcast and streaming localized JSON chat payloads directly to the WebSockets.

## Local Development

Written in Go 1.22+. Ensure you have the Go toolchain installed.

1. **Clone & Install Dependencies**
   ```bash
   git clone <repo>
   cd yt-chat-proxy
   go mod download
   ```

2. **Run Locally**
   ```bash
   # Default listens on port 8092
   go run main.go
   ```

3. **Test the WebSocket via browser**
   ```
   ws://localhost:8092/ws?target=your_youtube_handle
   ```

## Production Deployment (VPS)

The proxy runs on a VPS at `ytchat.wildcat.chat` using Docker Compose with Caddy for automatic HTTPS. A VPS is used instead of Cloud Run because YouTube blocks InnerTube API requests from major cloud provider datacenter IPs.

1. **Automatic deploys**: Pushing to `main` triggers the GitHub Actions workflow, which SSHes into the VPS and redeploys.

2. **Manual deploy** (from the VPS):
   ```bash
   cd /opt/yt-chat-proxy
   git pull origin main
   docker compose -f docker-compose.prod.yml build --no-cache
   docker compose -f docker-compose.prod.yml up -d
   ```

3. **View logs**:
   ```bash
   docker compose -f docker-compose.prod.yml logs -f proxy
   ```

4. **Health check**:
   ```
   https://ytchat.wildcat.chat/health
   ```

