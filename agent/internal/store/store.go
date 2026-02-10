package store

import "time"

type Receipt struct {
	ReceiptID string
	EscrowID  string
	AmountAGC uint64
	AcceptedAt time.Time
}

// Store is a minimal placeholder for local receipt persistence.
type Store struct {
	Receipts []Receipt
}

func New() *Store {
	return &Store{Receipts: []Receipt{}}
}

func (s *Store) Add(r Receipt) {
	s.Receipts = append(s.Receipts, r)
}
