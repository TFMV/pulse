package workflow

import (
	"context"
	"fmt"
	"time"

	"github.com/TFMV/pulse/proto"
	"go.temporal.io/sdk/activity"
)

// Activities encapsulates all payment workflow activities
type Activities struct {
	clients          map[string]proto.AuthServiceClient
	auditLogger      AuditLogger
	fraudAnalyzer    FraudAnalyzer
	defaultRetryOpts RetryOptions
}

// RetryOptions defines retry behavior for activities
type RetryOptions struct {
	MaxAttempts     int
	InitialInterval time.Duration
	MaxInterval     time.Duration
	BackoffCoeff    float64
}

// AuditLogger defines an interface for audit logging
type AuditLogger interface {
	LogTransaction(txnDetails map[string]interface{}) error
}

// FraudAnalyzer defines an interface for fraud analysis
type FraudAnalyzer interface {
	Analyze(request *proto.AuthRequest) (bool, string, error)
}

// NewActivities creates a new instance of payment activities
func NewActivities(
	clients map[string]proto.AuthServiceClient,
	auditLogger AuditLogger,
	fraudAnalyzer FraudAnalyzer,
) *Activities {
	return &Activities{
		clients:       clients,
		auditLogger:   auditLogger,
		fraudAnalyzer: fraudAnalyzer,
		defaultRetryOpts: RetryOptions{
			MaxAttempts:     3,
			InitialInterval: 500 * time.Millisecond,
			MaxInterval:     5 * time.Second,
			BackoffCoeff:    1.5,
		},
	}
}

// ProcessAuth sends an authorization request to a specific region
func (a *Activities) ProcessAuth(ctx context.Context, request *proto.AuthRequest, region string) (*proto.AuthResponse, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Processing auth request", "stan", request.Stan, "region", region)

	client, ok := a.clients[region]
	if !ok {
		return nil, fmt.Errorf("no client available for region %s", region)
	}

	// Add activity-specific information
	activityInfo := activity.GetInfo(ctx)
	logger.Info("Activity attempt", "attempt", activityInfo.Attempt)

	// Add region to the request
	request.Region = region

	// Process the request with the client
	start := time.Now()
	response, err := client.ProcessAuth(ctx, request)
	elapsed := time.Since(start)

	if err != nil {
		logger.Error("Failed to process auth request", "error", err)
		return nil, err
	}

	// Add processing time to the response
	response.ProcessingTimeMs = elapsed.Milliseconds()

	logger.Info("Auth request processed",
		"stan", request.Stan,
		"response_code", response.ResponseCode,
		"duration_ms", elapsed.Milliseconds())

	return response, nil
}

// CheckTransaction performs fraud check on a transaction
func (a *Activities) CheckTransaction(ctx context.Context, request *proto.AuthRequest) (bool, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Performing fraud check", "stan", request.Stan, "pan", maskPAN(request.Pan))

	if a.fraudAnalyzer == nil {
		logger.Warn("No fraud analyzer configured, skipping check")
		return true, nil // Default to passing the fraud check
	}

	// Track timing for metrics
	start := time.Now()

	// Perform the fraud analysis
	approved, reason, err := a.fraudAnalyzer.Analyze(request)

	elapsed := time.Since(start)

	if err != nil {
		logger.Error("Fraud check failed", "error", err, "duration_ms", elapsed.Milliseconds())
		return false, err
	}

	logger.Info("Fraud check completed",
		"stan", request.Stan,
		"approved", approved,
		"reason", reason,
		"duration_ms", elapsed.Milliseconds())

	return approved, nil
}

// LogTransaction records transaction details for audit purposes
func (a *Activities) LogTransaction(
	ctx context.Context,
	request *proto.AuthRequest,
	response *proto.AuthResponse,
	additionalInfo map[string]string,
) error {
	logger := activity.GetLogger(ctx)
	logger.Info("Logging transaction for audit", "stan", request.Stan)

	if a.auditLogger == nil {
		logger.Warn("No audit logger configured, skipping")
		return nil
	}

	// Prepare transaction details for logging
	txnDetails := map[string]interface{}{
		"stan":              request.Stan,
		"pan":               maskPAN(request.Pan),
		"amount":            request.Amount,
		"region":            request.Region,
		"transmission_time": request.TransmissionTime,
		"mti":               request.Mti,
		"response_code":     response.ResponseCode,
		"processing_time":   response.ProcessingTimeMs,
		"timestamp":         time.Now().Format(time.RFC3339),
		"workflow_id":       activity.GetInfo(ctx).WorkflowExecution.ID,
	}

	// Add any additional info
	for k, v := range additionalInfo {
		txnDetails[k] = v
	}

	// Log the transaction
	err := a.auditLogger.LogTransaction(txnDetails)
	if err != nil {
		logger.Error("Failed to log transaction", "error", err)
		return err
	}

	logger.Info("Transaction logged successfully", "stan", request.Stan)
	return nil
}

// Helper function to mask PAN for logging, e.g. 4111111111111111 -> 411111******1111
func maskPAN(pan string) string {
	if len(pan) <= 10 {
		return pan
	}
	return fmt.Sprintf("%s******%s", pan[:6], pan[len(pan)-4:])
}
