# Tvheadend Microservices Architecture
## Modern Go-based Redesign

---

## Executive Summary

This document outlines a microservices-based architecture for rewriting tvheadend in Go. The design focuses on:
- **Scalability**: Horizontal scaling of individual components
- **Resilience**: Fault isolation and graceful degradation
- **Maintainability**: Clear service boundaries and responsibilities
- **Performance**: Efficient stream processing with Go's concurrency
- **Cloud-native**: Container-ready, observability-first design

---

## Service Decomposition Strategy

### Core Principles

1. **Domain-Driven Design**: Services aligned with business domains
2. **Single Responsibility**: Each service owns one capability
3. **Data Ownership**: Services own their data stores
4. **API-First**: Well-defined contracts between services
5. **Event-Driven**: Async communication where appropriate

### Service Boundaries

```
┌─────────────────────────────────────────────────────────────┐
│                      API Gateway                             │
│           (gRPC Gateway / REST / GraphQL)                    │
└────────┬───────────────────────────────────────┬────────────┘
         │                                       │
    ┌────▼────────────────────────────┐    ┌───▼──────────┐
    │   Service Mesh (Istio/Linkerd)  │    │  Web UI      │
    └────┬────────────────────────────┘    │  (SPA)       │
         │                                  └──────────────┘
    ┌────▼─────────────────────────────────────────────────┐
    │              Message Bus (NATS/Kafka)                 │
    └────┬─────┬─────┬─────┬─────┬─────┬─────┬────┬───────┘
         │     │     │     │     │     │     │    │
    ┌────▼──┐ ┌▼──┐ ┌▼──┐ ┌▼──┐ ┌▼──┐ ┌▼──┐ ┌▼──┐ ┌▼──────┐
    │Input  │ │EPG│ │DVR│ │Ch │ │Str│ │TS │ │Tr │ │Descr  │
    │Mgr    │ │Svc│ │Svc│ │Svc│ │Svc│ │Svc│ │Svc│ │Svc    │
    └───────┘ └───┘ └───┘ └───┘ └───┘ └───┘ └───┘ └───────┘
```

---

## Microservices Catalog

### 1. API Gateway Service
**Responsibility**: External API entry point, routing, auth, rate limiting

**Technology**:
- Go with `grpc-gateway` or `envoy`
- GraphQL via `gqlgen` for flexible queries

**Key Features**:
- Protocol translation (HTTP/REST → gRPC)
- JWT authentication & authorization
- Request validation and sanitization
- API versioning support
- Rate limiting per client
- Response caching

**Endpoints**:
```
/api/v1/channels
/api/v1/epg
/api/v1/recordings
/api/v1/streams
/graphql
```

---

### 2. Input Management Service
**Responsibility**: Manage all input sources (DVB, IPTV, SAT>IP, Files)

**Technology**:
- Go with plugin architecture for input types
- `github.com/ziutek/dvb` for DVB support
- Custom RTSP/HTTP clients

**Sub-components**:
- **DVB Adapter Manager**: Linux DVB hardware control
- **IPTV Manager**: HTTP/UDP stream ingestion
- **SAT>IP Client**: RTSP-based satellite receivers
- **File Input Manager**: TS file playback

**Data Store**: PostgreSQL for configuration, Redis for state

**Key Features**:
- Hot-pluggable input sources
- Health monitoring per input
- Auto-discovery (SSDP for SAT>IP, DVB scanning)
- Input priority and fallback
- Signal quality metrics

**Events Published**:
```
input.discovered
input.online
input.offline
input.signal_quality
mux.scanned
service.found
```

---

### 3. Stream Processing Service
**Responsibility**: MPEG-TS demuxing, filtering, routing

**Technology**:
- Go with `github.com/Comcast/gots` for MPEG-TS
- Custom PSI/SI parsers
- Zero-copy buffer management

**Key Features**:
- PSI/SI table parsing (PAT, PMT, SDT, NIT)
- PID filtering and remapping
- Multiple concurrent streams
- Stream health monitoring
- Buffer management

**Architecture**:
```go
Pipeline: Input → Demuxer → Filter → Muxer → Output
         (raw TS) (packets)  (PIDs)  (TS)   (subscribers)
```

**Data Flow**:
- Receives raw TS packets from Input Service
- Parses PSI/SI tables
- Routes streams to subscribers (DVR, Clients)
- Publishes service information updates

**Events**:
```
stream.started
stream.stopped
stream.error
service.updated (PSI/SI changes)
```

---

### 4. EPG Service
**Responsibility**: Electronic Program Guide management

**Technology**:
- Go with `gorm` ORM
- Background workers for EPG grabbing

**Data Store**: PostgreSQL with jsonb for flexible metadata

**Key Features**:
- Multiple EPG sources:
  - EIT parsing from MPEG-TS
  - XMLTV import (XML parsing)
  - External API integrations
- Brand/Season/Episode hierarchy
- Genre classification
- Full-text search
- Series linking
- Schedule conflict detection

**API**:
```
GetProgramme(channel, time)
SearchProgrammes(query, filters)
GetSeriesEpisodes(seriesId)
GetSchedule(channelId, timeRange)
```

**Events**:
```
epg.programme.added
epg.programme.updated
epg.series.created
```

---

### 5. DVR Service
**Responsibility**: Recording management and scheduling

**Technology**:
- Go with cron scheduling (`github.com/robfig/cron`)
- File storage abstraction (local, S3, NFS)

**Data Store**: PostgreSQL for recording metadata

**Key Features**:
- Recording scheduling
- Automatic recording rules (autorec)
- Series recording with duplicate detection
- Post-processing pipeline
- Padding (pre/post record time)
- Storage quota management
- Recording prioritization
- Failed recording retry logic

**State Machine**:
```
Scheduled → Waiting → Recording → Post-Processing → Completed
                 ↓                      ↓
              Cancelled              Failed
```

**API**:
```
ScheduleRecording(programmeId, options)
CreateAutoRecRule(title, pattern, options)
GetRecordings(filters)
DeleteRecording(recordingId)
```

**Events**:
```
recording.scheduled
recording.started
recording.completed
recording.failed
```

---

### 6. Channel Service
**Responsibility**: Channel and service catalog management

**Technology**: Go with PostgreSQL

**Data Store**: PostgreSQL with relationships

**Schema**:
```
Channels (user-facing)
  ├─> Services (technical streams)
  ├─> EPG linkage
  ├─> Tags/Groups
  └─> Icons
```

**Key Features**:
- Channel CRUD operations
- Service mapping and priority
- Channel numbering and sorting
- Tag-based organization
- Icon/logo management
- Bouquet support

**API**:
```
GetChannels(filters)
MapChannelToService(channelId, serviceId)
CreateChannelTag(name, channels)
```

---

### 7. Timeshift Service
**Responsibility**: Live TV buffering and replay

**Technology**:
- Go with ring buffer implementation
- Memory-mapped files for efficient I/O

**Storage**:
- Ephemeral storage (tmpfs/RAM disk)
- Configurable retention period

**Key Features**:
- Per-channel circular buffers
- Pause/resume live TV
- Instant replay
- Variable buffer sizes
- Automatic cleanup
- Seek support

**API**:
```
CreateTimeshiftBuffer(channelId, duration)
GetTimeshiftSegment(bufferId, offset, length)
SeekTimeshift(bufferId, timestamp)
```

---

### 8. Transcoding Service
**Responsibility**: On-the-fly media transcoding

**Technology**:
- Go with FFmpeg bindings (`github.com/u2takey/ffmpeg-go`)
- Hardware acceleration support (VAAPI, NVENC)

**Key Features**:
- Multiple codec support (H.264, H.265, VP9, AV1)
- Adaptive bitrate streaming (HLS/DASH)
- Audio transcoding
- Subtitle rendering
- Horizontal scaling based on load
- Transcoding profiles

**Architecture**:
- Stateless workers
- Queue-based job distribution
- Result caching

**API**:
```
TranscodeStream(streamId, profile)
GetTranscodingProfiles()
GetTranscodingStatus(jobId)
```

---

### 9. Descrambler Service
**Responsibility**: Conditional access decryption

**Technology**:
- Go with CGO for existing C libraries (FFdecsa)
- Hardware crypto acceleration

**Key Features**:
- CSA descrambling
- CWC (Control Word Client)
- CAPMT protocol
- AES decryption
- Key caching
- CAM emulation

**Security**:
- Encrypted key storage
- Audit logging
- Key rotation

**API**:
```
DescramblerStream(streamId, caSystem)
GetCAMStatus()
```

---

### 10. User Service
**Responsibility**: User authentication, authorization, preferences

**Technology**:
- Go with JWT tokens
- `golang.org/x/crypto/bcrypt` for passwords

**Data Store**: PostgreSQL

**Key Features**:
- User registration and login
- Role-based access control (RBAC)
- IP-based ACLs
- OAuth2 integration
- User preferences
- Session management
- Audit logging

**API**:
```
Register(username, password)
Login(username, password) → JWT
ValidateToken(token) → UserClaims
GetUserPreferences(userId)
```

---

### 11. Notification Service
**Responsibility**: User notifications and alerts

**Technology**:
- Go with WebSocket support
- Push notification integrations

**Key Features**:
- Real-time notifications (WebSocket)
- Email notifications
- Push notifications (mobile)
- Notification preferences
- Event subscriptions

**Notification Types**:
- Recording started/completed/failed
- EPG updates for favorites
- System alerts
- Storage warnings

---

### 12. Media Server Service
**Responsibility**: Stream delivery to clients

**Technology**:
- Go HTTP/2 server
- WebSocket for HTSP protocol
- HLS/DASH packaging

**Key Features**:
- Multiple streaming protocols:
  - HTSP (custom protocol, backward compat)
  - HTTP (progressive download)
  - HLS (adaptive streaming)
  - DASH (adaptive streaming)
  - WebRTC (ultra-low latency)
- Client capability detection
- Bandwidth adaptation
- Seeking support
- Subtitle delivery

**API**:
```
/stream/channel/{id}/stream.m3u8 (HLS)
/stream/channel/{id}/manifest.mpd (DASH)
ws://host/htsp (HTSP protocol)
```

---

## Technology Stack

### Core Technologies

| Component | Technology | Rationale |
|-----------|-----------|-----------|
| **Programming Language** | Go 1.21+ | Concurrency, performance, simplicity |
| **API Framework** | gRPC + grpc-gateway | Efficient RPC, auto-generated REST |
| **Message Broker** | NATS JetStream | Cloud-native, high performance |
| **Service Mesh** | Istio / Linkerd | Traffic management, observability |
| **Database** | PostgreSQL 15+ | ACID, jsonb, full-text search |
| **Cache** | Redis 7+ | Fast state storage, pub/sub |
| **Object Storage** | MinIO / S3 | Recording storage |
| **Search** | Meilisearch | EPG full-text search |
| **Monitoring** | Prometheus + Grafana | Metrics and dashboards |
| **Tracing** | Jaeger / Tempo | Distributed tracing |
| **Logging** | Loki + Promtail | Centralized logging |
| **Container** | Docker / Podman | Containerization |
| **Orchestration** | Kubernetes | Container orchestration |
| **CI/CD** | GitHub Actions | Automation |

### Key Go Libraries

```go
// API & Web
"google.golang.org/grpc"
"github.com/grpc-ecosystem/grpc-gateway/v2"
"github.com/99designs/gqlgen"
"github.com/gorilla/websocket"
"github.com/go-chi/chi/v5"

// MPEG-TS Processing
"github.com/Comcast/gots"
"github.com/ziutek/dvb"
"github.com/nareix/joy5" // RTMP/RTSP

// Database & ORM
"gorm.io/gorm"
"github.com/jackc/pgx/v5"
"github.com/redis/go-redis/v9"

// Message Queue
"github.com/nats-io/nats.go"
"github.com/nats-io/nats-streaming-server"

// FFmpeg
"github.com/u2takey/ffmpeg-go"
"github.com/asticode/go-astits" // TS parsing

// Scheduling
"github.com/robfig/cron/v3"
"github.com/jonboulle/clockwork"

// Authentication
"github.com/golang-jwt/jwt/v5"
"golang.org/x/crypto/bcrypt"

// Observability
"go.opentelemetry.io/otel"
"github.com/prometheus/client_golang"
"go.uber.org/zap" // Structured logging

// Configuration
"github.com/spf13/viper"
"github.com/kelseyhightower/envconfig"

// Testing
"github.com/stretchr/testify"
"github.com/golang/mock"
```

---

## Communication Patterns

### Synchronous Communication (gRPC)

Use for:
- Request-response patterns
- Real-time queries
- Service-to-service calls requiring immediate response

**Example**:
```protobuf
service ChannelService {
  rpc GetChannel(GetChannelRequest) returns (Channel);
  rpc ListChannels(ListChannelsRequest) returns (ListChannelsResponse);
  rpc CreateChannel(CreateChannelRequest) returns (Channel);
}
```

### Asynchronous Communication (NATS)

Use for:
- Event notifications
- Fire-and-forget operations
- Fan-out patterns
- Decoupling services

**Event Schema**:
```json
{
  "event_id": "uuid",
  "event_type": "recording.completed",
  "timestamp": "2025-10-23T10:30:00Z",
  "source_service": "dvr-service",
  "data": {
    "recording_id": "123",
    "channel_id": "456",
    "file_path": "/recordings/show.ts"
  }
}
```

**Subjects**:
```
input.>
stream.>
epg.>
dvr.>
channel.>
user.>
```

---

## Data Architecture

### Database per Service Pattern

Each service owns its data:
```
input-service → input-db (PostgreSQL)
epg-service → epg-db (PostgreSQL)
dvr-service → dvr-db (PostgreSQL)
channel-service → channel-db (PostgreSQL)
user-service → user-db (PostgreSQL)
```

### Shared Data Challenges

**EPG & Channel linkage**:
- Solution: Event-driven sync via message bus
- Channel service subscribes to EPG updates
- Eventual consistency model

**Cross-service queries**:
- Solution: API Gateway aggregation or GraphQL stitching
- BFF (Backend for Frontend) pattern for complex queries

### Caching Strategy

```
Redis Layer:
├─ Active stream metadata (1-5 min TTL)
├─ User sessions (token expiry)
├─ Channel list (invalidate on change)
└─ EPG current/next (5 min TTL)
```

---

## Streaming Architecture

### Stream Flow

```
Input Service → Stream Service → Subscribers
                      ↓
                 [Descrambler] → [Transcoder] → Media Server
                                                      ↓
                                              Clients / DVR
```

### Zero-Copy Stream Handling

```go
type StreamPacket struct {
    Data      []byte        // Shared memory buffer
    PID       uint16
    Timestamp time.Time
    Metadata  PacketMeta
}

// Stream subscribers receive references, not copies
type Subscriber interface {
    OnPacket(packet *StreamPacket) error
}
```

### Backpressure Handling

```go
// Buffered channels with monitoring
subscriber := make(chan *StreamPacket, 1000)

// Drop packets if subscriber slow
select {
case subscriber <- packet:
    // Delivered
default:
    metrics.DroppedPackets.Inc()
}
```

---

## Deployment Architecture

### Kubernetes Deployment

```yaml
# Example: DVR Service Deployment
apiVersion: apps/v1
kind: Deployment
metadata:
  name: dvr-service
spec:
  replicas: 2
  selector:
    matchLabels:
      app: dvr-service
  template:
    spec:
      containers:
      - name: dvr-service
        image: tvheadend/dvr-service:v2.0
        resources:
          requests:
            memory: "256Mi"
            cpu: "250m"
          limits:
            memory: "512Mi"
            cpu: "500m"
        env:
        - name: DATABASE_URL
          valueFrom:
            secretKeyRef:
              name: dvr-db-secret
              key: url
        - name: NATS_URL
          value: "nats://nats.tvheadend.svc:4222"
        ports:
        - containerPort: 8080
          name: grpc
        - containerPort: 9090
          name: metrics
        livenessProbe:
          grpc:
            port: 8080
          initialDelaySeconds: 10
        readinessProbe:
          grpc:
            port: 8080
          initialDelaySeconds: 5
```

### Service Mesh Configuration

```yaml
# Istio Virtual Service for traffic routing
apiVersion: networking.istio.io/v1beta1
kind: VirtualService
metadata:
  name: dvr-service
spec:
  hosts:
  - dvr-service
  http:
  - match:
    - headers:
        version:
          exact: v2
    route:
    - destination:
        host: dvr-service
        subset: v2
  - route:
    - destination:
        host: dvr-service
        subset: v1
```

### Scaling Strategy

| Service | Scaling Type | Trigger | Min | Max |
|---------|--------------|---------|-----|-----|
| API Gateway | Horizontal | CPU > 70% | 2 | 10 |
| Input Service | Vertical | Per hardware | 1 | 1 |
| Stream Service | Horizontal | Connections | 2 | 20 |
| EPG Service | Horizontal | CPU > 60% | 1 | 3 |
| DVR Service | Horizontal | Active recordings | 1 | 5 |
| Transcoding | Horizontal | Queue depth | 0 | 10 |
| Media Server | Horizontal | Bandwidth | 2 | 20 |

---

## Observability

### Metrics (Prometheus)

```go
// Example metrics per service
var (
    StreamsActive = prometheus.NewGauge(
        prometheus.GaugeOpts{
            Name: "tvh_streams_active_total",
            Help: "Number of active streams",
        },
    )

    PacketsProcessed = prometheus.NewCounter(
        prometheus.CounterOpts{
            Name: "tvh_packets_processed_total",
            Help: "Total MPEG-TS packets processed",
        },
    )

    StreamLatency = prometheus.NewHistogram(
        prometheus.HistogramOpts{
            Name: "tvh_stream_latency_seconds",
            Help: "Stream processing latency",
            Buckets: prometheus.DefBuckets,
        },
    )
)
```

### Tracing (OpenTelemetry)

```go
// Trace stream processing pipeline
func ProcessStream(ctx context.Context, streamID string) error {
    ctx, span := otel.Tracer("stream-service").Start(ctx, "ProcessStream")
    defer span.End()

    span.SetAttributes(attribute.String("stream.id", streamID))

    // Process...

    return nil
}
```

### Logging (Structured)

```go
logger.Info("recording started",
    zap.String("recording_id", recID),
    zap.String("channel", channel),
    zap.Time("scheduled_start", startTime),
)
```

### Health Checks

```go
// Each service implements health endpoint
func (s *Service) HealthCheck(ctx context.Context) error {
    // Check database connection
    if err := s.db.Ping(); err != nil {
        return fmt.Errorf("database unhealthy: %w", err)
    }

    // Check message bus
    if !s.nats.IsConnected() {
        return errors.New("nats disconnected")
    }

    return nil
}
```

---

## Security

### Authentication & Authorization

```
Client → API Gateway → JWT Validation → Service
                              ↓
                        User Service
```

**JWT Claims**:
```json
{
  "sub": "user_id",
  "roles": ["admin", "user"],
  "permissions": ["channel:read", "dvr:write"],
  "exp": 1730000000
}
```

### Service-to-Service Auth

- mTLS via service mesh
- Service accounts with limited permissions
- Certificate rotation

### Secrets Management

- Kubernetes Secrets for sensitive config
- HashiCorp Vault for dynamic secrets
- Encrypted environment variables

---

## Migration Strategy

### Phase 1: Strangler Fig Pattern

Keep existing tvheadend running, gradually extract services:

```
┌────────────────────────────────┐
│   Existing Tvheadend (C)       │
│                                 │
│  ┌─────────────┐               │     ┌──────────────┐
│  │  EPG (C)    │───────────────┼────>│ EPG Service  │
│  └─────────────┘               │     │    (Go)      │
│                                 │     └──────────────┘
│  ┌─────────────┐               │
│  │  DVR (C)    │               │     ┌──────────────┐
│  └─────────────┘───────────────┼────>│ DVR Service  │
│                                 │     │    (Go)      │
└────────────────────────────────┘     └──────────────┘
```

### Phase 2: Extract by Priority

1. **Week 1-4**: EPG Service (least coupled)
2. **Week 5-8**: DVR Service (high value)
3. **Week 9-12**: User/Auth Service
4. **Week 13-16**: Channel Service
5. **Week 17-24**: Stream Processing (most complex)
6. **Week 25-28**: Input Services
7. **Week 29-32**: Media Server
8. **Week 33-36**: Descrambler & Transcoding

### Phase 3: Cutover

- Run both systems in parallel
- Gradually route traffic to new services
- Monitor metrics and rollback if needed
- Decommission old monolith

---

## API Examples

### gRPC Service Definition

```protobuf
syntax = "proto3";

package tvheadend.dvr.v1;

import "google/protobuf/timestamp.proto";

service DVRService {
  rpc ScheduleRecording(ScheduleRecordingRequest) returns (Recording);
  rpc ListRecordings(ListRecordingsRequest) returns (ListRecordingsResponse);
  rpc DeleteRecording(DeleteRecordingRequest) returns (DeleteRecordingResponse);
  rpc CreateAutoRecRule(CreateAutoRecRuleRequest) returns (AutoRecRule);
}

message Recording {
  string id = 1;
  string channel_id = 2;
  string programme_id = 3;
  google.protobuf.Timestamp start_time = 4;
  google.protobuf.Timestamp end_time = 5;
  RecordingStatus status = 6;
  string file_path = 7;
  int64 file_size = 8;
}

enum RecordingStatus {
  RECORDING_STATUS_UNSPECIFIED = 0;
  RECORDING_STATUS_SCHEDULED = 1;
  RECORDING_STATUS_RECORDING = 2;
  RECORDING_STATUS_COMPLETED = 3;
  RECORDING_STATUS_FAILED = 4;
  RECORDING_STATUS_CANCELLED = 5;
}
```

### REST API (via grpc-gateway)

```
GET  /api/v1/recordings
POST /api/v1/recordings
GET  /api/v1/recordings/{id}
DELETE /api/v1/recordings/{id}
POST /api/v1/recordings/autorec
```

---

## Performance Considerations

### Go Concurrency for Stream Processing

```go
// Process multiple streams concurrently
func (s *StreamService) ProcessStreams(ctx context.Context) {
    var wg sync.WaitGroup

    for _, stream := range s.activeStreams {
        wg.Add(1)
        go func(st *Stream) {
            defer wg.Done()
            s.processStream(ctx, st)
        }(stream)
    }

    wg.Wait()
}

// Worker pool for packet processing
func (s *StreamService) startWorkerPool(ctx context.Context) {
    for i := 0; i < runtime.NumCPU(); i++ {
        go s.packetWorker(ctx, s.packetQueue)
    }
}
```

### Memory Management

```go
// Use sync.Pool for packet buffers
var packetPool = sync.Pool{
    New: func() interface{} {
        return &Packet{
            Data: make([]byte, 188), // TS packet size
        }
    },
}

func getPacket() *Packet {
    return packetPool.Get().(*Packet)
}

func putPacket(p *Packet) {
    p.Reset()
    packetPool.Put(p)
}
```

### Database Optimization

```sql
-- Partitioning for EPG table
CREATE TABLE epg_programmes (
    id BIGSERIAL,
    channel_id INTEGER,
    start_time TIMESTAMP,
    end_time TIMESTAMP,
    title TEXT,
    ...
) PARTITION BY RANGE (start_time);

CREATE INDEX idx_epg_channel_time ON epg_programmes(channel_id, start_time);
```

---

## Development Workflow

### Project Structure

```
tvheadend-v2/
├── api/
│   ├── proto/              # Protocol buffer definitions
│   └── openapi/            # OpenAPI specs
├── cmd/
│   ├── dvr-service/
│   ├── epg-service/
│   └── ...
├── internal/
│   ├── dvr/                # DVR domain logic
│   ├── epg/
│   └── ...
├── pkg/
│   ├── mpegts/             # Shared MPEG-TS library
│   ├── auth/               # Shared auth
│   └── observability/
├── deployments/
│   ├── kubernetes/
│   └── docker-compose/
├── docs/
├── go.mod
└── Makefile
```

### Makefile Targets

```makefile
.PHONY: proto
proto:
	buf generate

.PHONY: test
test:
	go test -v -race ./...

.PHONY: build
build:
	go build -o bin/ ./cmd/...

.PHONY: docker-build
docker-build:
	docker build -t tvheadend/dvr-service:latest -f cmd/dvr-service/Dockerfile .

.PHONY: k8s-deploy
k8s-deploy:
	kubectl apply -f deployments/kubernetes/
```

---

## Testing Strategy

### Unit Tests
```go
func TestScheduleRecording(t *testing.T) {
    repo := &mock.RecordingRepository{}
    svc := NewDVRService(repo)

    rec, err := svc.ScheduleRecording(ctx, req)

    assert.NoError(t, err)
    assert.Equal(t, StatusScheduled, rec.Status)
}
```

### Integration Tests
```go
func TestDVRServiceIntegration(t *testing.T) {
    db := setupTestDB(t)
    nats := setupTestNATS(t)

    svc := NewDVRService(db, nats)

    // Test full flow
}
```

### End-to-End Tests
- Test complete user flows
- Use testcontainers for dependencies
- CI pipeline integration

---

## Cost Optimization

### Resource Allocation

- **Stateless services**: Aggressive auto-scaling
- **Stateful services**: Vertical scaling with limits
- **Transcoding**: Spot instances / preemptible nodes
- **Storage**: Tiered storage (hot: NVMe, warm: HDD, cold: S3 Glacier)

### Multi-tenancy

- Share infrastructure across multiple deployments
- Namespace isolation in Kubernetes
- Resource quotas per tenant

---

## Recommended Next Steps

1. **Proof of Concept**: Build EPG service first (simplest, high value)
2. **Define API Contracts**: Create .proto files for all services
3. **Setup Infrastructure**: Kubernetes cluster, databases, message bus
4. **Build Core Libraries**: MPEG-TS parsing, auth, observability
5. **Implement Services**: One at a time, with comprehensive tests
6. **Create Migration Tools**: Data migration from old to new
7. **Parallel Run**: Both systems for validation period
8. **Gradual Cutover**: Service by service

---

## Conclusion

This microservices architecture provides:
- **Scalability**: Horizontal scaling of individual components
- **Resilience**: Fault isolation prevents cascading failures
- **Velocity**: Teams can deploy services independently
- **Technology Flexibility**: Can use best tool per service
- **Cloud-Native**: Container-ready, observable, declarative

Go is an excellent choice due to:
- Native concurrency (goroutines for stream processing)
- Fast compilation and deployment
- Strong standard library
- Excellent tooling
- Great performance characteristics

The migration can be incremental using the strangler fig pattern, minimizing risk while delivering value early.
