package storage

import (
	"context"
	"time"

	"github.com/TFMV/pulse/proto"
)

// Storage defines the interface for persistence operations
type Storage interface {
	// SaveAuthorization persists the authorization request and result
	SaveAuthorization(ctx context.Context, auth *proto.AuthRequest, region string, approved bool) error

	// GetTransaction retrieves a transaction by STAN
	GetTransaction(ctx context.Context, stan string) (*proto.AuthRecord, error)

	// Close closes the storage connection
	Close() error
}

// AuthRecord represents a stored transaction record
type AuthRecord struct {
	Stan             string    `json:"stan"`
	Pan              string    `json:"pan"`
	Amount           string    `json:"amount"`
	Region           string    `json:"region"`
	Approved         bool      `json:"approved"`
	TransmissionTime string    `json:"transmission_time"`
	InsertedAt       time.Time `json:"inserted_at"`
}

// ToProto converts an AuthRecord to a proto.AuthRecord
func (a *AuthRecord) ToProto() *proto.AuthRecord {
	return &proto.AuthRecord{
		Stan:             a.Stan,
		Pan:              a.Pan,
		Amount:           a.Amount,
		Region:           a.Region,
		Approved:         a.Approved,
		TransmissionTime: a.TransmissionTime,
		InsertedAt:       a.InsertedAt.Format(time.RFC3339),
	}
}
