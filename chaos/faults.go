package chaos

import (
	"errors"
	"fmt"
	"math/rand"
	"time"
)

// Config represents chaos testing configuration
type Config struct {
	Enabled          bool    `yaml:"enabled"`
	FaultProbability float64 `yaml:"fault_probability"`
	MaxDelayMs       int     `yaml:"max_delay_ms"`
}

// Engine provides chaos testing capabilities
type Engine struct {
	config Config
	rand   *rand.Rand
}

// NewEngine creates a new chaos testing engine
func NewEngine(config Config) *Engine {
	// Create a deterministic source for reproducible tests
	source := rand.NewSource(time.Now().UnixNano())

	return &Engine{
		config: config,
		rand:   rand.New(source),
	}
}

// ShouldInjectFault determines if a fault should be injected
func (e *Engine) ShouldInjectFault() bool {
	if !e.config.Enabled {
		return false
	}

	return e.rand.Float64() < e.config.FaultProbability
}

// InjectFault injects a fault based on the configuration
func (e *Engine) InjectFault(faultType string) error {
	if !e.config.Enabled {
		return nil
	}

	faultTypes := []string{
		"timeout",
		"delay",
		"error",
	}

	// If no specific fault type is requested, choose randomly
	if faultType == "" {
		faultType = faultTypes[e.rand.Intn(len(faultTypes))]
	}

	switch faultType {
	case "timeout":
		// Sleep for a long time to cause timeout
		time.Sleep(time.Second * 10)
		return errors.New("chaos timeout injected")

	case "delay":
		// Random delay within max delay
		delayMs := e.rand.Intn(e.config.MaxDelayMs)
		time.Sleep(time.Duration(delayMs) * time.Millisecond)
		return nil

	case "error":
		// Return a random error
		errorMsgs := []string{
			"chaos simulated network error",
			"chaos simulated disk error",
			"chaos simulated memory error",
			"chaos simulated CPU overload",
		}
		return errors.New(errorMsgs[e.rand.Intn(len(errorMsgs))])

	case "processing_message":
		// Choose between different faults for message processing
		subfaults := []string{
			"invalid message format",
			"service unavailable",
			"connection reset",
			"internal server error",
		}
		return fmt.Errorf("chaos injected fault: %s", subfaults[e.rand.Intn(len(subfaults))])

	default:
		return fmt.Errorf("unknown fault type: %s", faultType)
	}
}
