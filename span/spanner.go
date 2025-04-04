package span

import (
	"context"
	"fmt"
	"log"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/TFMV/pulse/proto"
	"github.com/TFMV/pulse/storage"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
)

// Config holds Spanner configuration
type Config struct {
	ProjectID  string `yaml:"project_id"`
	InstanceID string `yaml:"instance_id"`
	DatabaseID string `yaml:"database_id"`
	Enabled    bool   `yaml:"enabled"`
}

// Store implements storage.Storage interface for Spanner
type Store struct {
	client         *spanner.Client
	databaseString string
	writeLatency   *prometheus.HistogramVec
	readLatency    *prometheus.HistogramVec
	errorCount     *prometheus.CounterVec
}

// NewStore creates a new Spanner storage client
func NewStore(ctx context.Context, config Config, opts ...option.ClientOption) (*Store, error) {
	if !config.Enabled {
		log.Println("Spanner storage is disabled")
		return nil, nil
	}

	// Validate config
	if config.ProjectID == "" || config.InstanceID == "" || config.DatabaseID == "" {
		return nil, fmt.Errorf("incomplete Spanner configuration: project_id, instance_id, and database_id are required")
	}

	// Create metrics
	writeLatency := promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "pulse_spanner_write_latency_seconds",
			Help:    "Spanner write latency distribution in seconds",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 10), // 1ms to ~1s
		},
		[]string{"operation"},
	)

	readLatency := promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "pulse_spanner_read_latency_seconds",
			Help:    "Spanner read latency distribution in seconds",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 10), // 1ms to ~1s
		},
		[]string{"operation"},
	)

	errorCount := promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pulse_spanner_errors_total",
			Help: "Total number of Spanner errors",
		},
		[]string{"operation", "error_type"},
	)

	// Construct database string
	databaseString := fmt.Sprintf("projects/%s/instances/%s/databases/%s",
		config.ProjectID, config.InstanceID, config.DatabaseID)

	// Create Spanner client
	client, err := spanner.NewClient(ctx, databaseString, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create Spanner client: %w", err)
	}

	log.Printf("Connected to Spanner database: %s", databaseString)

	return &Store{
		client:         client,
		databaseString: databaseString,
		writeLatency:   writeLatency,
		readLatency:    readLatency,
		errorCount:     errorCount,
	}, nil
}

// SaveAuthorization implements the storage.Storage interface
func (s *Store) SaveAuthorization(ctx context.Context, auth *proto.AuthRequest, region string, approved bool) error {
	if s == nil || s.client == nil {
		// Spanner is disabled, just return
		return nil
	}

	// Start timing
	start := time.Now()
	defer func() {
		s.writeLatency.WithLabelValues("save_authorization").Observe(time.Since(start).Seconds())
	}()

	// Create mutation
	mutation := spanner.InsertOrUpdate("Authorizations", []string{
		"Stan", "Pan", "Amount", "Region", "Approved", "TransmissionTime", "InsertedAt",
	}, []interface{}{
		auth.Stan, auth.Pan, auth.Amount, region, approved,
		auth.TransmissionTime, spanner.CommitTimestamp,
	})

	// Apply mutation
	_, err := s.client.Apply(ctx, []*spanner.Mutation{mutation})
	if err != nil {
		s.errorCount.WithLabelValues("save_authorization", grpcCodeToString(err)).Inc()
		return fmt.Errorf("failed to save authorization: %w", err)
	}

	return nil
}

// GetTransaction implements the storage.Storage interface
func (s *Store) GetTransaction(ctx context.Context, stan string) (*proto.AuthRecord, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("spanner storage is disabled")
	}

	// Start timing
	start := time.Now()
	defer func() {
		s.readLatency.WithLabelValues("get_transaction").Observe(time.Since(start).Seconds())
	}()

	// Execute query
	row, err := s.client.Single().ReadRow(ctx, "Authorizations", spanner.Key{stan}, []string{
		"Stan", "Pan", "Amount", "Region", "Approved", "TransmissionTime", "InsertedAt",
	})
	if err != nil {
		if spanner.ErrCode(err) == codes.NotFound {
			return nil, nil // Not found but not an error
		}
		s.errorCount.WithLabelValues("get_transaction", grpcCodeToString(err)).Inc()
		return nil, fmt.Errorf("failed to get transaction: %w", err)
	}

	// Parse the result
	var record storage.AuthRecord
	var insertedAt spanner.NullTime
	if err := row.Columns(
		&record.Stan,
		&record.Pan,
		&record.Amount,
		&record.Region,
		&record.Approved,
		&record.TransmissionTime,
		&insertedAt,
	); err != nil {
		s.errorCount.WithLabelValues("get_transaction", "parse_error").Inc()
		return nil, fmt.Errorf("failed to parse transaction: %w", err)
	}

	if insertedAt.Valid {
		record.InsertedAt = insertedAt.Time
	}

	return record.ToProto(), nil
}

// Close implements the storage.Storage interface
func (s *Store) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	s.client.Close()
	return nil
}

// Helper function to convert gRPC error code to string
func grpcCodeToString(err error) string {
	return fmt.Sprintf("%s", spanner.ErrCode(err))
}
