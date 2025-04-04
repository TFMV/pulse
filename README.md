# Pulse

Pulse is a simplified prototype of a Global Transaction Router inspired by American Express's architecture. It simulates the flow of ISO 8583 messages over TCP from external clients, converts them to gRPC protobuf requests, routes them internally based on BIN ranges, and returns ISO 8583 responses.

## Features

- ISO 8583 TCP server for receiving and responding to transaction messages
- BIN-based regional routing with configurable routing rules
- Internal gRPC service communication between components
- Multiple regional backend processors (US East and EU West)
- Support for chaos testing and fault injection
- Interactive client for testing
- **Multi-Region Observability** with Prometheus metrics
- **Automatic Failover** between regions with circuit breaker logic
- **Health Monitoring** for regional systems

## Architecture

Pulse consists of the following components:

1. **ISO 8583 TCP Server**: Listens for incoming ISO 8583 messages
2. **Message Router**: Translates ISO messages to Protobuf and routes to the appropriate region
3. **Regional Processors**: gRPC services that implement business logic for each region
4. **Chaos Engine**: Optional component for injecting faults and simulating issues
5. **Health Monitor**: Tracks and reports regional service health status
6. **Metrics Endpoint**: Exposes Prometheus metrics for observability

## Getting Started

### Prerequisites

- Go 1.19 or later
- `protoc` for protocol buffer compilation
- Prometheus (optional, for metrics collection)

### Installation

1. Clone the repository:

   ```
   git clone https://github.com/TFMV/pulse.git
   cd pulse
   ```

2. Install dependencies:

   ```
   go mod tidy
   ```

3. Generate gRPC code from protobuf:

   ```
   protoc --go_out=. --go-grpc_out=. proto/auth.proto
   ```

### Running the Server

To run the server with default settings:

```
go run main.go
```

This starts:

- ISO 8583 TCP server on 0.0.0.0:8583
- US East gRPC service on 0.0.0.0:50051
- EU West gRPC service on 0.0.0.0:50052
- Prometheus metrics endpoint on 0.0.0.0:9090

Command-line options:

- `--iso-addr`: Address for ISO8583 TCP server (default: `0.0.0.0:8583`)
- `--config`: Path to routing configuration (default: `config/routes.yaml`)
- `--inject-faults`: Enable chaos testing with fault injection
- `--metrics`: Address for Prometheus metrics endpoint (default: `0.0.0.0:9090`)

### Running the Client

To run the interactive client:

```
go run main.go --client --server localhost:8583
```

This allows you to send test transactions with different PAN/amount combinations.

## Configuration

The routing configuration is stored in `config/routes.yaml` and defines:

- BIN-to-region routing rules
- Regional service configurations
- Failover configuration
- Chaos testing settings

Example:

```yaml
bin_routes:
  "4000-4999": "us-east"
  "5000-5999": "eu-west"
default_region: "us-east"

regions:
  us-east:
    host: "localhost"
    port: 50051
    timeout_ms: 5000
  eu-west:
    host: "localhost"
    port: 50052
    timeout_ms: 8000

# Failover configuration mapping primary regions to fallback regions
failover_map:
  "us-east": "eu-west"
  "eu-west": "us-east"

chaos:
  enabled: false
  fault_probability: 0.1
  max_delay_ms: 1000
```

## Observability & Metrics

Pulse exposes Prometheus metrics at the `/metrics` endpoint that can be scraped by a Prometheus server. The following metrics are available:

- **pulse_requests_total**: Count of processed requests with labels for region, MTI, and response code
- **pulse_response_latency_seconds**: Histogram of response latencies by region and MTI
- **pulse_errors_total**: Count of errors with labels for region and error type
- **pulse_region_health**: Gauge showing health status by region (1.0 = healthy, 0.0 = unhealthy)

### Setting Up Prometheus

1. Install Prometheus from [prometheus.io](https://prometheus.io/download/)
2. Configure Prometheus to scrape the metrics endpoint:

```yaml
scrape_configs:
  - job_name: 'pulse'
    scrape_interval: 5s
    static_configs:
      - targets: ['localhost:9090']
```

3. Start Prometheus and navigate to its web interface

## Circuit Breaker & Failover

Pulse includes an automatic failover system using a circuit breaker pattern:

- When a region fails consecutively (default: 5 times), the circuit opens
- Traffic is automatically redirected to the failover region specified in the config
- The circuit remains open for a cooldown period (default: 30 seconds)
- After cooldown, the circuit enters half-open state to test if the region is healthy again
- Successful requests close the circuit and restore normal routing

The circuit breaker tracks:

- Consecutive failures
- Time-windowed error rates
- Circuit state transitions (CLOSED → OPEN → HALF-OPEN)

Health checks run every 10 seconds (configurable) to maintain up-to-date status of all regions.

## Testing

### Sample Transactions

- US Transaction (approved): `4111111111111111,50.00`
- US Transaction (declined - over limit): `4111111111111111,550.00`
- US Transaction (declined - PAN ending in 0): `4111111111111110,50.00`
- EU Transaction (approved): `5555555555554444,100.00`
- EU Transaction (declined - over limit): `5555555555554444,450.00`

### Chaos Testing

To enable chaos testing with fault injection:

```
go run main.go --inject-faults
```

This will randomly introduce:

- Delays in processing
- Timeouts
- Connection errors
- Service unavailability

Use chaos testing with the metrics dashboard to observe how the circuit breaker and failover mechanisms respond to failures.

## Project Structure

```
pulse/
├── main.go                  # Entry point, config load, router start
├── config/routes.yaml       # BIN-to-region routing configuration
├── iso/server.go            # TCP ISO server & message I/O
├── router/
│   ├── router.go            # Message translation, routing logic
│   └── health.go            # Circuit breaker and health monitoring
├── metrics/
│   └── metrics.go           # Prometheus metrics definitions
├── proto/auth.proto         # Protobuf definitions
├── issuer/                  # gRPC issuers for each region
├── chaos/faults.go          # Simulated chaos injection rules
├── client/send.go           # CLI to send sample ISO 8583 messages
```

## License

[MIT License](LICENSE)
