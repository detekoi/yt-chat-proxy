# YouTube Chat Proxy (`yt-chat-proxy`)

A lightweight, stateless Go-based WebSocket proxy server that bridges live YouTube chat messages (via the InnerTube API) to connected browser clients. It was specifically built as the bridging backend for the `wildcat-home` chat overlay.

## Features
- **Idempotent WebSocket Hub**: Multiple browser/OBS instances can connect to the same stream Target simultaneously. The server handles multiplexing so that only *one* outgoing polling loop is active per YouTube channel, minimizing API footprint.
- **Server-Side Deduplication**: Keeps track of `seenIds` to prevent the InnerTube API's "replay buffer" from flooding clients with historical messages on reconnects or page refreshes.
- **Resilient Connection Lifecycle**: Features a 30-second teardown "grace period." If the chat overlay refreshes (e.g. via OBS reloading or switching scenes), the poller stream remains alive seamlessly instead of terminating and restarting from scratch.
- **Continuous Keepalives**: Built-in server-side 30s `PING/PONG` logic strictly maintains the connection state through restrictive reverse proxies or cloud gateways.

## How it Works
The frontend establishes a `/ws` WebSocket connection containing the YouTube channel ID or standard @handle query param (`?target=parfaitfair`). The server spins up an InnerTube poller that scrapes live streaming data, strips it of unnecessary metadata, and pushes real-time chat payload buffers to the subscribed WebSocket(s).

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

## Cloud Run Deployment

The proxy is containerized and built to be deployed on Google Cloud Run. It requires no persistent volume or database. 

1. Ensure you have the `gcloud` CLI installed and authorized for the `chat-themer` project.
2. Run the deployment script:
   ```bash
   ./deploy.sh
   ```
   This will run `gcloud builds submit` using the multi-stage `Dockerfile`, build an optimized Alpine image, and deploy it to `us-central1`. 
   
*Note: Make sure to check the output of the script for the live `wss://` URI.*
