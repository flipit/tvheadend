# Tvheadend Microservices Framework

Modern Go-based microservices architecture for Tvheadend TV streaming server.

## Overview

This framework provides a complete microservices redesign of tvheadend using:
- **Go 1.21+** for high-performance, concurrent services
- **gRPC** for efficient inter-service communication
- **NATS JetStream** for event-driven architecture
- **PostgreSQL** for persistent storage
- **Kubernetes** for orchestration
- **OpenTelemetry** for observability

## Architecture

```
┌─────────────────────────────────────────┐
│         API Gateway (Envoy)             │
└──────────┬──────────────────────────────┘
           │
    ┌──────┴──────┐
    │  gRPC/HTTP  │
    └──────┬──────┘
           │
    ┌──────▼───────────────────────┐
    │      NATS JetStream          │
    │    (Message Bus)             │
    └──┬────┬────┬────┬────┬───────┘
       │    │    │    │    │
   ┌───▼┐ ┌─▼─┐ ┌▼─┐ ┌▼─┐ ┌▼───┐
   │DVR │ │EPG│ │Ch│ │In│ │Str │
   │Svc │ │Svc│ │Svc│ │Svc│ │Svc│
   └────┘ └───┘ └──┘ └──┘ └────┘
```

## Services

| Service | Port | Purpose |
|---------|------|---------|
| **DVR Service** | 8080 | Recording management and scheduling |
| **EPG Service** | 8081 | Electronic Program Guide |
| **Channel Service** | 8082 | Channel and service catalog |
| **Input Service** | 8083 | Input source management (DVB, IPTV, etc.) |
| **Stream Service** | 8084 | MPEG-TS processing and routing |
| **Media Server** | 8085 | Stream delivery to clients |
| **Transcoding Service** | 8086 | On-the-fly transcoding |
| **Descrambler Service** | 8087 | Conditional access |
| **API Gateway** | 8000 | External API entry point |

## Quick Start

### Prerequisites

- Go 1.21+
- Docker & Docker Compose
- Kubernetes (optional, for production)
- Protocol Buffers compiler (`protoc`)

### Local Development

1. **Clone the repository:**
```bash
git clone https://github.com/tvheadend/microservices
cd microservices
```

2. **Generate Protocol Buffers:**
```bash
make proto
```

3. **Start infrastructure:**
```bash
docker-compose up -d postgres redis nats jaeger
```

4. **Run a service:**
```bash
cd cmd/dvr-service
go run main.go
```

### Using Docker Compose

Start all services:
```bash
docker-compose up -d
```

View logs:
```bash
docker-compose logs -f dvr-service
```

### Kubernetes Deployment

1. **Create namespace:**
```bash
kubectl create namespace tvheadend
```

2. **Deploy services:**
```bash
kubectl apply -f deployments/kubernetes/
```

3. **Check status:**
```bash
kubectl get pods -n tvheadend
```

## API Documentation

### gRPC APIs

Protocol buffer definitions are in `api/proto/`:
- `dvr/v1/dvr.proto` - DVR service
- `epg/v1/epg.proto` - EPG service
- `channel/v1/channel.proto` - Channel service

### REST APIs (via gRPC-Gateway)

Access REST APIs at:
- DVR: `http://localhost:9090/api/v1/recordings`
- EPG: `http://localhost:9092/api/v1/programmes`
- Channels: `http://localhost:9094/api/v1/channels`

### Example Requests

**Schedule a recording:**
```bash
curl -X POST http://localhost:9090/api/v1/recordings \
  -H "Content-Type: application/json" \
  -d '{
    "channel_id": "ch123",
    "programme_id": "prog456",
    "priority": 50
  }'
```

**List recordings:**
```bash
curl http://localhost:9090/api/v1/recordings
```

**Search EPG:**
```bash
curl "http://localhost:9092/api/v1/programmes/search?query=news"
```

## Development

### Project Structure

```
.
├── api/
│   └── proto/              # Protocol buffer definitions
├── cmd/
│   ├── dvr-service/        # DVR service entrypoint
│   ├── epg-service/        # EPG service entrypoint
│   └── ...
├── internal/
│   ├── dvr/                # DVR domain logic
│   ├── epg/                # EPG domain logic
│   └── ...
├── pkg/
│   ├── mpegts/             # Shared MPEG-TS library
│   ├── config/             # Configuration
│   ├── auth/               # Authentication
│   └── observability/      # Metrics, tracing, logging
├── deployments/
│   ├── docker/             # Dockerfiles
│   └── kubernetes/         # K8s manifests
└── docs/                   # Documentation
```

### Code Generation

Generate gRPC code from proto files:
```bash
make proto
```

### Testing

Run unit tests:
```bash
make test
```

Run integration tests:
```bash
make test-integration
```

### Building

Build all services:
```bash
make build
```

Build Docker images:
```bash
make docker-build
```

## Observability

### Metrics

Prometheus metrics are exposed on port 9091 for each service:
```
http://localhost:9091/metrics
```

### Tracing

Jaeger UI is available at:
```
http://localhost:16686
```

### Logs

Structured JSON logs are sent to stdout. Use log aggregation (Loki, ELK) in production.

## Configuration

Services are configured via environment variables:

| Variable | Description | Default |
|----------|-------------|---------|
| `DATABASE_URL` | PostgreSQL connection string | Required |
| `NATS_URL` | NATS server URL | `nats://localhost:4222` |
| `REDIS_URL` | Redis URL | `redis://localhost:6379` |
| `GRPC_PORT` | gRPC server port | `8080` |
| `HTTP_PORT` | HTTP gateway port | `9090` |
| `METRICS_PORT` | Prometheus metrics port | `9091` |
| `LOG_LEVEL` | Logging level | `info` |
| `JAEGER_ENDPOINT` | Jaeger collector endpoint | `http://localhost:14268/api/traces` |

## Migration from Legacy Tvheadend

See [docs/microservices-architecture.md](docs/microservices-architecture.md) for detailed migration strategy.

### Strangler Fig Pattern

1. Run both systems in parallel
2. Extract services one at a time
3. Route traffic gradually to new services
4. Monitor and validate
5. Decommission legacy system

## Contributing

1. Fork the repository
2. Create a feature branch
3. Write tests for new functionality
4. Ensure all tests pass
5. Submit a pull request

## License

Same as tvheadend (GPLv3)

## Resources

- [Architecture Documentation](docs/microservices-architecture.md)
- [API Reference](docs/api-reference.md)
- [Development Guide](docs/development.md)
- [Deployment Guide](docs/deployment.md)
