# Getting Started with Tvheadend Microservices

This guide will help you get started with the tvheadend microservices architecture.

## Prerequisites

Before you begin, ensure you have the following installed:

- **Go 1.21+**: [Download](https://golang.org/dl/)
- **Docker**: [Download](https://www.docker.com/get-started)
- **Docker Compose**: Usually included with Docker Desktop
- **kubectl** (optional, for Kubernetes): [Install](https://kubernetes.io/docs/tasks/tools/)
- **Protocol Buffers compiler**:
  ```bash
  # macOS
  brew install protobuf

  # Linux
  apt-get install -y protobuf-compiler
  ```

## Step 1: Clone and Setup

1. **Clone the repository:**
```bash
git clone https://github.com/tvheadend/microservices
cd microservices
```

2. **Install development tools:**
```bash
make tools
```

This installs:
- `buf` - Protocol buffer tooling
- `protoc-gen-go` - Go protobuf generator
- `protoc-gen-go-grpc` - gRPC generator
- `protoc-gen-grpc-gateway` - REST gateway generator
- `golangci-lint` - Linter

3. **Download dependencies:**
```bash
make deps
```

## Step 2: Generate Code

Generate gRPC code from protocol buffer definitions:

```bash
make proto
```

This creates Go code in `api/` directory from `.proto` files.

## Step 3: Start Infrastructure

Use Docker Compose to start the required infrastructure:

```bash
make dev
```

This starts:
- PostgreSQL (port 5432)
- Redis (port 6379)
- NATS JetStream (port 4222)
- Jaeger (port 16686)

Verify services are running:
```bash
docker-compose ps
```

## Step 4: Run a Service Locally

### Option A: Run from source

```bash
# Set environment variables
export DATABASE_URL="postgresql://tvheadend:password@localhost:5432/tvheadend?sslmode=disable"
export NATS_URL="nats://localhost:4222"
export REDIS_URL="redis://localhost:6379"
export JAEGER_ENDPOINT="http://localhost:14268/api/traces"

# Run DVR service
cd cmd/dvr-service
go run main.go
```

### Option B: Build and run binary

```bash
# Build all services
make build

# Run DVR service
./bin/dvr-service
```

### Option C: Run with Docker Compose

```bash
docker-compose up dvr-service
```

## Step 5: Test the API

### gRPC Test

Use `grpcurl` to test gRPC endpoints:

```bash
# Install grpcurl
go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest

# List services
grpcurl -plaintext localhost:8080 list

# List methods
grpcurl -plaintext localhost:8080 list tvheadend.dvr.v1.DVRService

# Call a method
grpcurl -plaintext -d '{
  "channel_id": "ch123",
  "start_time": "2025-10-24T20:00:00Z",
  "end_time": "2025-10-24T21:00:00Z",
  "priority": 50
}' localhost:8080 tvheadend.dvr.v1.DVRService/ScheduleRecording
```

### REST Test

Test REST endpoints via gRPC-Gateway:

```bash
# Schedule a recording
curl -X POST http://localhost:9090/api/v1/recordings \
  -H "Content-Type: application/json" \
  -d '{
    "channel_id": "ch123",
    "start_time": "2025-10-24T20:00:00Z",
    "end_time": "2025-10-24T21:00:00Z",
    "priority": 50
  }'

# List recordings
curl http://localhost:9090/api/v1/recordings

# Get specific recording
curl http://localhost:9090/api/v1/recordings/{id}

# Delete recording
curl -X DELETE http://localhost:9090/api/v1/recordings/{id}
```

## Step 6: View Observability

### Metrics (Prometheus)

View Prometheus metrics:
```bash
# Service metrics
curl http://localhost:9091/metrics

# Or visit Prometheus UI
open http://localhost:9090
```

### Tracing (Jaeger)

View distributed traces:
```bash
open http://localhost:16686
```

### Logs

View structured logs:
```bash
# Docker Compose
docker-compose logs -f dvr-service

# Native
# Logs are output to stdout in JSON format
```

## Step 7: Run Tests

### Unit Tests
```bash
make test
```

### Integration Tests
```bash
make test-integration
```

### Benchmarks
```bash
make bench
```

## Step 8: Build Docker Images

Build Docker images for all services:

```bash
make docker-build
```

This creates images:
- `tvheadend/dvr-service:2.0.0`
- `tvheadend/epg-service:2.0.0`
- `tvheadend/channel-service:2.0.0`

## Step 9: Run Full Stack

Start all services with Docker Compose:

```bash
docker-compose up -d
```

Services will be available at:
- DVR Service: http://localhost:9090
- EPG Service: http://localhost:9092
- Channel Service: http://localhost:9094
- API Gateway: http://localhost:8000
- Jaeger UI: http://localhost:16686
- Prometheus: http://localhost:9090

## Step 10: Deploy to Kubernetes (Optional)

### Local Kubernetes (minikube/kind)

1. **Start local cluster:**
```bash
minikube start
# or
kind create cluster
```

2. **Build and load images:**
```bash
make docker-build

# For minikube
eval $(minikube docker-env)

# For kind
kind load docker-image tvheadend/dvr-service:2.0.0
```

3. **Deploy:**
```bash
make k8s-deploy
```

4. **Check status:**
```bash
kubectl get pods -n tvheadend
kubectl get services -n tvheadend
```

5. **Access services:**
```bash
# Port forward to access locally
kubectl port-forward -n tvheadend svc/dvr-service 9090:9090
```

## Development Workflow

### Adding a New Service

1. **Define proto:**
```bash
# Create api/proto/myservice/v1/myservice.proto
buf generate
```

2. **Implement service:**
```bash
# Create internal/myservice/service.go
# Create cmd/myservice/main.go
```

3. **Add tests:**
```bash
# Create internal/myservice/service_test.go
go test ./internal/myservice/...
```

4. **Build and run:**
```bash
make build
./bin/myservice
```

### Making Changes

1. **Update proto files** if changing APIs
2. **Run code generation:** `make proto`
3. **Implement changes**
4. **Add tests**
5. **Run tests:** `make test`
6. **Format code:** `make fmt`
7. **Lint:** `make lint`
8. **Commit changes**

### Debugging

#### Enable Debug Logging
```bash
export LOG_LEVEL=debug
```

#### Use Delve Debugger
```bash
# Install delve
go install github.com/go-delve/delve/cmd/dlv@latest

# Debug a service
dlv debug ./cmd/dvr-service
```

#### View Database
```bash
# Connect to PostgreSQL
docker-compose exec postgres psql -U tvheadend

# View recordings
SELECT * FROM recordings;
```

#### Inspect NATS Messages
```bash
# Install NATS CLI
go install github.com/nats-io/natscli/nats@latest

# Subscribe to all DVR events
nats sub "dvr.>"
```

## Common Issues

### Port Already in Use
```bash
# Find process using port
lsof -i :8080

# Kill process
kill -9 <PID>
```

### Database Migration Errors
```bash
# Reset database
docker-compose down -v
docker-compose up -d postgres

# Run migrations
make migrate-up
```

### Proto Generation Fails
```bash
# Ensure tools are installed
make tools

# Clean and regenerate
rm -rf api/
make proto
```

## Next Steps

1. **Read the architecture docs:** [microservices-architecture.md](microservices-architecture.md)
2. **Explore the API:** Try different endpoints
3. **Implement a feature:** Start with EPG or Channel service
4. **Add tests:** Ensure high code coverage
5. **Deploy to production:** Use Kubernetes manifests

## Resources

- [Go gRPC Tutorial](https://grpc.io/docs/languages/go/quickstart/)
- [NATS Documentation](https://docs.nats.io/)
- [Protocol Buffers Guide](https://developers.google.com/protocol-buffers)
- [Kubernetes Documentation](https://kubernetes.io/docs/)
- [OpenTelemetry Go](https://opentelemetry.io/docs/instrumentation/go/)

## Getting Help

- **Issues:** Open an issue on GitHub
- **Discussions:** Join the community forum
- **Documentation:** Check the docs/ directory
- **Examples:** See example code in cmd/ and internal/

Happy coding! 🚀
