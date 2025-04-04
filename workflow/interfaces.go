package workflow

import (
	"context"
	"time"

	"github.com/TFMV/pulse/proto"
)

// TransactionWorkflow defines the payment transaction workflow
type TransactionWorkflow interface {
	// Execute runs the payment workflow for a given authorization request
	Execute(ctx context.Context, request *proto.AuthRequest) (*proto.AuthResponse, error)
}

// FraudCheckWorkflow defines a workflow for detailed fraud checks
type FraudCheckWorkflow interface {
	// Execute runs fraud analysis on a transaction
	Execute(ctx context.Context, request *proto.AuthRequest) (bool, error)
}

// RetryableAuthActivity defines the activity for retryable authorization processing
type RetryableAuthActivity interface {
	// ProcessAuth sends an auth request to a specific region with retry logic
	ProcessAuth(ctx context.Context, request *proto.AuthRequest, region string) (*proto.AuthResponse, error)
}

// FraudCheckActivity defines the activity for fraud checking
type FraudCheckActivity interface {
	// CheckTransaction performs fraud analysis on a transaction
	CheckTransaction(ctx context.Context, request *proto.AuthRequest) (bool, error)
}

// AuditActivity defines the activity for audit logging
type AuditActivity interface {
	// LogTransaction records transaction details for audit purposes
	LogTransaction(ctx context.Context, request *proto.AuthRequest, response *proto.AuthResponse, additionalInfo map[string]string) error
}

// TransactionOptions contains options for transaction processing workflows
type TransactionOptions struct {
	// EnableFraudCheck enables additional fraud checking
	EnableFraudCheck bool

	// MaxRetries sets the maximum number of retry attempts
	MaxRetries int

	// RetryInterval sets the base interval between retries
	RetryInterval time.Duration

	// FraudCheckTimeout sets the timeout for fraud check operations
	FraudCheckTimeout time.Duration
}
