package main

import (
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
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	"gopkg.in/yaml.v3"
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
)

func main() {
	flag.Parse()

	// Handle client mode
	if *clientMode {
		if err := client.RunInteractiveClient(*clientServerAddr); err != nil {
			log.Fatalf("Client error: %v", err)
		}
		return
	}

	// Initialize metrics
	metricsCollector := metrics.NewMetrics()

	// Start Prometheus metrics endpoint
	go startMetricsServer(*metricsAddr)

	// Load configuration
	config, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Initialize chaos engine if enabled
	var chaosEngine *chaos.Engine
	if *injectFaults {
		chaosEngine = chaos.NewEngine(config.Chaos)
		log.Printf("Chaos testing enabled with fault probability %.1f%%", config.Chaos.FaultProbability*100)
	}

	// Initialize the router
	r := router.NewRouter(config.Router, chaosEngine, metricsCollector)
	if err := r.Initialize(); err != nil {
		log.Fatalf("Failed to initialize router: %v", err)
	}
	defer r.Close()

	// Start the ISO8583 server
	isoServer := iso.NewServer(*isoAddress, r)
	go func() {
		log.Printf("Starting ISO8583 server on %s", *isoAddress)
		if err := isoServer.Start(); err != nil {
			log.Fatalf("ISO8583 server error: %v", err)
		}
	}()

	// Start the gRPC issuer servers for each region
	for region, regionCfg := range config.Router.Regions {
		address := net.JoinHostPort(regionCfg.Host, "50051")
		go startIssuerService(address, region)
	}

	// Wait for termination signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down...")
	isoServer.Shutdown()

	// Give services a moment to complete shutdown
	time.Sleep(500 * time.Millisecond)
}

// startIssuerService starts a gRPC server for a specific region
func startIssuerService(address, region string) {
	lis, err := net.Listen("tcp", address)
	if err != nil {
		log.Fatalf("Failed to listen on %s: %v", address, err)
	}

	s := grpc.NewServer()
	// issuerInstance := issuer.NewIssuer(config, region)
	// proto.RegisterAuthServiceServer(s, issuerInstance)
	reflection.Register(s)

	log.Printf("Starting issuer service for %s region on %s", region, address)
	if err := s.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}

// startMetricsServer starts the Prometheus metrics endpoint
func startMetricsServer(addr string) {
	http.Handle("/metrics", promhttp.Handler())
	log.Printf("Starting Prometheus metrics server on %s", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Printf("Metrics server error: %v", err)
	}
}

// AppConfig holds the complete application configuration
type AppConfig struct {
	Router router.Config `yaml:"router"`
	Chaos  chaos.Config  `yaml:"chaos"`
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
