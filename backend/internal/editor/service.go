// internal/editor/service.go
package editor

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// Service represents the editor service with all its dependencies
type Service struct {
	hub      *Hub
	upgrader websocket.Upgrader
	config   *Config
	mu       sync.RWMutex

	// Document storage (in-memory for now, will be Redis later)
	documents map[string]*Document

	// Metrics
	metrics *Metrics
}

// Config holds service configuration
type Config struct {
	MaxMessageSize int64
	WriteTimeout   time.Duration
	ReadTimeout    time.Duration
	PingInterval   time.Duration
	MaxClients     int
}

// Document represents a collaborative document
type Document struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	Version   int       `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
	CreatedAt time.Time `json:"created_at"`

	// Track active editors
	OTManager     *OTManager         `json:"-"`
	CursorManager *CursorManager     `json:"-"`
	ActiveClients map[string]*Client `json:"-"`
	mu            sync.RWMutex       `json:"-"`
}

// Metrics tracks service performance
type Metrics struct {
	ActiveConnections int64
	MessagesSent      int64
	MessagesReceived  int64
	DocumentsActive   int64

	mu sync.RWMutex
}

// NewService creates a new editor service
func NewService(cfg *Config) *Service {
	if cfg == nil {
		cfg = &Config{
			MaxMessageSize: 512 * 1024, // 512KB
			WriteTimeout:   10 * time.Second,
			ReadTimeout:    60 * time.Second,
			PingInterval:   30 * time.Second,
			MaxClients:     1000,
		}
	}

	return &Service{
		hub: &Hub{
			clients:         make(map[*Client]bool),
			broadcast:       make(chan []byte, 256),
			register:        make(chan *Client),
			unregister:      make(chan *Client),
			documentClients: make(map[string]map[*Client]bool),
		},
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				// TODO: Implement proper CORS check in production
				return true
			},
		},
		config:    cfg,
		documents: make(map[string]*Document),
		metrics:   &Metrics{},
	}
}

// Start initializes and starts the service
func (s *Service) Start() error {
	log.Println("Starting editor service...")

	// Start the hub
	go s.hub.run()

	// Start metrics collector
	go s.collectMetrics()

	// Initialize any required resources
	if err := s.initialize(); err != nil {
		return err
	}

	log.Println("Editor service started successfully")
	return nil
}

// Shutdown gracefully shuts down the service
func (s *Service) Shutdown() {
	log.Println("Shutting down editor service...")

	// Close all client connections
	s.hub.shutdown()

	// Save any pending changes
	s.savePendingDocuments()

	log.Println("Editor service shut down complete")
}

// HandleWebSocket handles WebSocket upgrade requests
func (s *Service) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Extract document ID from query params
	docID := r.URL.Query().Get("doc")
	if docID == "" {
		http.Error(w, "Missing document ID", http.StatusBadRequest)
		return
	}

	// Upgrade connection
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}

	// Create new client with proper ID
	clientID := uuid.New().String()
	client := &Client{
		id:         clientID[:8], // Use first 8 chars for display
		hub:        s.hub,
		conn:       conn,
		send:       make(chan []byte, 256),
		documentID: docID,
		service:    s,
		username:   "User-" + clientID[:4],
		color:      "#4ECDC4",
	}

	// Register client
	s.hub.register <- client

	// Update metrics
	s.metrics.mu.Lock()
	s.metrics.ActiveConnections++
	s.metrics.mu.Unlock()

	initMsg := Message{
		Type:     "init",
		ClientID: client.id,
	}
	initData, _ := json.Marshal(initMsg)

	// Start client goroutines
	go client.writePump()
	go client.readPump()
	client.send <- initData

	// Then register with hub
	s.hub.register <- client

	// Then send document state
	s.sendDocumentState(client, docID)

	log.Printf("Client %s connected for document %s", client.id, docID)
}

// GetDocument retrieves a document by ID
func (s *Service) GetDocument(id string) (*Document, error) {
	s.mu.RLock()
	doc, exists := s.documents[id]
	s.mu.RUnlock()

	if !exists {
		// Create new document if it doesn't exist
		doc = &Document{
			ID:            id,
			Content:       "",
			Version:       1,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			OTManager:     NewOTManager(id), // Initialize OT manager
			CursorManager: NewCursorManager(),
			ActiveClients: make(map[string]*Client),
		}

		s.mu.Lock()
		s.documents[id] = doc
		s.mu.Unlock()

		// Update metrics
		s.metrics.mu.Lock()
		s.metrics.DocumentsActive++
		s.metrics.mu.Unlock()
	}

	return doc, nil
}

// UpdateDocument updates a document's content
// In service.go - modify UpdateDocument to only handle text, not interfere with other messages
func (s *Service) UpdateDocument(id string, content string, clientID string, clientVersion int) (string, int, error) {
	doc, err := s.GetDocument(id)
	if err != nil {
		return "", 0, err
	}

	// Use OT only for text updates
	if doc.OTManager != nil {
		newContent, newVersion, err := doc.OTManager.ProcessTextUpdate(clientID, content, clientVersion)
		if err == nil {
			doc.mu.Lock()
			doc.Content = newContent
			doc.Version = newVersion
			doc.UpdatedAt = time.Now()
			doc.mu.Unlock()
			return newContent, newVersion, nil
		}
	}

	// Fallback to simple versioning
	doc.mu.Lock()
	doc.Content = content
	doc.Version++
	doc.UpdatedAt = time.Now()
	newVersion := doc.Version
	doc.mu.Unlock()

	return content, newVersion, nil
}

// BroadcastToDocument sends a message to all clients editing a document
func (s *Service) BroadcastToDocument(docID string, message []byte, excludeClient *Client) {
	doc, err := s.GetDocument(docID)
	if err != nil {
		log.Printf("Error getting document %s: %v", docID, err)
		return
	}

	doc.mu.RLock()
	defer doc.mu.RUnlock()

	for _, client := range doc.ActiveClients {
		if client != excludeClient {
			select {
			case client.send <- message:
			default:
				// Client's send channel is full, close it
				close(client.send)
				delete(doc.ActiveClients, client.id)
			}
		}
	}

	// Update metrics
	s.metrics.mu.Lock()
	s.metrics.MessagesSent++
	s.metrics.mu.Unlock()
}

// sendDocumentState sends the current document state to a client
func (s *Service) sendDocumentState(client *Client, docID string) {
	doc, err := s.GetDocument(docID)
	if err != nil {
		log.Printf("Error getting document: %v", err)
		return
	}

	// Add client to document's active clients
	doc.mu.Lock()
	doc.ActiveClients[client.id] = client
	doc.mu.Unlock()

	// Send current document state
	state := map[string]interface{}{
		"type":    "document_state",
		"content": doc.Content,
		"version": doc.Version,
		"docId":   doc.ID,
	}

	data, err := json.Marshal(state)
	if err != nil {
		log.Printf("Error marshaling document state: %v", err)
		return
	}

	client.send <- data
}

// RemoveClientFromDocument removes a client from a document's active clients
func (s *Service) RemoveClientFromDocument(client *Client) {
	if client.documentID == "" {
		return
	}

	doc, err := s.GetDocument(client.documentID)
	if err != nil {
		return
	}

	doc.mu.Lock()
	delete(doc.ActiveClients, client.id)
	activeCount := len(doc.ActiveClients)
	doc.mu.Unlock()

	// If no clients are editing, mark document as inactive
	if activeCount == 0 {
		s.metrics.mu.Lock()
		s.metrics.DocumentsActive--
		s.metrics.mu.Unlock()
	}
}

// GetMetrics returns current service metrics
func (s *Service) GetMetrics() map[string]interface{} {
	s.metrics.mu.RLock()
	defer s.metrics.mu.RUnlock()

	return map[string]interface{}{
		"active_connections": s.metrics.ActiveConnections,
		"messages_sent":      s.metrics.MessagesSent,
		"messages_received":  s.metrics.MessagesReceived,
		"documents_active":   s.metrics.DocumentsActive,
		"hub_clients":        len(s.hub.clients),
	}
}

// initialize performs any required initialization
func (s *Service) initialize() error {
	// TODO: Initialize database connection
	// TODO: Load cached documents from Redis
	// TODO: Set up monitoring
	return nil
}

// savePendingDocuments saves any documents with pending changes
func (s *Service) savePendingDocuments() {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for id, doc := range s.documents {
		// TODO: Save to database
		log.Printf("Saving document %s with content length %d", id, len(doc.Content))
	}
}

// collectMetrics periodically collects and logs metrics
func (s *Service) collectMetrics() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		metrics := s.GetMetrics()
		log.Printf("Metrics: %+v", metrics)
	}
}
