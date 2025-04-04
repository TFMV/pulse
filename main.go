package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/TFMV/pulse/chaos"
	"github.com/TFMV/pulse/client"
	"github.com/TFMV/pulse/iso"
	"github.com/TFMV/pulse/metrics"
	"github.com/TFMV/pulse/router"
	"github.com/TFMV/pulse/storage"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection"
	"gopkg.in/yaml.v3"

	"github.com/TFMV/pulse/issuer"
	"github.com/TFMV/pulse/proto"
	"github.com/TFMV/pulse/span"
	"github.com/TFMV/pulse/workflow"
)

const (
	defaultIsoAddress     = "0.0.0.0:8583"
	defaultConfigPath     = "config/routes.yaml"
	defaultMetricsAddress = "0.0.0.0:9090"
)

var (
	configPath       = flag.String("config", defaultConfigPath, "Path to configuration file")
	isoAddress       = flag.String("iso-addr", defaultIsoAddress, "Address for ISO8583 server")
	clientMode       = flag.Bool("client", false, "Run in client mode")
	clientServerAddr = flag.String("server", "localhost:8583", "Server address for client mode")
	injectFaults     = flag.Bool("inject-faults", false, "Enable chaos testing with fault injection")
	metricsAddr      = flag.String("metrics", defaultMetricsAddress, "Prometheus metrics endpoint address")
	usEastAddr       = flag.String("us-east", "localhost:50051", "US East issuer address")
	euWestAddr       = flag.String("eu-west", "localhost:50052", "EU West issuer address")
	chaosFlag        = flag.Bool("chaos", false, "Enable chaos testing")
)

// RegionConfig holds configuration for a region
type RegionConfig struct {
	Address string `yaml:"address"`
}

// AppConfig holds the complete application configuration
type AppConfig struct {
	Iso8583Server struct {
		Address string `yaml:"address"`
	} `yaml:"iso8583_server"`

	Regions map[string]RegionConfig `yaml:"regions"`

	Router struct {
		HealthCheckInterval time.Duration     `yaml:"health_check_interval"`
		FailoverMap         map[string]string `yaml:"failover_map"`
		BinRoutes           map[string]string `yaml:"bin_routes"`
		DefaultRegion       string            `yaml:"default_region"`
	} `yaml:"router"`

	Metrics struct {
		Enabled bool   `yaml:"enabled"`
		Address string `yaml:"address"`
	} `yaml:"metrics"`

	Storage struct {
		Type       string `yaml:"type"`
		Connection string `yaml:"connection"`
		Database   string `yaml:"database"`
		Enabled    bool   `yaml:"enabled"`
	} `yaml:"storage"`

	Temporal struct {
		HostPort                 string        `yaml:"host_port"`
		Namespace                string        `yaml:"namespace"`
		TaskQueue                string        `yaml:"task_queue"`
		WorkflowExecutionTimeout time.Duration `yaml:"workflow_execution_timeout"`
		WorkerCount              int           `yaml:"worker_count"`
		Enabled                  bool          `yaml:"enabled"`
	} `yaml:"temporal"`

	Chaos chaos.Config `yaml:"chaos"`
}

func main() {
	// Parse command-line flags
	flag.Parse()

	// Handle client mode
	if *clientMode {
		if err := client.RunInteractiveClient(*clientServerAddr); err != nil {
			log.Fatalf("Client error: %v", err)
		}
		return
	}

	// Create a context that can be cancelled
	ctx := context.Background()

	// Initialize metrics
	metricsCollector := metrics.NewMetrics()

	// Start metrics server
	go startMetricsServer(*metricsAddr)
	log.Printf("Metrics server started on %s", *metricsAddr)

	// Load configuration
	config, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Override chaos setting from command line if specified
	if *chaosFlag {
		config.Chaos.Enabled = true
	}

	// Initialize chaos engine if enabled
	var chaosEngine *chaos.Engine
	if config.Chaos.Enabled {
		chaosEngine = chaos.NewEngine(config.Chaos)
		log.Printf("Chaos testing enabled with fault probability %.1f%%", config.Chaos.FaultProbability*100)
	}

	// Initialize storage if enabled
	var storageClient storage.Storage
	if config.Storage.Enabled {
		var err error
		spannerClient, err := span.NewStore(ctx, span.Config{
			ProjectID:  config.Storage.Connection,
			InstanceID: "pulse-instance",
			DatabaseID: config.Storage.Database,
			Enabled:    true,
		})
		if err != nil {
			log.Fatalf("Failed to initialize Spanner storage: %v", err)
		}
		defer spannerClient.Close()
		storageClient = spannerClient
	}

	// Convert map of RegionConfig to router.RegionConfig
	routerRegions := make(map[string]router.RegionConfig)
	for name, cfg := range config.Regions {
		host, port := parseAddress(cfg.Address)
		routerRegions[name] = router.RegionConfig{
			Host:      host,
			Port:      port,
			TimeoutMs: 5000, // Default 5 seconds timeout
		}
	}

	// Create router config
	routerConfig := router.Config{
		BinRoutes:     config.Router.BinRoutes,
		DefaultRegion: config.Router.DefaultRegion,
		Regions:       routerRegions,
		FailoverMap:   config.Router.FailoverMap,
	}

	// Initialize the router
	rt := router.NewRouter(routerConfig, chaosEngine, metricsCollector, storageClient)
	if err := rt.Initialize(); err != nil {
		log.Fatalf("Failed to initialize router: %v", err)
	}

	// Initialize Temporal orchestrator if enabled
	var orchestrator *workflow.Orchestrator
	if config.Temporal.Enabled {
		temporalConfig := workflow.TemporalConfig{
			HostPort:                 config.Temporal.HostPort,
			Namespace:                config.Temporal.Namespace,
			TaskQueue:                config.Temporal.TaskQueue,
			WorkflowExecutionTimeout: config.Temporal.WorkflowExecutionTimeout,
			WorkerCount:              config.Temporal.WorkerCount,
			Enabled:                  true,
		}

		// Create the orchestrator
		var err error
		orchestrator, err = workflow.NewOrchestrator(temporalConfig)
		if err != nil {
			log.Fatalf("Failed to initialize Temporal orchestrator: %v", err)
		}

		// Set up workflow implementations
		fraudAnalyzer := workflow.NewSimpleFraudAnalyzer()

		auditLogger, err := workflow.NewFileAuditLogger("./audit-logs")
		if err != nil {
			log.Fatalf("Failed to initialize audit logger: %v", err)
		}

		// Register workflows and activities
		clients := make(map[string]proto.AuthServiceClient)
		for region, regionConfig := range config.Regions {
			conn, err := grpc.Dial(regionConfig.Address, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				log.Fatalf("Failed to connect to region %s: %v", region, err)
			}
			clients[region] = proto.NewAuthServiceClient(conn)
		}

		activities := workflow.NewActivities(clients, auditLogger, fraudAnalyzer)

		// We need a function that returns the region based on request
		getRegionFunc := func(pan string) string {
			// This is a wrapper to determine region from router's logic
			// In a real implementation, this would use router's determineRegion
			// Since that's private, we might just use the default region
			return config.Router.DefaultRegion
		}

		orchestrator.RegisterWorkflowsAndActivities(activities, getRegionFunc)

		// Start the worker
		if err := orchestrator.Start(); err != nil {
			log.Fatalf("Failed to start Temporal worker: %v", err)
		}
		defer orchestrator.Close()
	}

	// Create and start ISO 8583 server
	isoServer := iso.NewServer(*isoAddress, rt)
	go func() {
		if err := isoServer.Start(); err != nil {
			log.Fatalf("Failed to start ISO 8583 server: %v", err)
		}
	}()

	// Create issuer instances
	usEastIssuer := issuer.NewUSEastIssuer()
	euWestIssuer := issuer.NewEUWestIssuer()

	// Wrap issuers with storage if enabled
	usEastIssuerWithStorage := issuer.WrapWithStorage(usEastIssuer, storageClient)
	euWestIssuerWithStorage := issuer.WrapWithStorage(euWestIssuer, storageClient)

	// Create gRPC servers
	usEastServer := grpc.NewServer()
	euWestServer := grpc.NewServer()

	// Register the services
	proto.RegisterAuthServiceServer(usEastServer, usEastIssuerWithStorage)
	proto.RegisterAuthServiceServer(euWestServer, euWestIssuerWithStorage)

	// Enable reflection on the servers
	reflection.Register(usEastServer)
	reflection.Register(euWestServer)

	// Start gRPC servers
	go startGRPCServer(usEastServer, *usEastAddr, "US-East")
	go startGRPCServer(euWestServer, *euWestAddr, "EU-West")

	// Wait for termination signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down...")
	rt.Close()
	isoServer.Shutdown()
	usEastServer.GracefulStop()
	euWestServer.GracefulStop()
	time.Sleep(500 * time.Millisecond)
}

// parseAddress parses a host:port string into separate components
func parseAddress(address string) (string, int) {
	host, portStr, err := net.SplitHostPort(address)
	if err != nil {
		return address, 50051 // Default port if no port specified
	}

	port := 50051
	fmt.Sscanf(portStr, "%d", &port)
	return host, port
}

// startGRPCServer starts a gRPC server on the specified address
func startGRPCServer(server *grpc.Server, address, region string) {
	lis, err := net.Listen("tcp", address)
	if err != nil {
		log.Fatalf("Failed to listen on %s: %v", address, err)
	}

	log.Printf("Starting %s issuer service on %s", region, address)
	if err := server.Serve(lis); err != nil {
		log.Fatalf("Failed to serve %s: %v", region, err)
	}
}

// startMetricsServer starts the Prometheus metrics endpoint
func startMetricsServer(addr string) {
	http.Handle("/metrics", promhttp.Handler())
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("Failed to start metrics server: %v", err)
	}
}

// loadConfig loads the configuration from a YAML file
func loadConfig(path string) (*AppConfig, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config AppConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Set default failover map if not present
	if config.Router.FailoverMap == nil {
		config.Router.FailoverMap = map[string]string{
			"us_east": "eu_west",
			"eu_west": "us_east",
		}
	}

	return &config, nil
}
