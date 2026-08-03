package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds process-wide settings loaded from env.
type Config struct {
	ServiceName    string
	HTTPAddr       string
	GRPCAddr       string
	RedisURL       string
	NATSURL        string
	PostgresURL    string
	OTLPEndpoint   string
	JWTSecret      string
	APIKeys        []string
	ClusterName    string
	LogLevel       string
	MetricsAddr    string
	GatewayURL     string
	RouterURL      string
	SchedulerURL   string
	RegistryURL    string
	HeartbeatEvery time.Duration
	RequestTimeout time.Duration
}

// Load reads configuration from environment variables.
func Load(service string) Config {
	return Config{
		ServiceName:    service,
		HTTPAddr:       getEnv("HTTP_ADDR", ":8080"),
		GRPCAddr:       getEnv("GRPC_ADDR", ":9090"),
		RedisURL:       getEnv("REDIS_URL", "redis://127.0.0.1:6379"),
		NATSURL:        getEnv("NATS_URL", "nats://127.0.0.1:4222"),
		PostgresURL:    getEnv("POSTGRES_URL", "postgres://lattice:lattice@127.0.0.1:5432/lattice?sslmode=disable"),
		OTLPEndpoint:   getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", "127.0.0.1:4318"),
		JWTSecret:      getEnv("JWT_SECRET", "lattice-dev-secret-change-me"),
		APIKeys:        splitCSV(getEnv("API_KEYS", "lattice-dev-key")),
		ClusterName:    getEnv("CLUSTER_NAME", "local"),
		LogLevel:       getEnv("LOG_LEVEL", "info"),
		MetricsAddr:    getEnv("METRICS_ADDR", ":2112"),
		GatewayURL:     getEnv("GATEWAY_URL", "http://127.0.0.1:8080"),
		RouterURL:      getEnv("ROUTER_URL", "http://127.0.0.1:8081"),
		SchedulerURL:   getEnv("SCHEDULER_URL", "http://127.0.0.1:8082"),
		RegistryURL:    getEnv("REGISTRY_URL", "http://127.0.0.1:8083"),
		HeartbeatEvery: getDuration("HEARTBEAT_EVERY", 5*time.Second),
		RequestTimeout: getDuration("REQUEST_TIMEOUT", 120*time.Second),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

func GetInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func GetFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			return n
		}
	}
	return fallback
}

func GetBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
