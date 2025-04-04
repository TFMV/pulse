package issuer

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/TFMV/pulse/proto"
)

// USEastIssuer implements the authentication service for the US East region
type USEastIssuer struct {
	proto.UnimplementedAuthServiceServer
}

// NewUSEastIssuer creates a new US East issuer
func NewUSEastIssuer() *USEastIssuer {
	return &USEastIssuer{}
}

// ProcessAuth processes an authorization request
func (i *USEastIssuer) ProcessAuth(ctx context.Context, req *proto.AuthRequest) (*proto.AuthResponse, error) {
	start := time.Now()
	log.Printf("[US-EAST] Processing auth request for PAN %s, STAN %s", maskPAN(req.Pan), req.Stan)

	// Simulate processing time (10-100ms)
	processingTime := 10 + time.Duration(time.Now().UnixNano()%90)*time.Millisecond
	time.Sleep(processingTime)

	// Create the response
	resp := &proto.AuthResponse{
		Mti:              "0110",
		Pan:              req.Pan,
		Amount:           req.Amount,
		TransmissionTime: req.TransmissionTime,
		Stan:             req.Stan,
		ProcessingTimeMs: time.Since(start).Milliseconds(),
	}

	// Apply business logic
	amountVal, err := strconv.ParseFloat(req.Amount, 64)
	if err != nil {
		resp.ResponseCode = "12" // Invalid transaction
		return resp, nil
	}

	// Business rule: Decline if amount > $500
	if amountVal > 500.00 {
		resp.ResponseCode = "05" // Do not honor
		log.Printf("[US-EAST] Declining transaction %s: amount $%.2f exceeds $500 limit", req.Stan, amountVal)
	} else {
		resp.ResponseCode = "00" // Approved
		log.Printf("[US-EAST] Approved transaction %s: amount $%.2f", req.Stan, amountVal)
	}

	// Additional business logic: Decline transaction with PAN ending in 0
	if req.Pan[len(req.Pan)-1:] == "0" {
		resp.ResponseCode = "14" // Invalid card number
		log.Printf("[US-EAST] Declining transaction %s: PAN ending in 0", req.Stan)
	}

	return resp, nil
}

// maskPAN masks the PAN for logging, e.g., 4111111111111111 -> 411111******1111
func maskPAN(pan string) string {
	if len(pan) <= 10 {
		return pan
	}
	return fmt.Sprintf("%s******%s", pan[:6], pan[len(pan)-4:])
}
