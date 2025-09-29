// internal/editor/ot_manager.go
package editor

import (
	"collaborative-editor/pkg/ot"
	"log"
	"sync"
)

// OTManager manages operational transformation for a document
type OTManager struct {
	mu          sync.RWMutex
	document    *ot.Document
	pendingOps  []ot.Operation
	lastContent string
	documentID  string
}

// NewOTManager creates a new OT manager
func NewOTManager(documentID string) *OTManager {
	return &OTManager{
		document:   ot.NewDocument(),
		pendingOps: []ot.Operation{},
		documentID: documentID,
	}
}

// ProcessTextUpdate processes a text update using OT
func (m *OTManager) ProcessTextUpdate(clientID string, newContent string, clientVersion int) (string, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	log.Printf("[OT Manager] Processing update from %s, client version: %d, server version: %d",
		clientID, clientVersion, m.document.Version)

	// Generate operation from the change
	op := ot.GenerateOperation(m.lastContent, newContent, 0, clientID)
	op.Version = clientVersion

	// If client is behind, transform the operation
	if clientVersion < m.document.Version {
		log.Printf("[OT Manager] Client behind, transforming operation")
		op = m.document.TransformAgainstHistory(op)
	}

	// Apply the operation
	if err := m.document.Apply(op); err != nil {
		log.Printf("[OT Manager] Error applying operation: %v", err)
		return m.document.Content, m.document.Version, err
	}

	m.lastContent = m.document.Content
	log.Printf("[OT Manager] Document updated to version %d, content length: %d",
		m.document.Version, len(m.document.Content))

	return m.document.Content, m.document.Version, nil
}

// GetDocument returns the current document state
func (m *OTManager) GetDocument() (string, int) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.document.Content, m.document.Version
}
