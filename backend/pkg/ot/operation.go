// Package ot implements Operational Transformation for real-time collaborative editing
package ot

import (
	"fmt"
	"log"
)

// OpType represents the type of operation
type OpType int

const (
	OpInsert OpType = iota
	OpDelete
	OpRetain
)

// Operation represents a single edit operation
type Operation struct {
	Type     OpType `json:"type"`
	Position int    `json:"position"`
	Content  string `json:"content,omitempty"` // For insert
	Length   int    `json:"length,omitempty"`  // For delete/retain
	ClientID string `json:"clientId"`
	Version  int    `json:"version"`
}

// Document represents the document state with OT
type Document struct {
	Content         string
	Version         int
	PendingOps      []Operation
	AcknowledgedOps []Operation
}

// NewDocument creates a new document
func NewDocument() *Document {
	return &Document{
		Content:         "",
		Version:         0,
		PendingOps:      []Operation{},
		AcknowledgedOps: []Operation{},
	}
}

// Transform transforms op1 against op2 (op1 happens "before" op2)
// Returns (op1', op2') where op1' and op2' can be applied to achieve convergence
func Transform(op1, op2 Operation) (Operation, Operation) {
	// Both operations start from the same document state
	// We need to transform them so they can be applied in either order

	log.Printf("[OT] Transforming op1 (type:%d pos:%d) against op2 (type:%d pos:%d)",
		op1.Type, op1.Position, op2.Type, op2.Position)

	op1Prime := op1
	op2Prime := op2

	switch op1.Type {
	case OpInsert:
		switch op2.Type {
		case OpInsert:
			op1Prime, op2Prime = transformInsertInsert(op1, op2)
		case OpDelete:
			op1Prime, op2Prime = transformInsertDelete(op1, op2)
		}
	case OpDelete:
		switch op2.Type {
		case OpInsert:
			op2Prime, op1Prime = transformInsertDelete(op2, op1)
		case OpDelete:
			op1Prime, op2Prime = transformDeleteDelete(op1, op2)
		}
	}

	return op1Prime, op2Prime
}

// transformInsertInsert handles two concurrent insertions
func transformInsertInsert(op1, op2 Operation) (Operation, Operation) {
	op1Prime := op1
	op2Prime := op2

	if op1.Position < op2.Position {
		// op1 happens before op2's position, so op2 needs to shift right
		op2Prime.Position += len(op1.Content)
	} else if op1.Position > op2.Position {
		// op2 happens before op1's position, so op1 needs to shift right
		op1Prime.Position += len(op2.Content)
	} else {
		// Same position - use client ID as tiebreaker for consistency
		if op1.ClientID < op2.ClientID {
			op2Prime.Position += len(op1.Content)
		} else {
			op1Prime.Position += len(op2.Content)
		}
	}

	return op1Prime, op2Prime
}

// transformInsertDelete handles insert vs delete
func transformInsertDelete(insert, delete Operation) (Operation, Operation) {
	insertPrime := insert
	deletePrime := delete

	if insert.Position <= delete.Position {
		// Insert happens before delete position
		deletePrime.Position += len(insert.Content)
	} else if insert.Position >= delete.Position+delete.Length {
		// Insert happens after deleted range
		insertPrime.Position -= delete.Length
	} else {
		// Insert happens within deleted range
		// Split the delete into two parts
		insertPrime.Position = delete.Position
	}

	return insertPrime, deletePrime
}

// transformDeleteDelete handles two concurrent deletions
func transformDeleteDelete(op1, op2 Operation) (Operation, Operation) {
	op1Prime := op1
	op2Prime := op2

	if op1.Position+op1.Length <= op2.Position {
		// op1 deletes before op2
		op2Prime.Position -= op1.Length
	} else if op2.Position+op2.Length <= op1.Position {
		// op2 deletes before op1
		op1Prime.Position -= op2.Length
	} else {
		// Overlapping deletes
		if op1.Position < op2.Position {
			// op1 starts first
			overlap := (op1.Position + op1.Length) - op2.Position
			op1Prime.Length -= overlap
			op2Prime.Position = op1.Position
			op2Prime.Length -= overlap
		} else {
			// op2 starts first
			overlap := (op2.Position + op2.Length) - op1.Position
			op2Prime.Length -= overlap
			op1Prime.Position = op2.Position
			op1Prime.Length -= overlap
		}
	}

	return op1Prime, op2Prime
}

// Apply applies an operation to the document
func (d *Document) Apply(op Operation) error {
	log.Printf("[OT] Applying operation type:%d pos:%d to doc version:%d",
		op.Type, op.Position, d.Version)

	switch op.Type {
	case OpInsert:
		if op.Position < 0 || op.Position > len(d.Content) {
			return fmt.Errorf("invalid insert position: %d (content length: %d)",
				op.Position, len(d.Content))
		}
		d.Content = d.Content[:op.Position] + op.Content + d.Content[op.Position:]

	case OpDelete:
		if op.Position < 0 || op.Position+op.Length > len(d.Content) {
			return fmt.Errorf("invalid delete range: %d-%d (content length: %d)",
				op.Position, op.Position+op.Length, len(d.Content))
		}
		d.Content = d.Content[:op.Position] + d.Content[op.Position+op.Length:]
	}

	d.Version++
	d.AcknowledgedOps = append(d.AcknowledgedOps, op)

	return nil
}

// TransformAgainstHistory transforms an operation against all pending operations
func (d *Document) TransformAgainstHistory(op Operation) Operation {
	transformed := op

	for _, histOp := range d.PendingOps {
		if histOp.ClientID != op.ClientID {
			transformed, _ = Transform(transformed, histOp)
		}
	}

	return transformed
}

// GenerateOperation generates an operation from old content to new content
func GenerateOperation(oldContent, newContent string, position int, clientID string) Operation {
	oldLen := len(oldContent)
	newLen := len(newContent)

	if newLen > oldLen {
		// Insertion
		insertLen := newLen - oldLen
		// Find where the insertion happened
		for i := 0; i < oldLen && i < newLen; i++ {
			if oldContent[i] != newContent[i] {
				return Operation{
					Type:     OpInsert,
					Position: i,
					Content:  newContent[i : i+insertLen],
					ClientID: clientID,
				}
			}
		}
		// Insertion at the end
		return Operation{
			Type:     OpInsert,
			Position: oldLen,
			Content:  newContent[oldLen:],
			ClientID: clientID,
		}
	} else if oldLen > newLen {
		// Deletion
		deleteLen := oldLen - newLen
		// Find where the deletion happened
		for i := 0; i < newLen; i++ {
			if oldContent[i] != newContent[i] {
				return Operation{
					Type:     OpDelete,
					Position: i,
					Length:   deleteLen,
					ClientID: clientID,
				}
			}
		}
		// Deletion at the end
		return Operation{
			Type:     OpDelete,
			Position: newLen,
			Length:   deleteLen,
			ClientID: clientID,
		}
	}

	// No change or replacement (treat as delete + insert)
	return Operation{Type: OpRetain}
}
