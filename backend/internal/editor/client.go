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

	case "request_document":
		c.handleDocumentRequest(msg)

	case "save_document":
		c.handleSaveDocument(msg)

	case "typing_start":
		c.handleTypingStart(msg)

	case "typing_stop":
		c.handleTypingStop(msg)

	case "cursor_position":
		c.handleCursorPosition(msg)

	case "selection_change":
		c.handleSelectionChange(msg)

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
	log.Printf("[CLIENT] handleTextUpdate from %s, version %d", c.id, msg.Version)

	// Extract version from message (default to 0 for backward compatibility)
	clientVersion := 0
	if msg.Version > 0 {
		clientVersion = msg.Version
	}

	// Update document using OT
	if c.service != nil {
		newContent, newVersion, err := c.service.UpdateDocument(
			c.documentID,
			msg.Content,
			c.id,
			clientVersion,
		)

		if err != nil {
			log.Printf("Error updating document: %v", err)
			c.sendError("Failed to update document")
			return
		}

		// Update message with transformed content and new version
		msg.Content = newContent
		msg.Version = newVersion
	}

	// Add metadata
	msg.ClientID = c.id
	msg.DocumentID = c.documentID

	// Broadcast the transformed update to other clients
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Error marshaling text update: %v", err)
		return
	}

	// Send through hub broadcast channel
	c.hub.broadcast <- data

	log.Printf("Client %s sent text update for doc %s (version %d)", c.id, c.documentID, msg.Version)
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

func (c *Client) handleCursorPosition(msg Message) {
	log.Printf("[CLIENT] Cursor position from %s: %v", c.id, msg.Position)

	// Extract position from message
	position := 0
	if pos, ok := msg.Data.(float64); ok {
		position = int(pos)
	} else if msg.Position > 0 {
		position = msg.Position
	}

	// Update cursor position in document's cursor manager
	if c.service != nil {
		doc, err := c.service.GetDocument(c.documentID)
		if err != nil {
			log.Printf("Error getting document: %v", err)
			return
		}

		// Initialize CursorManager if nil
		if doc.CursorManager == nil {
			doc.CursorManager = NewCursorManager()
		}

		doc.CursorManager.UpdateCursorPosition(c.id, c.username, c.color, position)
	}

	// Broadcast cursor position to other clients
	cursorMsg := Message{
		Type:       "cursor_position",
		ClientID:   c.id,
		DocumentID: c.documentID,
		Data: map[string]interface{}{
			"clientId": c.id,
			"username": c.username,
			"color":    c.color,
			"position": position,
		},
	}

	data, err := json.Marshal(cursorMsg)
	if err != nil {
		log.Printf("Error marshaling cursor position: %v", err)
		return
	}

	c.hub.broadcast <- data
}

func (c *Client) handleSelectionChange(msg Message) {
	log.Printf("[CLIENT] Selection change from %s", c.id)

	// Extract selection range
	start, end := 0, 0
	if selection, ok := msg.Data.(map[string]interface{}); ok {
		if s, ok := selection["start"].(float64); ok {
			start = int(s)
		}
		if e, ok := selection["end"].(float64); ok {
			end = int(e)
		}
	}

	// Update selection in document's cursor manager
	if c.service != nil {
		doc, _ := c.service.GetDocument(c.documentID)
		if doc.CursorManager != nil {
			doc.CursorManager.UpdateSelection(c.id, c.username, c.color, start, end)
		}
	}

	// Broadcast selection to other clients
	selectionMsg := Message{
		Type:       "selection_change",
		ClientID:   c.id,
		DocumentID: c.documentID,
		Data: map[string]interface{}{
			"clientId": c.id,
			"username": c.username,
			"color":    c.color,
			"start":    start,
			"end":      end,
		},
	}

	data, err := json.Marshal(selectionMsg)
	if err != nil {
		log.Printf("Error marshaling selection: %v", err)
		return
	}

	c.hub.broadcast <- data
}

// When client disconnects, remove their cursor
func (c *Client) cleanup() {
	if c.service != nil {
		doc, _ := c.service.GetDocument(c.documentID)
		if doc.CursorManager != nil {
			doc.CursorManager.RemoveClient(c.id)

			// Notify other clients to remove this cursor
			removeMsg := Message{
				Type:       "cursor_remove",
				ClientID:   c.id,
				DocumentID: c.documentID,
				Data: map[string]interface{}{
					"clientId": c.id,
				},
			}

			data, _ := json.Marshal(removeMsg)
			c.hub.broadcast <- data
		}
	}
}
