package workflow

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/TFMV/pulse/proto"
)

// SimpleFraudAnalyzer is a basic implementation of fraud analysis
type SimpleFraudAnalyzer struct {
	// High-risk BIN ranges (first 6 digits of card number)
	highRiskBins []string

	// High-risk amount thresholds
	amountThreshold float64

	// Rules for triggering fraud alerts
	velocityThreshold  int
	recentTransactions map[string][]time.Time // PAN -> timestamps
}

// NewSimpleFraudAnalyzer creates a new fraud analyzer with default settings
func NewSimpleFraudAnalyzer() *SimpleFraudAnalyzer {
	return &SimpleFraudAnalyzer{
		highRiskBins: []string{
			"431274", // Example high-risk BIN
			"557788", // Example high-risk BIN
		},
		amountThreshold:    1000.0, // transactions over $1000 get extra scrutiny
		velocityThreshold:  3,      // 3 transactions in short succession is suspicious
		recentTransactions: make(map[string][]time.Time),
	}
}

// Analyze performs fraud analysis on a transaction request
func (f *SimpleFraudAnalyzer) Analyze(request *proto.AuthRequest) (bool, string, error) {
	// Extract PAN prefix (BIN)
	cardBin := ""
	if len(request.Pan) >= 6 {
		cardBin = request.Pan[:6]
	}

	// Parse amount
	amountStr := strings.TrimSpace(request.Amount)
	amount, err := strconv.ParseFloat(amountStr, 64)
	if err != nil {
		return false, "Invalid amount format", err
	}

	// Check if BIN is in high-risk list
	for _, highRiskBin := range f.highRiskBins {
		if cardBin == highRiskBin && amount > f.amountThreshold/2 {
			reason := fmt.Sprintf("High-risk BIN %s with amount $%.2f", maskBin(cardBin), amount)
			return false, reason, nil
		}
	}

	// Check for high amount
	if amount > f.amountThreshold {
		// Allow, but with a note
		reason := fmt.Sprintf("High amount $%.2f requires additional verification", amount)
		return true, reason, nil
	}

	// Check velocity (multiple transactions in short time)
	if f.checkVelocity(request.Pan) {
		reason := fmt.Sprintf("Velocity check failed: too many transactions for PAN %s", maskPAN(request.Pan))
		return false, reason, nil
	}

	// No fraud detected
	return true, "Transaction passed fraud checks", nil
}

// checkVelocity checks if there are too many transactions for a PAN in a short time
func (f *SimpleFraudAnalyzer) checkVelocity(pan string) bool {
	now := time.Now()

	// Get recent transactions for this PAN
	transactions, exists := f.recentTransactions[pan]
	if !exists {
		// First transaction for this PAN
		f.recentTransactions[pan] = []time.Time{now}
		return false
	}

	// Clean up old transactions (older than 1 hour)
	var recent []time.Time
	for _, t := range transactions {
		if now.Sub(t) < time.Hour {
			recent = append(recent, t)
		}
	}

	// Add current transaction
	recent = append(recent, now)
	f.recentTransactions[pan] = recent

	// Count transactions in the last 5 minutes
	var recentCount int
	for _, t := range recent {
		if now.Sub(t) < 5*time.Minute {
			recentCount++
		}
	}

	// Trigger fraud alert if too many recent transactions
	return recentCount > f.velocityThreshold
}

// Helper for masking BIN in logs
func maskBin(bin string) string {
	if len(bin) < 4 {
		return bin
	}
	return bin[:2] + "****"
}

// FileAuditLogger is a simple implementation that logs transactions to a file
type FileAuditLogger struct {
	logDir     string
	logFile    string
	prettyJSON bool
}

// NewFileAuditLogger creates a new audit logger that writes to a file
func NewFileAuditLogger(directory string) (*FileAuditLogger, error) {
	// Create directory if it doesn't exist
	if err := os.MkdirAll(directory, 0755); err != nil {
		return nil, fmt.Errorf("failed to create audit log directory: %w", err)
	}

	// Use date-based log file
	logFile := filepath.Join(directory, fmt.Sprintf("audit-%s.log", time.Now().Format("2006-01-02")))

	return &FileAuditLogger{
		logDir:     directory,
		logFile:    logFile,
		prettyJSON: true, // human-readable JSON
	}, nil
}

// LogTransaction logs transaction details to a file
func (l *FileAuditLogger) LogTransaction(txnDetails map[string]interface{}) error {
	// Create a new logfile if it's a new day
	currentDay := time.Now().Format("2006-01-02")
	expectedLogFile := filepath.Join(l.logDir, fmt.Sprintf("audit-%s.log", currentDay))
	if expectedLogFile != l.logFile {
		l.logFile = expectedLogFile
	}

	// Open the log file in append mode
	file, err := os.OpenFile(l.logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Failed to open audit log file: %v", err)
		return err
	}
	defer file.Close()

	// Add timestamp if not present
	if _, ok := txnDetails["timestamp"]; !ok {
		txnDetails["timestamp"] = time.Now().Format(time.RFC3339)
	}

	// Marshal to JSON
	var jsonData []byte
	if l.prettyJSON {
		jsonData, err = json.MarshalIndent(txnDetails, "", "  ")
	} else {
		jsonData, err = json.Marshal(txnDetails)
	}

	if err != nil {
		log.Printf("Failed to marshal transaction details: %v", err)
		return err
	}

	// Write to log file
	if _, err := file.Write(append(jsonData, '\n')); err != nil {
		log.Printf("Failed to write to audit log: %v", err)
		return err
	}

	return nil
}
