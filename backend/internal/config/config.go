package config

import (
	"fmt"
	"os"
)

// Config holds all backend configuration, loaded entirely from environment
// variables so the same container image works locally and on any platform.
type Config struct {
	HTTPPort    string
	DatabaseURL string
	RedisURL    string
	Timezone    string
}

// Load reads configuration from the environment. DATABASE_URL and REDIS_URL
// are required; everything else has a sane local-dev default.
func Load() (Config, error) {
	cfg := Config{
		HTTPPort:    getEnv("HTTP_PORT", "8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		RedisURL:    os.Getenv("REDIS_URL"),
		Timezone:    getEnv("APP_TIMEZONE", "Asia/Jakarta"),
	}

	if cfg.DatabaseURL == "" {
		return cfg, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.RedisURL == "" {
		return cfg, fmt.Errorf("REDIS_URL is required")
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
