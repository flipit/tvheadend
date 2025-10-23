package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/jaeger"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	inputv1 "github.com/tvheadend/api/input/v1"
	"github.com/tvheadend/internal/input"
	"github.com/tvheadend/pkg/config"
)

const (
	serviceName    = "input-service"
	serviceVersion = "2.0.0"
)

func main() {
	// Initialize logger
	logger, err := zap.NewProduction()
	if err != nil {
		panic(fmt.Sprintf("failed to create logger: %v", err))
	}
	defer logger.Sync()

	logger.Info("starting input service", zap.String("version", serviceVersion))

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		logger.Fatal("failed to load configuration", zap.Error(err))
	}

	// Initialize OpenTelemetry tracing
	tp, err := initTracer(cfg.JaegerEndpoint)
	if err != nil {
		logger.Fatal("failed to initialize tracer", zap.Error(err))
	}
	defer func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			logger.Error("failed to shutdown tracer", zap.Error(err))
		}
	}()

	// Connect to database
	db, err := initDatabase(cfg.DatabaseURL, logger)
	if err != nil {
		logger.Fatal("failed to connect to database", zap.Error(err))
	}

	// Connect to NATS
	nc, err := nats.Connect(cfg.NATSUrl,
		nats.Name(serviceName),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
	)
	if err != nil {
		logger.Fatal("failed to connect to NATS", zap.Error(err))
	}
	defer nc.Close()

	// Initialize JetStream
	js, err := nc.JetStream()
	if err != nil {
		logger.Fatal("failed to initialize JetStream", zap.Error(err))
	}

	// Create Input service implementation
	inputRepo := input.NewRepository(db)
	inputService := input.NewService(inputRepo, js, logger)

	// Start background workers
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go inputService.StartAdapterDiscovery(ctx)
	go inputService.MonitorSignalQuality(ctx)

	// Start gRPC server
	grpcServer := newGRPCServer(inputService, logger)
	grpcListener, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.GRPCPort))
	if err != nil {
		logger.Fatal("failed to create gRPC listener", zap.Error(err))
	}

	go func() {
		logger.Info("starting gRPC server", zap.Int("port", cfg.GRPCPort))
		if err := grpcServer.Serve(grpcListener); err != nil {
			logger.Fatal("gRPC server failed", zap.Error(err))
		}
	}()

	// Start HTTP gateway server
	httpServer := newHTTPGateway(ctx, cfg, logger)
	go func() {
		logger.Info("starting HTTP gateway", zap.Int("port", cfg.HTTPPort))
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("HTTP server failed", zap.Error(err))
		}
	}()

	// Start metrics server
	metricsServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.MetricsPort),
		Handler: promhttp.Handler(),
	}
	go func() {
		logger.Info("starting metrics server", zap.Int("port", cfg.MetricsPort))
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("metrics server failed", zap.Error(err))
		}
	}()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	logger.Info("shutting down gracefully...")

	// Graceful shutdown
	cancel()
	grpcServer.GracefulStop()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown failed", zap.Error(err))
	}
	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("metrics server shutdown failed", zap.Error(err))
	}

	logger.Info("service stopped")
}

func initTracer(endpoint string) (*sdktrace.TracerProvider, error) {
	exporter, err := jaeger.New(jaeger.WithCollectorEndpoint(jaeger.WithEndpoint(endpoint)))
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String(serviceName),
			semconv.ServiceVersionKey.String(serviceVersion),
		)),
	)

	otel.SetTracerProvider(tp)
	return tp, nil
}

func initDatabase(url string, logger *zap.Logger) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(url), &gorm.Config{})
	if err != nil {
		return nil, err
	}

	// Auto-migrate schemas
	if err := input.AutoMigrate(db); err != nil {
		return nil, err
	}

	// Configure connection pool
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}

	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(100)
	sqlDB.SetConnMaxLifetime(time.Hour)

	return db, nil
}

func newGRPCServer(svc *input.Service, logger *zap.Logger) *grpc.Server {
	// Create gRPC server with interceptors
	server := grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(
			loggingInterceptor(logger),
			recoveryInterceptor(logger),
		),
	)

	// Register services
	inputv1.RegisterInputServiceServer(server, svc)

	// Register health check
	healthServer := health.NewServer()
	healthServer.SetServingStatus(serviceName, grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(server, healthServer)

	// Enable reflection for debugging
	reflection.Register(server)

	return server
}

func newHTTPGateway(ctx context.Context, cfg *config.Config, logger *zap.Logger) *http.Server {
	// Create gRPC-Gateway mux
	mux := runtime.NewServeMux(
		runtime.WithHealthzEndpoint(grpc_health_v1.NewHealthClient(nil)),
	)

	// Register gRPC-Gateway handlers
	opts := []grpc.DialOption{grpc.WithInsecure()}
	endpoint := fmt.Sprintf("localhost:%d", cfg.GRPCPort)

	if err := inputv1.RegisterInputServiceHandlerFromEndpoint(ctx, mux, endpoint, opts); err != nil {
		logger.Fatal("failed to register gateway", zap.Error(err))
	}

	// Add CORS middleware
	handler := corsMiddleware(mux)

	return &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler: handler,
	}
}

func loggingInterceptor(logger *zap.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		duration := time.Since(start)

		if err != nil {
			logger.Error("grpc call failed",
				zap.String("method", info.FullMethod),
				zap.Duration("duration", duration),
				zap.Error(err),
			)
		} else {
			logger.Info("grpc call completed",
				zap.String("method", info.FullMethod),
				zap.Duration("duration", duration),
			)
		}

		return resp, err
	}
}

func recoveryInterceptor(logger *zap.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("panic recovered",
					zap.String("method", info.FullMethod),
					zap.Any("panic", r),
				)
				err = fmt.Errorf("internal server error")
			}
		}()
		return handler(ctx, req)
	}
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Add methods to Service that are called in main but not yet defined
func (s *input.Service) StartAdapterDiscovery(ctx context.Context) {
	s.logger.Info("starting adapter discovery")
	// TODO: Implement adapter discovery
	// - Scan for DVB adapters in /dev/dvb/
	// - Discover SAT>IP servers via SSDP
	// - Register found adapters in database

	<-ctx.Done()
	s.logger.Info("adapter discovery stopped")
}

func (s *input.Service) MonitorSignalQuality(ctx context.Context) {
	s.logger.Info("starting signal quality monitor")

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("signal quality monitor stopped")
			return
		case <-ticker.C:
			// TODO: Query signal quality for active inputs
			// - Publish quality updates to NATS
			// - Log quality degradation
		}
	}
}
