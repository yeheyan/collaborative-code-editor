// internal/editor/hub.go
package editor

import (
	"encoding/json"
	"log"
)

// Hub maintains active client connections and broadcasts messages
type Hub struct {
	// Registered clients
	clients map[*Client]bool

	// Inbound messages from clients
	broadcast chan []byte

	// Register requests from clients
	register chan *Client

	// Unregister requests from clients
	unregister chan *Client

	// Document-specific client tracking
	documentClients map[string]map[*Client]bool
}

// Message represents different types of messages
type Message struct {
	Type       string      `json:"type"`
	Content    string      `json:"content,omitempty"`
	Position   int         `json:"position,omitempty"`
	ClientID   string      `json:"clientId,omitempty"`
	DocumentID string      `json:"documentId,omitempty"`
	Version    int         `json:"version,omitempty"`
	Data       interface{} `json:"data,omitempty"`
}

// NewHub creates a new Hub
func NewHub() *Hub {
	return &Hub{
		broadcast:       make(chan []byte),
		register:        make(chan *Client),
		unregister:      make(chan *Client),
		clients:         make(map[*Client]bool),
		documentClients: make(map[string]map[*Client]bool),
	}
}

// run starts the hub's main loop
func (h *Hub) run() {
	for {
		select {
		case client := <-h.register:
			h.handleRegister(client)

		case client := <-h.unregister:
			h.handleUnregister(client)

		case message := <-h.broadcast:
			h.handleBroadcast(message)
		}
	}
}

// handleRegister handles client registration
func (h *Hub) handleRegister(client *Client) {
	log.Printf("[HUB] Registering client %s for document %s", client.id, client.documentID)

	h.clients[client] = true

	if client.documentID != "" {
		if h.documentClients[client.documentID] == nil {
			h.documentClients[client.documentID] = make(map[*Client]bool)
		}
		h.documentClients[client.documentID][client] = true
		log.Printf("[HUB] Document %s now has %d clients", client.documentID, len(h.documentClients[client.documentID]))

		// List all clients in document
		for c := range h.documentClients[client.documentID] {
			log.Printf("[HUB]   - Client %s in document", c.id)
		}
	}

	log.Printf("Client %s connected. Total clients: %d", client.id, len(h.clients))

	// THIS IS THE KEY PART - notify others
	if client.documentID != "" {
		h.notifyUserJoined(client)
	}
}

// handleUnregister handles client disconnection
func (h *Hub) handleUnregister(client *Client) {
	if _, ok := h.clients[client]; ok {
		delete(h.clients, client)
		close(client.send)

		// Remove from document-specific tracking
		if client.documentID != "" && h.documentClients[client.documentID] != nil {
			delete(h.documentClients[client.documentID], client)

			// Clean up empty document entries
			if len(h.documentClients[client.documentID]) == 0 {
				delete(h.documentClients, client.documentID)
			} else {
				// IMPORTANT: Notify remaining users
				h.notifyUserLeft(client)
			}
		}

		if client.service != nil {
			doc, _ := client.service.GetDocument(client.documentID)
			if doc != nil && doc.CursorManager != nil {
				doc.CursorManager.RemoveClient(client.id)
			}

			// Send cursor_remove message to other clients
			removeMsg := Message{
				Type:       "cursor_remove",
				ClientID:   client.id,
				DocumentID: client.documentID,
				Data: map[string]interface{}{
					"clientId": client.id,
				},
			}

			if data, err := json.Marshal(removeMsg); err == nil {
				h.broadcastToDocument(client.documentID, data, client.id)
			}
		}

		// Remove from service's document tracking
		if client.service != nil {
			client.service.RemoveClientFromDocument(client)
		}

		log.Printf("Client %s disconnected. Total clients: %d", client.id, len(h.clients))
	}
}

// handleBroadcast handles message broadcasting
func (h *Hub) handleBroadcast(message []byte) {
	var msg Message
	if err := json.Unmarshal(message, &msg); err != nil {
		log.Printf("Error unmarshaling message: %v", err)
		return
	}

	log.Printf("[HUB] Broadcasting message type: %s from client: %s", msg.Type, msg.ClientID)

	// Route message based on type
	switch msg.Type {
	case "text_update":
		// For text updates, broadcast to all others in the document
		h.broadcastToDocument(msg.DocumentID, message, msg.ClientID)

	case "cursor_position", "selection":
		h.broadcastToDocument(msg.DocumentID, message, msg.ClientID)

	case "typing_start", "typing_stop":
		h.broadcastToDocument(msg.DocumentID, message, msg.ClientID)

	default:
		// Default: broadcast to document
		if msg.DocumentID != "" {
			h.broadcastToDocument(msg.DocumentID, message, msg.ClientID)
		}
	}
}

// broadcastToDocument sends a message to all clients in a specific document
func (h *Hub) broadcastToDocument(docID string, message []byte, excludeClientID string) {
	clients := h.documentClients[docID]
	if clients == nil {
		log.Printf("[HUB] No clients for document %s", docID)
		return
	}

	sentCount := 0
	for client := range clients {
		if client.id != excludeClientID {
			select {
			case client.send <- message:
				sentCount++
				log.Printf("[HUB] Sent message to client %s", client.id)
			default:
				log.Printf("[HUB] Client %s buffer full, closing", client.id)
				close(client.send)
				delete(h.clients, client)
				delete(clients, client)
			}
		}
	}

	log.Printf("[HUB] Broadcast complete: sent to %d clients", sentCount)
}

// broadcastToAll sends a message to all connected clients
func (h *Hub) broadcastToAll(message []byte, excludeClientID string) {
	for client := range h.clients {
		if client.id != excludeClientID {
			select {
			case client.send <- message:
			default:
				close(client.send)
				delete(h.clients, client)
			}
		}
	}
}

// notifyUserJoined notifies other users in a document that a new user joined
func (h *Hub) notifyUserJoined(newClient *Client) {
	log.Printf("[HUB] notifyUserJoined called for client %s", newClient.id)

	// Send join notification
	notification := Message{
		Type:       "user_joined",
		ClientID:   newClient.id,
		DocumentID: newClient.documentID,
		Data: map[string]interface{}{
			"userId":   newClient.id,
			"username": newClient.username,
			"color":    newClient.color,
		},
	}

	data, err := json.Marshal(notification)
	if err != nil {
		log.Printf("Error marshaling join notification: %v", err)
		return
	}

	// Broadcast to other users
	h.broadcastToDocument(newClient.documentID, data, newClient.id)

	// CRITICAL: Send active users list to ALL users
	h.sendActiveUsersToAll(newClient.documentID)
}

func (h *Hub) sendActiveUsersToAll(documentID string) {
	log.Printf("[HUB] sendActiveUsersToAll called for document %s", documentID)

	users := []map[string]interface{}{}

	if clients := h.documentClients[documentID]; clients != nil {
		log.Printf("[HUB] Found %d clients in document", len(clients))
		for c := range clients {
			users = append(users, map[string]interface{}{
				"userId":   c.id,
				"username": c.username,
				"color":    c.color,
			})
			log.Printf("[HUB]   Adding user %s to list", c.id)
		}
	}

	message := Message{
		Type:       "active_users",
		DocumentID: documentID,
		Data:       users,
	}

	data, err := json.Marshal(message)
	if err != nil {
		log.Printf("Error marshaling active users: %v", err)
		return
	}

	// Send to ALL clients
	if clients := h.documentClients[documentID]; clients != nil {
		for client := range clients {
			select {
			case client.send <- data:
				log.Printf("[HUB] Sent active users list to client %s", client.id)
			default:
				log.Printf("[HUB] Failed to send to client %s (channel full)", client.id)
			}
		}
	}
}

// notifyUserLeft notifies other users in a document that a user left
func (h *Hub) notifyUserLeft(leftClient *Client) {
	notification := Message{
		Type:       "user_left",
		ClientID:   leftClient.id,
		DocumentID: leftClient.documentID,
		Data: map[string]interface{}{
			"userId": leftClient.id,
		},
	}

	data, err := json.Marshal(notification)
	if err != nil {
		log.Printf("Error marshaling leave notification: %v", err)
		return
	}

	// Broadcast to remaining users
	h.broadcastToDocument(leftClient.documentID, data, leftClient.id)

	// Update active users list for remaining users
	h.sendActiveUsersToAll(leftClient.documentID)
}

// sendActiveUsers sends list of active users to a specific client (for initial connection)
func (h *Hub) sendActiveUsers(client *Client) {
	users := []map[string]interface{}{}

	if clients := h.documentClients[client.documentID]; clients != nil {
		for c := range clients {
			// Include ALL users (the frontend will handle displaying "others")
			users = append(users, map[string]interface{}{
				"userId":   c.id,
				"username": c.username,
				"color":    c.color,
			})
		}
	}

	message := Message{
		Type: "active_users",
		Data: users,
	}

	data, err := json.Marshal(message)
	if err != nil {
		log.Printf("Error marshaling active users: %v", err)
		return
	}

	select {
	case client.send <- data:
		log.Printf("Sent initial active users list to client %s", client.id)
	default:
		// Client not ready to receive
	}
}

// shutdown gracefully shuts down the hub
func (h *Hub) shutdown() {
	// Close all client connections
	for client := range h.clients {
		close(client.send)
		client.conn.Close()
	}

	log.Println("Hub shutdown complete")
}

// GetStats returns statistics about the hub
func (h *Hub) GetStats() map[string]interface{} {
	stats := map[string]interface{}{
		"total_clients":    len(h.clients),
		"total_documents":  len(h.documentClients),
		"documents_detail": make(map[string]int),
	}

	// Add per-document client counts
	for docID, clients := range h.documentClients {
		stats["documents_detail"].(map[string]int)[docID] = len(clients)
	}

	return stats
}
