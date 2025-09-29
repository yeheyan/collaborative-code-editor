// internal/editor/cursor.go
package editor

import (
	"sync"
	"time"
)

// CursorPosition represents a user's cursor position in a document
type CursorPosition struct {
	ClientID  string    `json:"clientId"`
	Username  string    `json:"username"`
	Position  int       `json:"position"`
	Color     string    `json:"color"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// SelectionRange represents a text selection
type SelectionRange struct {
	ClientID string `json:"clientId"`
	Username string `json:"username"`
	Start    int    `json:"start"`
	End      int    `json:"end"`
	Color    string `json:"color"`
}

// CursorManager manages cursor positions for a document
type CursorManager struct {
	mu         sync.RWMutex
	cursors    map[string]*CursorPosition
	selections map[string]*SelectionRange
}

// NewCursorManager creates a new cursor manager
func NewCursorManager() *CursorManager {
	return &CursorManager{
		cursors:    make(map[string]*CursorPosition),
		selections: make(map[string]*SelectionRange),
	}
}

// UpdateCursorPosition updates a client's cursor position
func (cm *CursorManager) UpdateCursorPosition(clientID, username, color string, position int) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.cursors[clientID] = &CursorPosition{
		ClientID:  clientID,
		Username:  username,
		Position:  position,
		Color:     color,
		UpdatedAt: time.Now(),
	}
}

// UpdateSelection updates a client's text selection
func (cm *CursorManager) UpdateSelection(clientID, username, color string, start, end int) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if start == end {
		// No selection, remove it
		delete(cm.selections, clientID)
	} else {
		cm.selections[clientID] = &SelectionRange{
			ClientID: clientID,
			Username: username,
			Start:    start,
			End:      end,
			Color:    color,
		}
	}
}

// RemoveClient removes a client's cursor and selection
func (cm *CursorManager) RemoveClient(clientID string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	delete(cm.cursors, clientID)
	delete(cm.selections, clientID)
}

// GetAllCursors returns all cursor positions except for the requesting client
func (cm *CursorManager) GetAllCursors(excludeClientID string) []CursorPosition {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	var positions []CursorPosition
	for id, cursor := range cm.cursors {
		if id != excludeClientID {
			positions = append(positions, *cursor)
		}
	}
	return positions
}

// GetAllSelections returns all selections except for the requesting client
func (cm *CursorManager) GetAllSelections(excludeClientID string) []SelectionRange {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	var selections []SelectionRange
	for id, selection := range cm.selections {
		if id != excludeClientID {
			selections = append(selections, *selection)
		}
	}
	return selections
}

// CleanupStale removes cursor positions that haven't been updated recently
func (cm *CursorManager) CleanupStale(timeout time.Duration) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	now := time.Now()
	for id, cursor := range cm.cursors {
		if now.Sub(cursor.UpdatedAt) > timeout {
			delete(cm.cursors, id)
			delete(cm.selections, id)
		}
	}
}
