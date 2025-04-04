package examples

import (
	"fmt"
	"log"
	"testing"

	"github.com/TFMV/pulse/proto"
	"github.com/TFMV/pulse/workflow"
)

func TestFraudAnalysis(t *testing.T) {
	// Create a fraud analyzer with default settings
	analyzer := workflow.NewSimpleFraudAnalyzer()

	// Test cases to demonstrate fraud detection capabilities
	testCases := []struct {
		name     string
		request  *proto.AuthRequest
		expected bool
		reason   string
	}{
		{
			name: "Valid Transaction",
			request: &proto.AuthRequest{
				Pan:    "4111111111111111",
				Amount: "50.00",
				Stan:   "123456",
			},
			expected: true,
			reason:   "Transaction passed fraud checks",
		},
		{
			name: "High Risk BIN with Large Amount",
			request: &proto.AuthRequest{
				Pan:    "431274111111111",
				Amount: "600.00",
				Stan:   "123457",
			},
			expected: false,
			reason:   "High-risk BIN",
		},
		{
			name: "Very Large Amount",
			request: &proto.AuthRequest{
				Pan:    "5555555555554444",
				Amount: "1200.00",
				Stan:   "123458",
			},
			expected: true, // Will pass but with a note
			reason:   "High amount",
		},
	}

	// Run through each test case
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			approved, reason, err := analyzer.Analyze(tc.request)
			if err != nil {
				t.Fatalf("Error analyzing transaction: %v", err)
			}

			log.Printf("Transaction: %s, Amount: $%s, Result: %v, Reason: %s",
				tc.request.Pan, tc.request.Amount, approved, reason)

			if approved != tc.expected {
				t.Errorf("Expected approval status %v but got %v", tc.expected, approved)
			}

			// Check reason contains expected substring
			if tc.reason != "" && !containsSubstring(reason, tc.reason) {
				t.Errorf("Expected reason to contain '%s' but got '%s'", tc.reason, reason)
			}
		})
	}

	// Test velocity check
	t.Run("Velocity Check", func(t *testing.T) {
		sameCardReq := &proto.AuthRequest{
			Pan:    "4111111111111111",
			Amount: "20.00",
			Stan:   "123460",
		}

		// Run multiple transactions with the same card in rapid succession
		results := make([]bool, 5)
		for i := 0; i < 5; i++ {
			sameCardReq.Stan = fmt.Sprintf("12346%d", i)
			approved, reason, err := analyzer.Analyze(sameCardReq)
			if err != nil {
				t.Fatalf("Error analyzing transaction: %v", err)
			}
			results[i] = approved
			log.Printf("Velocity test #%d: Approved=%v, Reason=%s", i+1, approved, reason)
		}

		// The first two transactions should pass
		if !results[0] || !results[1] {
			t.Errorf("First two transactions should be approved: %v", results)
		}

		// Transactions 3, 4, 5 should be rejected due to velocity check
		if results[2] || results[3] || results[4] {
			t.Errorf("Later transactions should be declined due to velocity: %v", results)
		}
	})
}

// Helper function to check if a string contains a substring
func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && s[:len(substr)] == substr ||
		len(s) > len(substr) && s[len(s)-len(substr):] == substr ||
		len(s) > len(substr) && len(substr) > 0 && s[1:len(s)-1] == substr[1:len(substr)-1]
}
