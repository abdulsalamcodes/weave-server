package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Server    ServerConfig
	Database  DatabaseConfig
	Redis     RedisConfig
	JWT       JWTConfig
	Auth      AuthConfig
	Paystack  PaystackConfig
	Mono      MonoConfig
	LLM       LLMConfig
	KYC       KYCConfig
	Ratelimit RatelimitConfig
	OTel      OTelConfig
}

type ServerConfig struct {
	Host        string
	Port        int
	Environment string
}

type DatabaseConfig struct {
	URL string
}

type RedisConfig struct {
	URL string
}

type JWTConfig struct {
	Secret     string
	AccessTTL  time.Duration
	RefreshTTL time.Duration
}

type AuthConfig struct {
	EncryptionKey    string
	BcryptCost       int
	MaxPINAttempts   int
	PINLockoutMins   int
}

type PaystackConfig struct {
	SecretKey string
	PublicKey string
	Bank      string
}

type MonoConfig struct {
	SecretKey string
}

type LLMConfig struct {
	APIKey  string
	Model   string
	BaseURL string // override for Ollama: http://localhost:11434/v1
}

type KYCConfig struct {
	Provider        string
	YouVerifyAPIKey string
}

type RatelimitConfig struct {
	Requests int
	Window   time.Duration
}

type OTelConfig struct {
	Enabled      bool
	ServiceName  string
	ServiceVersion string
	OTLPEndpoint string
}

func Load() (*Config, error) {
	port, err := intEnv("SERVER_PORT", 8080)
	if err != nil {
		return nil, fmt.Errorf("server port: %w", err)
	}

	accessTTL, err := durationEnv("JWT_ACCESS_TTL", 15*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("jwt access ttl: %w", err)
	}

	refreshTTL, err := durationEnv("JWT_REFRESH_TTL", 7*24*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("jwt refresh ttl: %w", err)
	}

	bcryptCost, err := intEnv("BCRYPT_COST", 12)
	if err != nil {
		return nil, fmt.Errorf("bcrypt cost: %w", err)
	}

	maxAttempts, err := intEnv("MAX_PIN_ATTEMPTS", 3)
	if err != nil {
		return nil, fmt.Errorf("max pin attempts: %w", err)
	}

	pinLockout, err := intEnv("PIN_LOCKOUT_MINS", 15)
	if err != nil {
		return nil, fmt.Errorf("pin lockout mins: %w", err)
	}

	rateLimitReqs, err := intEnv("RATE_LIMIT_REQUESTS", 100)
	if err != nil {
		return nil, fmt.Errorf("rate limit requests: %w", err)
	}

	rateLimitWindow, err := durationEnv("RATE_LIMIT_WINDOW", 1*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("rate limit window: %w", err)
	}

	return &Config{
		Server: ServerConfig{
			Host:        env("SERVER_HOST", "0.0.0.0"),
			Port:        port,
			Environment: env("ENVIRONMENT", "development"),
		},
		Database: DatabaseConfig{
			URL: env("DATABASE_URL", "postgres://weave:weave_dev@localhost:5432/weave?sslmode=disable"),
		},
		Redis: RedisConfig{
			URL: env("REDIS_URL", "redis://localhost:6379/0"),
		},
		JWT: JWTConfig{
			Secret:     env("JWT_SECRET", "dev-secret-change-in-production-min-32-chars!!"),
			AccessTTL:  accessTTL,
			RefreshTTL: refreshTTL,
		},
		Auth: AuthConfig{
			EncryptionKey:  env("ENCRYPTION_KEY", "dev-encryption-key-change-in-prod!!"),
			BcryptCost:     bcryptCost,
			MaxPINAttempts: maxAttempts,
			PINLockoutMins: pinLockout,
		},
		Paystack: PaystackConfig{
			SecretKey: env("PAYSTACK_SECRET_KEY", ""),
			PublicKey: env("PAYSTACK_PUBLIC_KEY", ""),
			Bank:      env("PAYSTACK_BANK", "test-bank"),
		},
		Mono: MonoConfig{
			SecretKey: env("MONO_SECRET_KEY", ""),
		},
		LLM: LLMConfig{
			APIKey:  env("LLM_API_KEY", ""),
			Model:   env("LLM_MODEL", "llama3.2"),
			BaseURL: env("LLM_BASE_URL", "http://localhost:11434/v1"),
		},
		KYC: KYCConfig{
			Provider:        env("KYC_PROVIDER", "youverify"),
			YouVerifyAPIKey: env("YOUVERIFY_API_KEY", ""),
		},
		Ratelimit: RatelimitConfig{
			Requests: rateLimitReqs,
			Window:   rateLimitWindow,
		},
		OTel: OTelConfig{
			Enabled:      env("OTEL_ENABLED", "false") == "true",
			ServiceName:  env("OTEL_SERVICE_NAME", "weave-server"),
			ServiceVersion: env("OTEL_SERVICE_VERSION", "1.0.0"),
			OTLPEndpoint: env("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317"),
		},
	}, nil
}

func env(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func intEnv(key string, fallback int) (int, error) {
	val := os.Getenv(key)
	if val == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %q", key, val)
	}
	return n, nil
}

func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	val := os.Getenv(key)
	if val == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(val)
	if err != nil {
		return 0, fmt.Errorf("invalid %s duration: %q: %w", key, val, err)
	}
	return d, nil
}
