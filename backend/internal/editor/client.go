// internal/editor/client.go
package editor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write a message to the peer
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer
	pongWait = 60 * time.Second

	// Send pings to peer with this period
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer
	maxMessageSize = 512 * 1024 // 512KB
)

var (
	newline = []byte{'\n'}
	space   = []byte{' '}
)

// Client represents a connected user/editor
type Client struct {
	// Unique identifier
	id string

	// The hub that manages this client
	hub *Hub

	// The websocket connection
	conn *websocket.Conn

	// Buffered channel of outbound messages
	send chan []byte

	// Document this client is editing
	documentID string

	// Reference to the service
	service *Service

	// User information
	username string
	color    string // For cursor color

	// Position tracking
	cursorPosition int
	selection      *Selection
}

// Selection represents text selection
type Selection struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// readPump pumps messages from the websocket connection to the hub
func (c *Client) readPump() {
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
				log.Printf("Websocket error: %v", err)
			}
			break
		}

		message = bytes.TrimSpace(bytes.Replace(message, newline, space, -1))

		// Process the message
		c.processMessage(message)
	}
}

// writePump pumps messages from the hub to the websocket connection
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// The hub closed the channel
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			// Add queued messages to the current websocket message
			// n := len(c.send)
			// for i := 0; i < n; i++ {
			// 	w.Write(newline)
			// 	w.Write(<-c.send)
			// }

			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// processMessage processes incoming messages from the client
func (c *Client) processMessage(message []byte) {
	var msg Message
	if err := json.Unmarshal(message, &msg); err != nil {
		log.Printf("Error unmarshaling message: %v", err)
		c.sendError("Invalid message format")
		return
	}

	// Add metadata to message
	msg.ClientID = c.id
	msg.DocumentID = c.documentID

	// Update metrics
	if c.service != nil {
		c.service.metrics.mu.Lock()
		c.service.metrics.MessagesReceived++
		c.service.metrics.mu.Unlock()
	}

	// Handle different message types
	switch msg.Type {
	case "text_update":
		c.handleTextUpdate(msg)

	case "cursor_position":
		c.handleCursorPosition(msg)

	case "selection":
		c.handleSelection(msg)

	case "request_document":
		c.handleDocumentRequest(msg)

	case "save_document":
		c.handleSaveDocument(msg)

	case "typing_start":
		c.handleTypingStart(msg)

	case "typing_stop":
		c.handleTypingStop(msg)

	case "ping":
		// Just a keepalive, no action needed
		return

	default:
		log.Printf("Unknown message type: %s", msg.Type)
		c.sendError(fmt.Sprintf("Unknown message type: %s", msg.Type))
	}
}

// Add new handler functions
func (c *Client) handleTypingStart(msg Message) {
	// Broadcast typing indicator to other users
	msg.Data = map[string]interface{}{
		"userId":   c.id,
		"username": c.username,
		"color":    c.color,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Error marshaling typing start: %v", err)
		return
	}

	c.hub.broadcast <- data
}

func (c *Client) handleTypingStop(msg Message) {
	// Broadcast typing stop to other users
	msg.Data = map[string]interface{}{
		"userId": c.id,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Error marshaling typing stop: %v", err)
		return
	}

	c.hub.broadcast <- data
}

// Update the initialization message to include color
func (c *Client) sendInitMessage() {
	initMsg := Message{
		Type:     "init",
		ClientID: c.id,
		Data: map[string]interface{}{
			"username": c.username,
			"color":    c.color,
		},
	}

	data, err := json.Marshal(initMsg)
	if err != nil {
		log.Printf("Error marshaling init message: %v", err)
		return
	}

	select {
	case c.send <- data:
	default:
		// Client not ready
	}
}

// handleTextUpdate handles text update messages
func (c *Client) handleTextUpdate(msg Message) {
	// Update document in service
	if c.service != nil {
		err := c.service.UpdateDocument(c.documentID, msg.Content, msg.Version)
		if err != nil {
			log.Printf("Error updating document: %v", err)
			c.sendError("Failed to update document")
			return
		}
	}

	// IMPORTANT: Set the client ID and document ID
	msg.ClientID = c.id
	msg.DocumentID = c.documentID

	// Broadcast to other clients
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Error marshaling text update: %v", err)
		return
	}

	// Send through hub broadcast channel
	c.hub.broadcast <- data

	// Update metrics
	if c.service != nil {
		c.service.metrics.mu.Lock()
		c.service.metrics.MessagesSent++
		c.service.metrics.mu.Unlock()
	}

	log.Printf("Client %s sent text update for doc %s", c.id, c.documentID)
}

// handleCursorPosition handles cursor position updates
func (c *Client) handleCursorPosition(msg Message) {
	c.cursorPosition = msg.Position

	// Add user info to message
	msg.Data = map[string]interface{}{
		"userId":   c.id,
		"username": c.username,
		"color":    c.color,
		"position": msg.Position,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Error marshaling cursor position: %v", err)
		return
	}

	c.hub.broadcast <- data
}

// handleSelection handles text selection updates
func (c *Client) handleSelection(msg Message) {
	if selData, ok := msg.Data.(map[string]interface{}); ok {
		c.selection = &Selection{
			Start: int(selData["start"].(float64)),
			End:   int(selData["end"].(float64)),
		}
	}

	// Add user info to message
	msg.Data = map[string]interface{}{
		"userId":    c.id,
		"username":  c.username,
		"color":     c.color,
		"selection": c.selection,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Error marshaling selection: %v", err)
		return
	}

	c.hub.broadcast <- data
}

// handleDocumentRequest handles requests for document state
func (c *Client) handleDocumentRequest(msg Message) {
	if c.service != nil {
		c.service.sendDocumentState(c, c.documentID)
	}
}

// handleSaveDocument handles document save requests
func (c *Client) handleSaveDocument(msg Message) {
	// TODO: Implement document persistence
	log.Printf("Saving document %s", c.documentID)

	response := Message{
		Type: "save_confirmation",
		Data: map[string]interface{}{
			"documentId": c.documentID,
			"saved":      true,
			"timestamp":  time.Now().Unix(),
		},
	}

	data, err := json.Marshal(response)
	if err != nil {
		log.Printf("Error marshaling save confirmation: %v", err)
		return
	}

	select {
	case c.send <- data:
	default:
		// Client not ready to receive
	}
}

// sendError sends an error message to the client
func (c *Client) sendError(errorMsg string) {
	msg := Message{
		Type: "error",
		Data: map[string]interface{}{
			"message": errorMsg,
		},
	}

	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Error marshaling error message: %v", err)
		return
	}

	select {
	case c.send <- data:
	default:
		// Client not ready to receive
	}
}

// SendMessage sends a message to the client
func (c *Client) SendMessage(msg Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	select {
	case c.send <- data:
		return nil
	default:
		return fmt.Errorf("client %s not ready to receive", c.id)
	}
}

// NewClient creates a new client
func NewClient(hub *Hub, conn *websocket.Conn, service *Service, documentID string) *Client {
	clientID := uuid.New().String()

	// Generate a random color for cursor
	colors := []string{"#FF6B6B", "#4ECDC4", "#45B7D1", "#96CEB4", "#FFEAA7", "#DDA0DD", "#98D8C8", "#FFA07A"}
	color := colors[time.Now().UnixNano()%int64(len(colors))]

	return &Client{
		id:         clientID[:8], // Use first 8 chars for display
		hub:        hub,
		conn:       conn,
		send:       make(chan []byte, 256),
		documentID: documentID,
		service:    service,
		username:   fmt.Sprintf("User-%s", clientID[:4]),
		color:      color,
	}
}
