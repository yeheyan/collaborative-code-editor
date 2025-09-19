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
	h.clients[client] = true

	// Add to document-specific tracking
	if client.documentID != "" {
		if h.documentClients[client.documentID] == nil {
			h.documentClients[client.documentID] = make(map[*Client]bool)
		}
		h.documentClients[client.documentID][client] = true
	}

	log.Printf("Client %s connected. Total clients: %d", client.id, len(h.clients))

	// Notify other clients in the same document
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
			}

			// Notify other clients in the same document
			h.notifyUserLeft(client)
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

	// Route message based on type
	switch msg.Type {
	case "text_update", "cursor_position", "selection":
		h.broadcastToDocument(msg.DocumentID, message, msg.ClientID)

	case "broadcast_all":
		h.broadcastToAll(message, msg.ClientID)

	default:
		// Default behavior: broadcast to document
		if msg.DocumentID != "" {
			h.broadcastToDocument(msg.DocumentID, message, msg.ClientID)
		}
	}
}

// broadcastToDocument sends a message to all clients in a specific document
func (h *Hub) broadcastToDocument(docID string, message []byte, excludeClientID string) {
	clients := h.documentClients[docID]
	if clients == nil {
		log.Printf("No clients for document %s", docID)
		return
	}

	log.Printf("Broadcasting to %d clients in doc %s (excluding %s)", len(clients), docID, excludeClientID)

	for client := range clients {
		if client.id != excludeClientID {
			select {
			case client.send <- message:
				log.Printf("Sent message to client %s", client.id)
			default:
				log.Printf("Client %s buffer full, closing", client.id)
				close(client.send)
				delete(h.clients, client)
				delete(clients, client)
			}
		}
	}
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
	notification := Message{
		Type:       "user_joined",
		ClientID:   newClient.id,
		DocumentID: newClient.documentID,
		Data: map[string]interface{}{
			"userId":   newClient.id,
			"username": newClient.username,
			"color":    newClient.color, // For cursor color
		},
	}

	data, err := json.Marshal(notification)
	if err != nil {
		log.Printf("Error marshaling join notification: %v", err)
		return
	}

	h.broadcastToDocument(newClient.documentID, data, newClient.id)

	// Send list of active users to the new client
	h.sendActiveUsers(newClient)
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

	h.broadcastToDocument(leftClient.documentID, data, leftClient.id)
}

// sendActiveUsers sends list of active users to a client
func (h *Hub) sendActiveUsers(client *Client) {
	users := []map[string]interface{}{}

	if clients := h.documentClients[client.documentID]; clients != nil {
		for c := range clients {
			if c.id != client.id {
				users = append(users, map[string]interface{}{
					"userId":   c.id,
					"username": c.username,
					"color":    c.color,
				})
			}
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
