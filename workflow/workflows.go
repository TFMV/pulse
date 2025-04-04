package workflow

import (
	"time"

	"github.com/TFMV/pulse/proto"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// PaymentWorkflow implements the transaction workflow
type PaymentWorkflow struct {
	// Default options for the payment workflow
	defaultOptions TransactionOptions
	// Router region determination function
	getRegionFunc func(string) string
}

// NewPaymentWorkflow creates a new payment workflow with default options
func NewPaymentWorkflow(getRegionFunc func(string) string) *PaymentWorkflow {
	return &PaymentWorkflow{
		defaultOptions: TransactionOptions{
			EnableFraudCheck:  true,
			MaxRetries:        3,
			RetryInterval:     500 * time.Millisecond,
			FraudCheckTimeout: 2 * time.Second,
		},
		getRegionFunc: getRegionFunc,
	}
}

// Execute runs the payment transaction workflow
func (w *PaymentWorkflow) Execute(ctx workflow.Context, request *proto.AuthRequest) (*proto.AuthResponse, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("Starting transaction workflow", "stan", request.Stan, "pan_prefix", request.Pan[:6])

	// Setup workflow options
	options := w.defaultOptions
	retryPolicy := &temporal.RetryPolicy{
		InitialInterval:    options.RetryInterval,
		BackoffCoefficient: 1.5,
		MaximumInterval:    options.RetryInterval * 10,
		MaximumAttempts:    int32(options.MaxRetries),
	}

	// Record workflow start in search attributes
	err := workflow.UpsertSearchAttributes(ctx, map[string]interface{}{
		"TransactionID": request.Stan,
		"CardBIN":       request.Pan[:6],
		"Amount":        request.Amount,
	})
	if err != nil {
		logger.Warn("Failed to upsert search attributes", "error", err)
	}

	// Determine the target region for the transaction
	var region string
	if request.Region != "" {
		region = request.Region
	} else if w.getRegionFunc != nil {
		region = w.getRegionFunc(request.Pan)
	} else {
		region = "us-east" // Default fallback
	}

	// Step 1: Run fraud check if enabled
	if options.EnableFraudCheck {
		var fraudCheckResult bool
		fraudCheckCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			StartToCloseTimeout: options.FraudCheckTimeout,
			RetryPolicy:         retryPolicy,
		})

		logger.Info("Executing fraud check activity", "stan", request.Stan)
		err := workflow.ExecuteActivity(fraudCheckCtx, "CheckTransaction", request).Get(ctx, &fraudCheckResult)
		if err != nil {
			logger.Error("Fraud check failed", "error", err)
			// Continue with the transaction but flag that fraud check failed
			workflow.UpsertSearchAttributes(ctx, map[string]interface{}{
				"FraudCheckStatus": "ERROR",
			})
		} else if !fraudCheckResult {
			logger.Info("Transaction rejected by fraud check", "stan", request.Stan)
			workflow.UpsertSearchAttributes(ctx, map[string]interface{}{
				"FraudCheckStatus": "REJECTED",
			})

			// Create a declined response for fraud
			response := &proto.AuthResponse{
				Mti:              getResponseMTI(request.Mti),
				Pan:              request.Pan,
				Amount:           request.Amount,
				TransmissionTime: request.TransmissionTime,
				Stan:             request.Stan,
				ResponseCode:     "59", // Fraud suspicion response code
			}

			// Log the declined transaction
			additionalInfo := map[string]string{"decline_reason": "fraud_check"}
			logCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
				StartToCloseTimeout: 10 * time.Second,
			})
			workflow.ExecuteActivity(logCtx, "LogTransaction", request, response, additionalInfo)

			return response, nil
		} else {
			workflow.UpsertSearchAttributes(ctx, map[string]interface{}{
				"FraudCheckStatus": "APPROVED",
			})
		}
	}

	// Step 2: Process the authorization with retries
	var response *proto.AuthResponse
	authCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
		RetryPolicy:         retryPolicy,
	})

	logger.Info("Executing auth processing activity", "stan", request.Stan, "region", region)
	err = workflow.ExecuteActivity(authCtx, "ProcessAuth", request, region).Get(ctx, &response)

	if err != nil {
		logger.Error("Authorization failed after retries", "error", err)

		// Create a declined response for technical failure
		response = &proto.AuthResponse{
			Mti:              getResponseMTI(request.Mti),
			Pan:              request.Pan,
			Amount:           request.Amount,
			TransmissionTime: request.TransmissionTime,
			Stan:             request.Stan,
			ResponseCode:     "96", // System malfunction
		}

		// Log the failed transaction
		additionalInfo := map[string]string{
			"error":        err.Error(),
			"decline_type": "technical_failure",
		}
		logCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			StartToCloseTimeout: 10 * time.Second,
		})
		workflow.ExecuteActivity(logCtx, "LogTransaction", request, response, additionalInfo)

		return response, nil
	}

	// Step 3: Log the transaction for audit purposes
	logCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
	})

	additionalInfo := map[string]string{
		"workflow_type": "standard",
	}

	if response.ResponseCode == "00" {
		additionalInfo["transaction_status"] = "approved"
	} else {
		additionalInfo["transaction_status"] = "declined"
		additionalInfo["decline_code"] = response.ResponseCode
	}

	workflow.ExecuteActivity(logCtx, "LogTransaction", request, response, additionalInfo)

	// Record the final workflow result
	transactionStatus := "DECLINED"
	if response.ResponseCode == "00" {
		transactionStatus = "APPROVED"
	}

	workflow.UpsertSearchAttributes(ctx, map[string]interface{}{
		"TransactionStatus": transactionStatus,
		"ResponseCode":      response.ResponseCode,
	})

	logger.Info("Transaction workflow completed",
		"stan", request.Stan,
		"response_code", response.ResponseCode)

	return response, nil
}

// getResponseMTI converts a request MTI to a response MTI (e.g., 0100 -> 0110)
func getResponseMTI(requestMTI string) string {
	if len(requestMTI) != 4 {
		return "0110" // Default response MTI
	}

	// The response MTI typically changes the third digit from 0->1
	return requestMTI[:2] + "1" + requestMTI[3:]
}
