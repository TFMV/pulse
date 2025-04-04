package issuer

import (
	"context"
	"fmt"
	"log"

	"github.com/TFMV/pulse/proto"
	"github.com/TFMV/pulse/storage"
)

// IssuerService is a central service for issuer operations that supports storage
type IssuerService struct {
	storage storage.Storage
}

// NewIssuerService creates a new issuer service
func NewIssuerService(storage storage.Storage) *IssuerService {
	return &IssuerService{
		storage: storage,
	}
}

// WrapWithStorage wraps an existing issuer with storage capabilities
func WrapWithStorage(server proto.AuthServiceServer, storage storage.Storage) proto.AuthServiceServer {
	// If no storage is provided, return the original server
	if storage == nil {
		return server
	}

	// Create a wrapper that delegates ProcessAuth to the original server
	// but handles GetTransaction with its own implementation
	return &wrappedIssuer{
		AuthServiceServer: server,
		storage:           storage,
	}
}

// wrappedIssuer wraps an existing issuer server with storage capabilities
type wrappedIssuer struct {
	proto.AuthServiceServer
	storage storage.Storage
}

// GetTransaction implements the GetTransaction endpoint from the proto.AuthServiceServer interface
func (w *wrappedIssuer) GetTransaction(ctx context.Context, req *proto.GetTransactionRequest) (*proto.AuthRecord, error) {
	if w.storage == nil {
		return nil, fmt.Errorf("storage is not configured")
	}

	record, err := w.storage.GetTransaction(ctx, req.Stan)
	if err != nil {
		log.Printf("Failed to retrieve transaction %s: %v", req.Stan, err)
		return nil, fmt.Errorf("failed to retrieve transaction: %w", err)
	}

	if record == nil {
		log.Printf("Transaction %s not found", req.Stan)
		return nil, fmt.Errorf("transaction not found")
	}

	return record, nil
}
