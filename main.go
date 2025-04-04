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
	"github.com/TFMV/pulse/span"
	"github.com/TFMV/pulse/storage"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	"gopkg.in/yaml.v3"

	"github.com/TFMV/pulse/issuer"
	"github.com/TFMV/pulse/proto"
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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
	if config.Spanner.Enabled {
		spannerClient, err := span.NewStore(ctx, config.Spanner)
		if err != nil {
			log.Fatalf("Failed to initialize Spanner client: %v", err)
		}
		defer spannerClient.Close()
		storageClient = spannerClient
		log.Println("Spanner storage initialized")
	} else {
		log.Println("Spanner storage disabled")
	}

	// Initialize the router
	r := router.NewRouter(config.Router, chaosEngine, metricsCollector, storageClient)
	if err := r.Initialize(); err != nil {
		log.Fatalf("Failed to initialize router: %v", err)
	}

	// Create and start ISO 8583 server
	isoServer := iso.NewServer(*isoAddress, r)
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
	r.Close()
	isoServer.Shutdown()
	usEastServer.GracefulStop()
	euWestServer.GracefulStop()
	time.Sleep(500 * time.Millisecond)
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

// AppConfig holds the complete application configuration
type AppConfig struct {
	Router  router.Config `yaml:"router"`
	Chaos   chaos.Config  `yaml:"chaos"`
	Spanner span.Config   `yaml:"spanner"`
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
