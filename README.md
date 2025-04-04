# Pulse: Global Transaction Router

A simplified payment transaction routing system inspired by American Express's architecture, implementing ISO 8583 processing with modern technologies.

## Overview

Pulse simulates the flow of ISO 8583 messages over TCP from external clients, converts them to gRPC protobuf requests, routes them internally based on BIN ranges, and returns ISO 8583 responses. The system implements modern architectural patterns while maintaining compatibility with legacy payment protocols.

## Key Features

- **ISO 8583 Processing**: TCP server for receiving and responding to financial transaction messages
- **Protocol Transformation**: Converts ISO 8583 messages to Protocol Buffers and back
- **BIN-based Routing**: Routes transactions to different regional processors based on card BIN ranges
- **Multi-Region Architecture**: Supports multiple backend processors (US East and EU West)
- **Fault Tolerance**: Circuit breaker pattern with automatic failover between regions
- **Observability**: Comprehensive Prometheus metrics for monitoring system health
- **Chaos Testing**: Support for fault injection to test resilience
- **Transaction Storage**: Integration with Google Cloud Spanner for persistent transaction history

## Architecture

```mermaid
graph TD
    classDef external fill:#e1f5fe,stroke:#01579b,stroke-width:2px
    classDef internal fill:#f3e5f5,stroke:#6a1b9a,stroke-width:2px
    classDef processor fill:#e8f5e9,stroke:#2e7d32,stroke-width:2px
    classDef storage fill:#ffecb3,stroke:#ff6f00,stroke-width:2px
    
    A1[Payment Client] -.ISO 8583<br>over TCP.-> A2
    A2[ISO 8583 Server] --> A3
    A3[Router ISO→Proto] --> A4
    A4[Regional Routing] --> A5
    A4 --> A6
    A5[US East Processor]
    A6[EU West Processor]
    A3 -.->|Async Storage| A7[Spanner]
    
    class A1 external
    class A2,A3,A4 internal
    class A5,A6 processor
    class A7 storage
```

Pulse consists of the following components:

1. **ISO 8583 TCP Server**: Accepts incoming financial messages over persistent TCP connections
2. **Message Router**: Translates ISO messages to Protocol Buffers and determines appropriate routing
3. **Regional Processors**: gRPC services implementing business logic for each region
4. **Health Monitor**: Tracks regional service health and implements circuit breaking for reliability
5. **Metrics System**: Provides real-time observability with Prometheus
6. **Storage Layer**: Persists transaction data using Google Cloud Spanner

## Getting Started

### Prerequisites

- Go 1.19 or later
- Protocol Buffers compiler (`protoc`)
- Prometheus (optional, for metrics collection)

### Installation

1. Clone the repository:

   ```bash
   git clone https://github.com/TFMV/pulse.git
   cd pulse
   ```

2. Install dependencies:

   ```bash
   go mod tidy
   ```

3. Generate gRPC code from protobuf:

   ```bash
   protoc --go_out=. --go-grpc_out=. proto/auth.proto
   ```

### Running Pulse

#### Starting the Server

```bash
go run main.go
```

This starts:

- ISO 8583 TCP server on 0.0.0.0:8583
- US East gRPC service on 0.0.0.0:50051
- EU West gRPC service on 0.0.0.0:50052
- Prometheus metrics endpoint on 0.0.0.0:9090

#### Command-line Options

- `--config`: Path to configuration file (default: `config/config.yaml`)
- `--iso-addr`: Address for ISO 8583 server (default: `0.0.0.0:8583`)
- `--metrics`: Address for Prometheus metrics (default: `0.0.0.0:9090`)
- `--chaos`: Enable chaos testing with fault injection
- `--client`: Run in client mode (for testing)

#### Using the Test Client

To send test transactions:

```bash
go run main.go --client --server localhost:8583
```

This launches an interactive client for sending sample transactions.

## Configuration

The system is configured via YAML files stored in the `config` directory.

### Main Configuration File

The main configuration file (`config/config.yaml`) includes:

```yaml
router:
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

  failover_map:
    "us-east": "eu-west"
    "eu-west": "us-east"

chaos:
  enabled: false
  fault_probability: 0.1
  max_delay_ms: 1000

spanner:
  enabled: false
  project_id: "pulse-project"
  instance_id: "pulse-instance"
  database_id: "pulse-db"
```

## Reliability Features

### Circuit Breaker Pattern

Pulse implements a sophisticated circuit breaker for automatic failover:

```mermaid
stateDiagram-v2
    [*] --> CLOSED
    
    CLOSED --> OPEN: 5+ consecutive failures
    OPEN --> HALF_OPEN: 30s timeout
    HALF_OPEN --> CLOSED: Success
    HALF_OPEN --> OPEN: Failure
```

- **Circuit States**:
  - **CLOSED**: Normal operation, all requests processed
  - **OPEN**: Circuit broken, requests redirected to failover region
  - **HALF-OPEN**: Testing recovery, limited traffic allowed

- **Health Monitoring**:
  - Tracks consecutive failures and error rates
  - Automatically redirects traffic to healthy regions
  - Periodically checks health (every 10 seconds)
  - Self-healing when regions recover

### Chaos Testing

Pulse includes a chaos engine for simulating failure scenarios:

```bash
go run main.go --chaos
```

The chaos engine introduces:

- Processing delays
- Timeouts
- Connection errors
- Service unavailability

This helps test the resilience of the circuit breaker and failover system.

## Observability

### Prometheus Metrics

Pulse exposes detailed metrics at the `/metrics` endpoint:

| Metric | Type | Description |
|--------|------|-------------|
| `pulse_requests_total` | Counter | Request count by region, MTI, and response code |
| `pulse_response_latency_seconds` | Histogram | Response time distribution |
| `pulse_errors_total` | Counter | Error count by region and type |
| `pulse_region_health` | Gauge | Health status by region (1.0=healthy, 0.0=unhealthy) |
| `pulse_spanner_write_latency_seconds` | Histogram | Spanner write operation times |
| `pulse_spanner_read_latency_seconds` | Histogram | Spanner read operation times |
| `pulse_spanner_errors_total` | Counter | Spanner errors by operation and type |

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

## Spanner Integration

Pulse includes a pluggable storage system with Google Cloud Spanner implementation.

### Storage Features

- **Transaction Persistence**: Stores all authorization requests and responses
- **Asynchronous Writes**: Non-blocking storage to maintain low latency
- **Historical Lookups**: API to retrieve transaction history by STAN
- **Regional Analytics**: Support for regional transaction analysis
- **Metrics**: Comprehensive monitoring of storage operations

### Database Schema

```sql
CREATE TABLE Authorizations (
  Stan STRING(12) NOT NULL,
  Pan STRING(19) NOT NULL,
  Amount FLOAT64 NOT NULL,
  Region STRING(50) NOT NULL,
  Approved BOOL NOT NULL,
  TransmissionTime TIMESTAMP NOT NULL,
  InsertedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp=true),
) PRIMARY KEY (Stan);
```

Indexes are created for efficient querying by region, approval status, and PAN.

### API Access

Transaction history can be retrieved via the gRPC API using the `GetTransaction` endpoint:

```protobuf
rpc GetTransaction (GetTransactionRequest) returns (AuthRecord) {}
```

## Testing

### Sample Transactions

| Card Number | Amount | Region | Expected Result |
|-------------|--------|--------|----------------|
| 4111111111111111 | 50.00 | US East | Approved |
| 4111111111111111 | 550.00 | US East | Declined (over limit) |
| 4111111111111110 | 50.00 | US East | Declined (PAN ending in 0) |
| 5555555555554444 | 100.00 | EU West | Approved |
| 5555555555554444 | 450.00 | EU West | Declined (over limit) |

## Project Structure

```
pulse/
├── main.go                  # Application entry point
├── config/                  # Configuration files
│   └── config.yaml          # Main configuration
├── iso/                     # ISO 8583 message handling
│   └── server.go            # TCP server implementation
├── router/                  # Message routing
│   ├── router.go            # Main routing logic
│   └── health.go            # Health monitoring
├── proto/                   # Protocol Buffers
│   ├── auth.proto           # Service definitions
│   └── *.pb.go              # Generated code
├── issuer/                  # Regional processors
│   ├── service.go           # Service wrapper
│   ├── us_east.go           # US East implementation
│   └── eu_west.go           # EU West implementation
├── storage/                 # Data persistence
│   └── storage.go           # Storage interface
├── span/                    # Spanner implementation
│   ├── spanner.go           # Spanner client
│   └── schema.sql           # Database schema
├── metrics/                 # Observability
│   └── metrics.go           # Prometheus metrics
├── chaos/                   # Chaos testing
│   └── faults.go            # Fault injection
└── client/                  # Test tools
    └── send.go              # ISO 8583 client
```

## License

[MIT License](LICENSE)
