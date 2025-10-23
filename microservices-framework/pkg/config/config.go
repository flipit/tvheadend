package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds application configuration
type Config struct {
	// Service
	ServiceName string
	Environment string

	// Server ports
	GRPCPort    int
	HTTPPort    int
	MetricsPort int

	// Database
	DatabaseURL string

	// Message broker
	NATSUrl string

	// Cache
	RedisURL string

	// Observability
	JaegerEndpoint string
	LogLevel       string

	// Storage
	RecordingsPath string
	S3Endpoint     string
	S3AccessKey    string
	S3SecretKey    string
	S3Bucket       string
}

// Load loads configuration from environment variables
func Load() (*Config, error) {
	cfg := &Config{
		ServiceName:    getEnv("SERVICE_NAME", "tvheadend-service"),
		Environment:    getEnv("ENVIRONMENT", "development"),
		GRPCPort:       getEnvAsInt("GRPC_PORT", 8080),
		HTTPPort:       getEnvAsInt("HTTP_PORT", 9090),
		MetricsPort:    getEnvAsInt("METRICS_PORT", 9091),
		DatabaseURL:    getEnv("DATABASE_URL", ""),
		NATSUrl:        getEnv("NATS_URL", "nats://localhost:4222"),
		RedisURL:       getEnv("REDIS_URL", "redis://localhost:6379"),
		JaegerEndpoint: getEnv("JAEGER_ENDPOINT", "http://localhost:14268/api/traces"),
		LogLevel:       getEnv("LOG_LEVEL", "info"),
		RecordingsPath: getEnv("RECORDINGS_PATH", "/data/recordings"),
		S3Endpoint:     getEnv("S3_ENDPOINT", ""),
		S3AccessKey:    getEnv("S3_ACCESS_KEY", ""),
		S3SecretKey:    getEnv("S3_SECRET_KEY", ""),
		S3Bucket:       getEnv("S3_BUCKET", "recordings"),
	}

	// Validate required fields
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}

	return cfg, nil
}

// getEnv gets an environment variable or returns a default value
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvAsInt gets an environment variable as int or returns default
func getEnvAsInt(key string, defaultValue int) int {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}

	value, err := strconv.Atoi(valueStr)
	if err != nil {
		return defaultValue
	}

	return value
}

// getEnvAsBool gets an environment variable as bool or returns default
func getEnvAsBool(key string, defaultValue bool) bool {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}

	value, err := strconv.ParseBool(valueStr)
	if err != nil {
		return defaultValue
	}

	return value
}
