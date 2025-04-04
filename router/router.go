package router

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TFMV/pulse/chaos"
	"github.com/TFMV/pulse/metrics"
	"github.com/moov-io/iso8583"
	"github.com/moov-io/iso8583/encoding"
	"github.com/moov-io/iso8583/field"
	"github.com/moov-io/iso8583/prefix"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/TFMV/pulse/proto"
	"github.com/TFMV/pulse/storage"
)

// Config holds router configuration
type Config struct {
	BinRoutes     map[string]string       `yaml:"bin_routes"`
	DefaultRegion string                  `yaml:"default_region"`
	Regions       map[string]RegionConfig `yaml:"regions"`
	FailoverMap   map[string]string       `yaml:"failover_map"` // Maps primary region to fallback region
}

// RegionConfig holds configuration for a specific region
type RegionConfig struct {
	Host      string `yaml:"host"`
	Port      int    `yaml:"port"`
	TimeoutMs int    `yaml:"timeout_ms"`
}

// Router handles routing ISO8583 messages to the appropriate regional processors
type Router struct {
	config              Config
	connections         map[string]*grpc.ClientConn
	clients             map[string]proto.AuthServiceClient
	chaosEngine         *chaos.Engine
	spec                *iso8583.MessageSpec
	regionHealth        map[string]*RegionHealth
	metrics             *metrics.Metrics
	healthMutex         sync.RWMutex
	healthCheckInterval time.Duration
	stopHealthCheck     chan struct{}
	storage             storage.Storage
}

// NewRouter creates a new router with the given configuration
func NewRouter(config Config, chaosEngine *chaos.Engine, metricsCollector *metrics.Metrics, storage storage.Storage) *Router {
	// Create a message spec for ISO8583 messages
	spec := &iso8583.MessageSpec{
		Name: "ISO 8583 v1987",
		Fields: map[int]field.Field{
			0:  field.NewString(field.NewSpec(4, "Message Type Indicator", encoding.ASCII, prefix.None.Fixed)),
			2:  field.NewString(field.NewSpec(19, "Primary Account Number", encoding.ASCII, prefix.None.Fixed)),
			4:  field.NewString(field.NewSpec(12, "Amount, Transaction", encoding.ASCII, prefix.None.Fixed)),
			7:  field.NewString(field.NewSpec(10, "Transmission Date and Time", encoding.ASCII, prefix.None.Fixed)),
			11: field.NewString(field.NewSpec(6, "System Trace Audit Number", encoding.ASCII, prefix.None.Fixed)),
			39: field.NewString(field.NewSpec(2, "Response Code", encoding.ASCII, prefix.None.Fixed)),
		},
	}

	// Initialize health status for each region
	regionHealth := make(map[string]*RegionHealth)
	for region := range config.Regions {
		regionHealth[region] = NewRegionHealth()

		// Set initial health status in Prometheus
		if metricsCollector != nil {
			metricsCollector.RegionHealthStatus.WithLabelValues(region).Set(1.0)
		}
	}

	return &Router{
		config:              config,
		connections:         make(map[string]*grpc.ClientConn),
		clients:             make(map[string]proto.AuthServiceClient),
		chaosEngine:         chaosEngine,
		spec:                spec,
		regionHealth:        regionHealth,
		metrics:             metricsCollector,
		healthCheckInterval: 10 * time.Second,
		stopHealthCheck:     make(chan struct{}),
		storage:             storage,
	}
}

// Initialize establishes connections to all regional services and starts health monitoring
func (r *Router) Initialize() error {
	for region, cfg := range r.config.Regions {
		address := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
		conn, err := grpc.Dial(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect to %s region: %w", region, err)
		}

		r.connections[region] = conn
		r.clients[region] = proto.NewAuthServiceClient(conn)
		log.Printf("Connected to %s region at %s", region, address)
	}

	// Start background health check
	go r.runPeriodicHealthCheck()

	return nil
}

// runPeriodicHealthCheck performs regular health checks on all regions
func (r *Router) runPeriodicHealthCheck() {
	ticker := time.NewTicker(r.healthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.checkAllRegionsHealth()
		case <-r.stopHealthCheck:
			return
		}
	}
}

// checkAllRegionsHealth performs a health check on all regions
func (r *Router) checkAllRegionsHealth() {
	for region := range r.config.Regions {
		go func(reg string) {
			// Create a simple echo request to check health
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			client, ok := r.clients[reg]
			if !ok {
				log.Printf("No client for region %s", reg)
				return
			}

			// Simple echo request with minimal data
			req := &proto.AuthRequest{
				Mti:    "0800", // Network management message
				Stan:   fmt.Sprintf("%06d", time.Now().Unix()%1000000),
				Region: reg,
			}

			start := time.Now()
			_, err := client.ProcessAuth(ctx, req)
			latency := time.Since(start)

			r.healthMutex.Lock()
			defer r.healthMutex.Unlock()

			// Update health status based on response
			health := r.regionHealth[reg]
			if err != nil {
				health.RecordFailure()
				if r.metrics != nil {
					r.metrics.ErrorCount.WithLabelValues(reg, "health_check").Inc()
				}
				log.Printf("Health check failed for region %s: %v", reg, err)
			} else {
				health.RecordSuccess()
				if r.metrics != nil {
					r.metrics.ResponseLatency.WithLabelValues(reg, "0800").Observe(latency.Seconds())
				}
			}

			// Update Prometheus metric
			if r.metrics != nil {
				healthValue := 0.0
				if health.IsHealthy() {
					healthValue = health.GetHealth()
				}
				r.metrics.RegionHealthStatus.WithLabelValues(reg).Set(healthValue)
			}
		}(region)
	}
}

// Close stops health monitoring and closes all connections
func (r *Router) Close() {
	// Stop health check goroutine
	close(r.stopHealthCheck)

	for region, conn := range r.connections {
		if err := conn.Close(); err != nil {
			log.Printf("Error closing connection to %s: %v", region, err)
		}
	}
}

// HandleMessage implements the iso.MessageHandler interface
func (r *Router) HandleMessage(ctx context.Context, message *iso8583.Message) (*iso8583.Message, error) {
	// Check if we should inject chaos
	if r.chaosEngine != nil && r.chaosEngine.ShouldInjectFault() {
		return nil, r.chaosEngine.InjectFault("processing_message")
	}

	// Convert ISO message to AuthRequest
	authRequest, err := r.isoToAuthRequest(message)
	if err != nil {
		if r.metrics != nil {
			r.metrics.ErrorCount.WithLabelValues("unknown", "parse_request").Inc()
		}
		return nil, fmt.Errorf("failed to convert ISO to AuthRequest: %w", err)
	}

	// Get MTI for metrics
	mti, _ := message.GetString(0)

	// Determine primary region
	primaryRegion := r.determineRegion(authRequest.Pan)
	authRequest.Region = primaryRegion

	// Start timing the request
	startTime := time.Now()

	// Check if the primary region is healthy
	r.healthMutex.RLock()
	regionHealth, ok := r.regionHealth[primaryRegion]
	primaryHealthy := ok && regionHealth.IsHealthy()
	r.healthMutex.RUnlock()

	// Determine which region to use (primary or failover)
	targetRegion := primaryRegion
	if !primaryHealthy {
		// Try to use a failover region
		if failoverRegion, ok := r.config.FailoverMap[primaryRegion]; ok {
			r.healthMutex.RLock()
			failoverHealth, exists := r.regionHealth[failoverRegion]
			failoverHealthy := exists && failoverHealth.IsHealthy()
			r.healthMutex.RUnlock()

			if failoverHealthy {
				log.Printf("Failing over from %s to %s for transaction %s",
					primaryRegion, failoverRegion, authRequest.Stan)
				targetRegion = failoverRegion
				authRequest.Region = failoverRegion
			} else {
				log.Printf("Primary region %s unhealthy and failover %s also unhealthy for transaction %s",
					primaryRegion, failoverRegion, authRequest.Stan)
			}
		} else {
			log.Printf("Primary region %s unhealthy and no failover configured for transaction %s",
				primaryRegion, authRequest.Stan)
		}
	}

	log.Printf("Routing transaction %s to region %s", authRequest.Stan, targetRegion)

	// Get client for target region
	client, ok := r.clients[targetRegion]
	if !ok {
		if r.metrics != nil {
			r.metrics.ErrorCount.WithLabelValues(targetRegion, "no_client").Inc()
		}
		return nil, fmt.Errorf("no client available for region %s", targetRegion)
	}

	regionCfg := r.config.Regions[targetRegion]
	timeoutDuration := time.Duration(regionCfg.TimeoutMs) * time.Millisecond

	// Create context with timeout
	timeoutCtx, cancel := context.WithTimeout(ctx, timeoutDuration)
	defer cancel()

	// Process the request
	response, err := client.ProcessAuth(timeoutCtx, authRequest)

	// Calculate latency regardless of success/failure
	elapsed := time.Since(startTime)

	// Handle response and update metrics
	if err != nil {
		// Record failure for health monitoring
		r.healthMutex.Lock()
		if health, ok := r.regionHealth[targetRegion]; ok {
			health.RecordFailure()
		}
		r.healthMutex.Unlock()

		if r.metrics != nil {
			errorType := "unknown_error"
			if errors.Is(err, context.DeadlineExceeded) {
				errorType = "timeout"
			}
			r.metrics.ErrorCount.WithLabelValues(targetRegion, errorType).Inc()
		}

		// If this is a timeout, try to return a declined response
		if errors.Is(err, context.DeadlineExceeded) {
			log.Printf("Request timed out for region %s, returning timeout decline", targetRegion)
			return r.createTimeoutResponse(message)
		}
		return nil, fmt.Errorf("failed to process request in region %s: %w", targetRegion, err)
	}

	// Record success for health monitoring
	r.healthMutex.Lock()
	if health, ok := r.regionHealth[targetRegion]; ok {
		health.RecordSuccess()
	}
	r.healthMutex.Unlock()

	// Add processing time to the response
	response.ProcessingTimeMs = elapsed.Milliseconds()

	// Update metrics for successful request
	if r.metrics != nil {
		r.metrics.RequestCount.WithLabelValues(targetRegion, mti, response.ResponseCode).Inc()
		r.metrics.ResponseLatency.WithLabelValues(targetRegion, mti).Observe(elapsed.Seconds())
	}

	// Convert the AuthResponse back to ISO8583
	responseMessage, err := r.authResponseToIso(response, message)
	if err != nil {
		if r.metrics != nil {
			r.metrics.ErrorCount.WithLabelValues(targetRegion, "response_conversion").Inc()
		}
		return nil, fmt.Errorf("failed to convert AuthResponse to ISO: %w", err)
	}

	// Record the transaction in storage
	if r.storage != nil {
		storeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		// Determine if approved based on response code
		approved := response.ResponseCode == "00"

		// Save to storage asynchronously to avoid impacting response time
		go func() {
			if err := r.storage.SaveAuthorization(storeCtx, authRequest, targetRegion, approved); err != nil {
				log.Printf("Failed to store authorization: %v", err)
			}
		}()
	}

	return responseMessage, nil
}

// isoToAuthRequest converts an ISO8583 message to an AuthRequest
func (r *Router) isoToAuthRequest(message *iso8583.Message) (*proto.AuthRequest, error) {
	mti, err := message.GetString(0)
	if err != nil {
		return nil, err
	}

	// Retrieve required fields
	pan, err := message.GetString(2)
	if err != nil {
		return nil, fmt.Errorf("failed to get PAN: %w", err)
	}

	amount, err := message.GetString(4)
	if err != nil {
		return nil, fmt.Errorf("failed to get amount: %w", err)
	}

	transmissionTime, err := message.GetString(7)
	if err != nil {
		return nil, fmt.Errorf("failed to get transmission time: %w", err)
	}

	stan, err := message.GetString(11)
	if err != nil {
		return nil, fmt.Errorf("failed to get STAN: %w", err)
	}

	return &proto.AuthRequest{
		Mti:              mti,
		Pan:              pan,
		Amount:           amount,
		TransmissionTime: transmissionTime,
		Stan:             stan,
	}, nil
}

// authResponseToIso converts an AuthResponse to an ISO8583 message
func (r *Router) authResponseToIso(response *proto.AuthResponse, requestMessage *iso8583.Message) (*iso8583.Message, error) {
	// Create a new message
	responseMessage := iso8583.NewMessage(r.spec)

	// Set MTI (response is usually request + 10)
	err := responseMessage.Field(0, response.Mti)
	if err != nil {
		return nil, fmt.Errorf("failed to set MTI: %w", err)
	}

	// Copy fields from request
	fieldsToCopy := []int{2, 4, 7, 11}
	for _, field := range fieldsToCopy {
		if value, err := requestMessage.GetString(field); err == nil {
			if err := responseMessage.Field(field, value); err != nil {
				return nil, fmt.Errorf("failed to set field %d: %w", field, err)
			}
		}
	}

	// Set response code
	if err := responseMessage.Field(39, response.ResponseCode); err != nil {
		return nil, fmt.Errorf("failed to set response code: %w", err)
	}

	return responseMessage, nil
}

// determineRegion determines the appropriate region based on the PAN's BIN
func (r *Router) determineRegion(pan string) string {
	if len(pan) < 6 {
		return r.config.DefaultRegion
	}

	bin := pan[:6]

	for binRange, region := range r.config.BinRoutes {
		// Check if the range is a simple prefix match
		if !strings.Contains(binRange, "-") {
			if strings.HasPrefix(bin, binRange) {
				return region
			}
			continue
		}

		// Parse the range
		parts := strings.Split(binRange, "-")
		if len(parts) != 2 {
			continue
		}

		start, err1 := strconv.Atoi(parts[0])
		end, err2 := strconv.Atoi(parts[1])

		if err1 != nil || err2 != nil {
			continue
		}

		binInt, err := strconv.Atoi(bin[:len(parts[0])])
		if err != nil {
			continue
		}

		if binInt >= start && binInt <= end {
			return region
		}
	}

	return r.config.DefaultRegion
}

// createTimeoutResponse creates a decline response for timeout scenarios
func (r *Router) createTimeoutResponse(requestMessage *iso8583.Message) (*iso8583.Message, error) {
	mti, err := requestMessage.GetString(0)
	if err != nil {
		return nil, err
	}

	// Convert request MTI to response MTI
	responseMti := mti[:2] + "10"

	responseMessage := iso8583.NewMessage(r.spec)
	if err := responseMessage.Field(0, responseMti); err != nil {
		return nil, fmt.Errorf("failed to set MTI: %w", err)
	}

	// Copy fields from request
	fieldsToCopy := []int{2, 4, 7, 11}
	for _, field := range fieldsToCopy {
		if value, err := requestMessage.GetString(field); err == nil {
			if err := responseMessage.Field(field, value); err != nil {
				return nil, fmt.Errorf("failed to set field %d: %w", field, err)
			}
		}
	}

	// Set decline response code (91 = Issuer or switch inoperative)
	if err := responseMessage.Field(39, "91"); err != nil {
		return nil, fmt.Errorf("failed to set response code: %w", err)
	}

	return responseMessage, nil
}
