package issuer

import (
	"context"
	"log"
	"strconv"
	"time"

	"github.com/TFMV/pulse/proto"
)

// EUWestIssuer implements the authentication service for the EU West region
type EUWestIssuer struct {
	proto.UnimplementedAuthServiceServer
}

// NewEUWestIssuer creates a new EU West issuer
func NewEUWestIssuer() *EUWestIssuer {
	return &EUWestIssuer{}
}

// ProcessAuth processes an authorization request
func (i *EUWestIssuer) ProcessAuth(ctx context.Context, req *proto.AuthRequest) (*proto.AuthResponse, error) {
	start := time.Now()
	log.Printf("[EU-WEST] Processing auth request for PAN %s, STAN %s", maskPAN(req.Pan), req.Stan)

	// EU West region has higher latency (50-200ms)
	processingTime := 50 + time.Duration(time.Now().UnixNano()%150)*time.Millisecond
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

	// Business rule: Decline if amount > €400 (EU region)
	if amountVal > 400.00 {
		resp.ResponseCode = "05" // Do not honor
		log.Printf("[EU-WEST] Declining transaction %s: amount €%.2f exceeds €400 limit", req.Stan, amountVal)
	} else {
		resp.ResponseCode = "00" // Approved
		log.Printf("[EU-WEST] Approved transaction %s: amount €%.2f", req.Stan, amountVal)
	}

	// EU Region specific: Check for potential fraud based on transmission time
	if isNightTimeTransaction(req.TransmissionTime) && amountVal > 200.00 {
		resp.ResponseCode = "59" // Suspected fraud
		log.Printf("[EU-WEST] Declining transaction %s: suspicious night transaction of €%.2f", req.Stan, amountVal)
	}

	return resp, nil
}

// isNightTimeTransaction checks if the transaction occurred during night hours (11PM-5AM)
func isNightTimeTransaction(timestamp string) bool {
	// Format of timestamp is MMDDhhmmss
	if len(timestamp) < 10 {
		return false
	}

	// Extract hour (positions 4-5)
	hourStr := timestamp[4:6]
	hour, err := strconv.Atoi(hourStr)
	if err != nil {
		return false
	}

	// Consider 23:00-05:00 as night time
	return hour >= 23 || hour < 5
}
