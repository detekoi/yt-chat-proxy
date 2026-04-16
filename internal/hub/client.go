package hub

import (
	"context"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

type Client struct {
	conn *websocket.Conn
	hub  *Hub
	send chan any
}

func NewClient(hub *Hub, conn *websocket.Conn) *Client {
	return &Client{
		conn: conn,
		hub:  hub,
		send: make(chan any, 256),
	}
}

func (c *Client) Send(m any) {
	select {
	case c.send <- m:
	default:
		// Queue full
	}
}

func (c *Client) WritePump(ctx context.Context) {
	defer c.conn.Close(websocket.StatusGoingAway, "Server closed")
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-c.send:
			if !ok {
				return
			}
			err := wsjson.Write(ctx, c.conn, msg)
			if err != nil {
				return
			}
		}
	}
}
