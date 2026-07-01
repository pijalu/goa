// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package helper

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 512 * 1024
)

// WSHub manages WebSocket connections and broadcasts messages to all connected clients.
type WSHub struct {
	// Registered clients by client pointer
	clients map[*WSClient]bool

	// Register requests from clients
	register chan *WSClient

	// Unregister requests from clients
	unregister chan *WSClient

	// Broadcast channel for messages to send to all clients
	broadcast chan []byte

	// Session-specific broadcast: sessionID -> message
	broadcastSession map[string]chan []byte

	mu sync.RWMutex
}

// WSClient represents a WebSocket client connection.
type WSClient struct {
	hub *WSHub

	// Buffered channel of outbound messages
	send chan []byte

	// WebSocket connection
	conn *websocket.Conn

	// Metadata (e.g., session_id for session-specific messaging)
	Metadata map[string]interface{}
}

// NewWSHub creates a new WSHub.
func NewWSHub() *WSHub {
	return &WSHub{
		clients:          make(map[*WSClient]bool),
		register:         make(chan *WSClient),
		unregister:       make(chan *WSClient),
		broadcast:        make(chan []byte, 256),
		broadcastSession: make(map[string]chan []byte),
	}
}

// Run starts the hub's main loop in a goroutine.
func (h *WSHub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			h.mu.Unlock()

		case message := <-h.broadcast:
			h.mu.RLock()
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					// Client buffer full, close connection
					close(client.send)
					delete(h.clients, client)
				}
			}
			h.mu.RUnlock()
		}
	}
}

// Broadcast sends a message to all connected clients.
func (h *WSHub) Broadcast(message []byte) {
	select {
	case h.broadcast <- message:
	default:
		// Broadcast channel full, drop message
	}
}

// BroadcastToSession sends a message to all clients in a specific session.
func (h *WSHub) BroadcastToSession(sessionID string, message []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for client := range h.clients {
		if sid, ok := client.Metadata["session_id"].(string); ok && sid == sessionID {
			select {
			case client.send <- message:
			default:
				// Client buffer full
			}
		}
	}
}

// ClientCount returns the number of connected clients.
func (h *WSHub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// ReadPump pumps messages from the WebSocket connection to the hub.
func (c *WSClient) ReadPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				// Log error
			}
			break
		}

		// Handle incoming messages (e.g., ping/pong, session join)
		var msg map[string]interface{}
		if err := json.Unmarshal(message, &msg); err != nil {
			continue
		}

		// Handle session join
		if msgType, ok := msg["type"].(string); ok && msgType == "join_session" {
			if sessionID, ok := msg["session_id"].(string); ok {
				c.Metadata["session_id"] = sessionID
			}
		}
	}
}

// WritePump pumps messages from the hub to the WebSocket connection.
func (c *WSClient) WritePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			if !c.writeMessageBatch(message, ok) {
				return
			}
		case <-ticker.C:
			if !c.writePing() {
				return
			}
		}
	}
}

func (c *WSClient) writeMessageBatch(first []byte, ok bool) bool {
	c.conn.SetWriteDeadline(time.Now().Add(writeWait))
	if !ok {
		c.conn.WriteMessage(websocket.CloseMessage, []byte{})
		return false
	}

	w, err := c.conn.NextWriter(websocket.TextMessage)
	if err != nil {
		return false
	}
	w.Write(first)

	n := len(c.send)
	for i := 0; i < n; i++ {
		w.Write([]byte{'\n'})
		w.Write(<-c.send)
	}

	return w.Close() == nil
}

func (c *WSClient) writePing() bool {
	c.conn.SetWriteDeadline(time.Now().Add(writeWait))
	return c.conn.WriteMessage(websocket.PingMessage, nil) == nil
}

// ServeWS handles a WebSocket connection from the HTTP server.
func (h *WSHub) ServeWS(w http.ResponseWriter, r *http.Request) *WSClient {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return nil
	}

	client := &WSClient{
		hub:  h,
		send: make(chan []byte, 256),
		conn: conn,
		Metadata: map[string]interface{}{
			"remote_addr": r.RemoteAddr,
		},
	}

	h.register <- client

	// Start pump goroutines
	go client.WritePump()
	go client.ReadPump()

	return client
}

// upgrader is the WebSocket upgrader configuration.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for development
	},
}
