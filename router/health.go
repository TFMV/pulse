package router

import (
	"sync"
	"time"
)

// CircuitState represents the state of a circuit breaker
type CircuitState int

const (
	// CircuitClosed means the circuit is healthy and requests flow normally
	CircuitClosed CircuitState = iota
	// CircuitOpen means the circuit is unhealthy and requests should be redirected
	CircuitOpen
	// CircuitHalfOpen means the circuit is testing if it's healthy again
	CircuitHalfOpen
)

// Constants for circuit breaker configuration
const (
	// Number of consecutive failures needed to open circuit
	FailureThreshold = 5
	// How long to keep circuit open before testing again
	ResetTimeout = 30 * time.Second
	// Allowed errors within a time window
	ErrorWindow = 60 * time.Second
)

// RegionHealth tracks the health status of a region
type RegionHealth struct {
	// Current circuit state
	State CircuitState
	// Number of consecutive failures
	ConsecutiveFailures int
	// Last time the circuit state changed
	LastStateChange time.Time
	// Timestamps of recent errors for windowed evaluation
	RecentErrors []time.Time
	// Mutex for thread safety
	mutex sync.RWMutex
}

// NewRegionHealth creates a new RegionHealth with initial state
func NewRegionHealth() *RegionHealth {
	return &RegionHealth{
		State:           CircuitClosed,
		LastStateChange: time.Now(),
		RecentErrors:    make([]time.Time, 0),
	}
}

// RecordSuccess records a successful request to the region
func (rh *RegionHealth) RecordSuccess() {
	rh.mutex.Lock()
	defer rh.mutex.Unlock()

	// Reset failures on success
	rh.ConsecutiveFailures = 0

	// If we're in half-open state and got a success, close the circuit
	if rh.State == CircuitHalfOpen {
		rh.State = CircuitClosed
		rh.LastStateChange = time.Now()
	}
}

// RecordFailure records a failed request to the region
func (rh *RegionHealth) RecordFailure() {
	rh.mutex.Lock()
	defer rh.mutex.Unlock()

	// Increment consecutive failures
	rh.ConsecutiveFailures++

	// Add to recent errors
	now := time.Now()
	rh.RecentErrors = append(rh.RecentErrors, now)

	// Clean up old errors outside the window
	var recentErrors []time.Time
	for _, t := range rh.RecentErrors {
		if now.Sub(t) <= ErrorWindow {
			recentErrors = append(recentErrors, t)
		}
	}
	rh.RecentErrors = recentErrors

	// Check if we need to open the circuit
	if rh.State == CircuitClosed && rh.ConsecutiveFailures >= FailureThreshold {
		rh.State = CircuitOpen
		rh.LastStateChange = now
	}
}

// IsHealthy returns true if the region should receive traffic
func (rh *RegionHealth) IsHealthy() bool {
	rh.mutex.RLock()
	defer rh.mutex.RUnlock()

	// Check if we should try half-open state
	if rh.State == CircuitOpen && time.Since(rh.LastStateChange) > ResetTimeout {
		// Transition to half-open to test the waters
		rh.mutex.RUnlock()
		rh.mutex.Lock()
		rh.State = CircuitHalfOpen
		rh.LastStateChange = time.Now()
		rh.mutex.Unlock()
		rh.mutex.RLock()
	}

	return rh.State == CircuitClosed || rh.State == CircuitHalfOpen
}

// GetState returns the current circuit state
func (rh *RegionHealth) GetState() CircuitState {
	rh.mutex.RLock()
	defer rh.mutex.RUnlock()
	return rh.State
}

// GetHealth returns a value between 0.0 and 1.0 representing health
func (rh *RegionHealth) GetHealth() float64 {
	rh.mutex.RLock()
	defer rh.mutex.RUnlock()

	// If circuit is open, health is 0
	if rh.State == CircuitOpen {
		return 0.0
	}

	// If no errors, health is 1.0
	if len(rh.RecentErrors) == 0 {
		return 1.0
	}

	// Calculate health based on recent errors
	// More errors = lower health
	errorRatio := float64(len(rh.RecentErrors)) / 10.0 // 10 errors = 0 health
	health := 1.0 - errorRatio
	if health < 0 {
		health = 0
	}
	return health
}
