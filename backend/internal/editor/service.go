// internal/editor/service.go
package editor

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"collaborative-editor/internal/database"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// Service represents the editor service with all its dependencies
type Service struct {
	hub      *Hub
	upgrader websocket.Upgrader
	config   *Config
	mu       sync.RWMutex
	db       *database.DB // Add database connection

	// Document storage (in-memory cache with PostgreSQL backing)
	documents map[string]*Document

	// Metrics
	metrics *Metrics
}

// Config holds service configuration
type Config struct {
	MaxMessageSize   int64
	WriteTimeout     time.Duration
	ReadTimeout      time.Duration
	PingInterval     time.Duration
	MaxClients       int
	AutoSaveInterval time.Duration // Add auto-save interval
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
	ActiveClients map[string]*Client `json:"-"`
	mu            sync.RWMutex       `json:"-"`

	// Add fields for persistence
	dirty     bool      `json:"-"` // Track if document needs saving
	lastSaved time.Time `json:"-"` // Last save time
}

// Metrics tracks service performance
type Metrics struct {
	ActiveConnections int64
	MessagesSent      int64
	MessagesReceived  int64
	DocumentsActive   int64
	DocumentsSaved    int64 // Add saved documents counter

	mu sync.RWMutex
}

// NewService creates a new editor service with database connection
func NewService(cfg *Config, db *database.DB) *Service {
	if cfg == nil {
		cfg = &Config{
			MaxMessageSize:   512 * 1024, // 512KB
			WriteTimeout:     10 * time.Second,
			ReadTimeout:      60 * time.Second,
			PingInterval:     30 * time.Second,
			MaxClients:       1000,
			AutoSaveInterval: 30 * time.Second, // Auto-save every 30 seconds
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
		db:        db, // Store database connection
	}
}

// Start initializes and starts the service
func (s *Service) Start() error {
	log.Println("Starting editor service...")

	// Start the hub
	go s.hub.run()

	// Start metrics collector
	go s.collectMetrics()

	// Start auto-save goroutine
	go s.autoSaveLoop()

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

	// Close database connection
	if s.db != nil {
		s.db.Close()
	}

	log.Println("Editor service shut down complete")
}

// HandleWebSocket handles WebSocket upgrade requests (no changes needed)
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

	// Send document state
	s.sendDocumentState(client, docID)

	log.Printf("Client %s connected for document %s", client.id, docID)
}

// GetDocument retrieves a document by ID (with database loading)
func (s *Service) GetDocument(id string) (*Document, error) {
	s.mu.RLock()
	doc, exists := s.documents[id]
	s.mu.RUnlock()

	if exists {
		return doc, nil
	}

	// Try to load from database
	var dbDoc *database.Document
	if s.db != nil {
		dbDoc, _ = s.db.GetDocument(id)
	}

	// Create document (either from DB or new)
	if dbDoc != nil {
		// Load from database
		doc = &Document{
			ID:            dbDoc.ID,
			Content:       dbDoc.Content,
			Version:       dbDoc.Version,
			CreatedAt:     dbDoc.CreatedAt,
			UpdatedAt:     dbDoc.UpdatedAt,
			OTManager:     NewOTManager(id),
			CursorManager: NewCursorManager(),
			ActiveClients: make(map[string]*Client),
			dirty:         false,
			lastSaved:     time.Now(),
		}
		log.Printf("Loaded document %s from database (version %d)", id, doc.Version)
	} else {
		// Create new document
		doc = &Document{
			ID:            id,
			Content:       "",
			Version:       1,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			OTManager:     NewOTManager(id),
			CursorManager: NewCursorManager(),
			ActiveClients: make(map[string]*Client),
			dirty:         true, // New document needs saving
			lastSaved:     time.Now(),
		}

		// Save new document to database
		if s.db != nil {
			s.db.CreateDocument(id, doc.Content)
		}
	}

	s.mu.Lock()
	s.documents[id] = doc
	s.mu.Unlock()

	// Update metrics
	s.metrics.mu.Lock()
	s.metrics.DocumentsActive++
	s.metrics.mu.Unlock()

	return doc, nil
}

// UpdateDocument updates a document's content (with dirty flag)
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
			doc.dirty = true // Mark as needing save
			doc.mu.Unlock()
			return newContent, newVersion, nil
		}
	}

	// Fallback to simple versioning
	doc.mu.Lock()
	doc.Content = content
	doc.Version++
	doc.UpdatedAt = time.Now()
	doc.dirty = true // Mark as needing save
	newVersion := doc.Version
	doc.mu.Unlock()

	return content, newVersion, nil
}

// SaveDocument saves a specific document to database
func (s *Service) SaveDocument(docID string) error {
	s.mu.RLock()
	doc, exists := s.documents[docID]
	s.mu.RUnlock()

	if !exists || !doc.dirty {
		return nil // Nothing to save
	}

	doc.mu.RLock()
	content := doc.Content
	version := doc.Version
	doc.mu.RUnlock()

	// Save to database
	if s.db != nil {
		err := s.db.UpdateDocument(docID, content, version)
		if err != nil {
			log.Printf("Error saving document %s: %v", docID, err)
			return err
		}

		// Save to history (optional)
		s.db.SaveDocumentHistory(docID, content, "system", version)
	}

	doc.mu.Lock()
	doc.dirty = false
	doc.lastSaved = time.Now()
	doc.mu.Unlock()

	// Update metrics
	s.metrics.mu.Lock()
	s.metrics.DocumentsSaved++
	s.metrics.mu.Unlock()

	log.Printf("Saved document %s to database (version %d)", docID, version)
	return nil
}

// autoSaveLoop runs in background and saves dirty documents periodically
func (s *Service) autoSaveLoop() {
	ticker := time.NewTicker(s.config.AutoSaveInterval)
	defer ticker.Stop()

	for range ticker.C {
		s.autoSave()
	}
}

// autoSave saves all dirty documents
func (s *Service) autoSave() {
	s.mu.RLock()
	docIDs := make([]string, 0)
	for id, doc := range s.documents {
		doc.mu.RLock()
		needsSave := doc.dirty && time.Since(doc.lastSaved) > 10*time.Second
		doc.mu.RUnlock()

		if needsSave {
			docIDs = append(docIDs, id)
		}
	}
	s.mu.RUnlock()

	for _, id := range docIDs {
		if err := s.SaveDocument(id); err != nil {
			log.Printf("Auto-save failed for document %s: %v", id, err)
		}
	}
}

// BroadcastToDocument sends a message to all clients editing a document (no changes)
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

// sendDocumentState sends the current document state to a client (no changes)
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

// RemoveClientFromDocument removes a client from a document's active clients (no changes)
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
		// Save document before removing from active list
		s.SaveDocument(client.documentID)

		s.metrics.mu.Lock()
		s.metrics.DocumentsActive--
		s.metrics.mu.Unlock()
	}
}

// GetMetrics returns current service metrics (updated)
func (s *Service) GetMetrics() map[string]interface{} {
	s.metrics.mu.RLock()
	defer s.metrics.mu.RUnlock()

	return map[string]interface{}{
		"active_connections": s.metrics.ActiveConnections,
		"messages_sent":      s.metrics.MessagesSent,
		"messages_received":  s.metrics.MessagesReceived,
		"documents_active":   s.metrics.DocumentsActive,
		"documents_saved":    s.metrics.DocumentsSaved,
		"hub_clients":        len(s.hub.clients),
	}
}

// initialize performs any required initialization
func (s *Service) initialize() error {
	// Database is already initialized in NewService
	log.Println("Service initialization complete")
	return nil
}

// savePendingDocuments saves any documents with pending changes (updated)
func (s *Service) savePendingDocuments() {
	s.mu.RLock()
	docIDs := make([]string, 0)
	for id, doc := range s.documents {
		if doc.dirty {
			docIDs = append(docIDs, id)
		}
	}
	s.mu.RUnlock()

	for _, id := range docIDs {
		s.SaveDocument(id)
	}

	log.Printf("Saved %d pending documents on shutdown", len(docIDs))
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
