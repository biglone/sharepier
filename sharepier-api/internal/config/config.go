package config

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	AppName                string
	DatabaseURL            string
	Env                    string
	Host                   string
	Port                   int
	LogLevel               string
	MigrationsDir          string
	AllowedOrigins         []string
	SessionTTL             time.Duration
	SessionCookie          string
	SecureCookies          bool
	StorageRoot            string
	PublicBaseURL          string
	MaxUploadSize          int64
	ResumableChunkSize     int64
	UploadSessionTTL       time.Duration
	AdminBootstrapUsername string
	AdminBootstrapPassword string
}

func Load() Config {
	env := getEnv("SHAREPIER_ENV", "development")
	secureCookiesDefault := env != "development"

	return Config{
		AppName:                getEnv("SHAREPIER_APP_NAME", "SharePier API"),
		DatabaseURL:            getEnv("DATABASE_URL", "postgres://sharepier:sharepier@localhost:5432/sharepier?sslmode=disable"),
		Env:                    env,
		Host:                   getEnv("SHAREPIER_HOST", "0.0.0.0"),
		Port:                   getEnvAsInt("SHAREPIER_PORT", 8080),
		LogLevel:               getEnv("SHAREPIER_LOG_LEVEL", "info"),
		MigrationsDir:          getEnv("SHAREPIER_MIGRATIONS_DIR", "./migrations"),
		AllowedOrigins:         splitCSV(getEnv("SHAREPIER_ALLOWED_ORIGINS", "http://localhost:3000,http://127.0.0.1:3000,http://localhost:5173,http://127.0.0.1:5173")),
		SessionTTL:             getEnvAsDuration("SHAREPIER_SESSION_TTL", 168*time.Hour),
		SessionCookie:          getEnv("SHAREPIER_SESSION_COOKIE_NAME", "sharepier_session"),
		SecureCookies:          getEnvAsBool("SHAREPIER_SECURE_COOKIES", secureCookiesDefault),
		StorageRoot:            getEnv("SHAREPIER_STORAGE_ROOT", "./data/storage"),
		PublicBaseURL:          getEnv("SHAREPIER_PUBLIC_BASE_URL", "http://localhost:8080"),
		MaxUploadSize:          getEnvAsInt64("SHAREPIER_MAX_UPLOAD_SIZE", 100<<20),
		ResumableChunkSize:     getEnvAsInt64("SHAREPIER_RESUMABLE_CHUNK_SIZE", 8<<20),
		UploadSessionTTL:       getEnvAsDuration("SHAREPIER_UPLOAD_SESSION_TTL", 24*time.Hour),
		AdminBootstrapUsername: getEnv("SHAREPIER_ADMIN_USERNAME", ""),
		AdminBootstrapPassword: getEnv("SHAREPIER_ADMIN_PASSWORD", ""),
	}
}

func ParseLogLevel(value string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && strings.TrimSpace(value) != "" {
		return value
	}

	return fallback
}

func getEnvAsInt(key string, fallback int) int {
	value := getEnv(key, "")
	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}

	return parsed
}

func getEnvAsInt64(key string, fallback int64) int64 {
	value := getEnv(key, "")
	if value == "" {
		return fallback
	}

	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}

	return parsed
}

func getEnvAsBool(key string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(getEnv(key, "")))
	if value == "" {
		return fallback
	}

	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func getEnvAsDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(getEnv(key, ""))
	if value == "" {
		return fallback
	}

	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}

	return parsed
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item != "" {
			items = append(items, item)
		}
	}

	return items
}
