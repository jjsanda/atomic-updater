// Package config loads server configuration from the environment
// (documented in docs/api.md §4).
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DBURL string

	S3Endpoint       string
	S3PublicEndpoint string
	S3AccessKey      string
	S3SecretKey      string
	S3Bucket         string
	S3UseSSL         bool

	DeviceAddr string
	AdminAddr  string

	TLSCert      string
	TLSKey       string
	DeviceCACert string
	DeviceCAKey  string
	MfgCACert    string
	CAChain      string

	AdminUser         string
	AdminPasswordHash string

	PresignTTL         time.Duration
	BundlePollInterval time.Duration
	PauseMinCohort     int
	PauseFailPct       int

	LogLevel slog.Level
}

func FromEnv() (*Config, error) {
	c := &Config{
		DBURL:             os.Getenv("ATOMIC_DB_URL"),
		S3Endpoint:        os.Getenv("ATOMIC_S3_ENDPOINT"),
		S3PublicEndpoint:  os.Getenv("ATOMIC_S3_PUBLIC_ENDPOINT"),
		S3AccessKey:       os.Getenv("ATOMIC_S3_ACCESS_KEY"),
		S3SecretKey:       os.Getenv("ATOMIC_S3_SECRET_KEY"),
		S3Bucket:          envDefault("ATOMIC_S3_BUCKET", "bundles"),
		DeviceAddr:        envDefault("ATOMIC_DEVICE_ADDR", ":8443"),
		AdminAddr:         envDefault("ATOMIC_ADMIN_ADDR", ":8080"),
		TLSCert:           os.Getenv("ATOMIC_TLS_CERT"),
		TLSKey:            os.Getenv("ATOMIC_TLS_KEY"),
		DeviceCACert:      os.Getenv("ATOMIC_DEVICE_CA_CERT"),
		DeviceCAKey:       os.Getenv("ATOMIC_DEVICE_CA_KEY"),
		MfgCACert:         os.Getenv("ATOMIC_MFG_CA_CERT"),
		CAChain:           os.Getenv("ATOMIC_CA_CHAIN"),
		AdminUser:         envDefault("ATOMIC_ADMIN_USER", "admin"),
		AdminPasswordHash: os.Getenv("ATOMIC_ADMIN_PASSWORD_HASH"),
	}

	var err error
	if c.S3UseSSL, err = envBool("ATOMIC_S3_USE_SSL", false); err != nil {
		return nil, err
	}
	if c.PresignTTL, err = envDuration("ATOMIC_PRESIGN_TTL", 15*time.Minute); err != nil {
		return nil, err
	}
	if c.BundlePollInterval, err = envDuration("ATOMIC_BUNDLE_POLL_INTERVAL", 30*time.Second); err != nil {
		return nil, err
	}
	if c.PauseMinCohort, err = envInt("ATOMIC_PAUSE_MIN_COHORT", 25); err != nil {
		return nil, err
	}
	if c.PauseFailPct, err = envInt("ATOMIC_PAUSE_FAIL_PCT", 2); err != nil {
		return nil, err
	}
	switch strings.ToLower(envDefault("ATOMIC_LOG_LEVEL", "info")) {
	case "debug":
		c.LogLevel = slog.LevelDebug
	case "info":
		c.LogLevel = slog.LevelInfo
	case "warn":
		c.LogLevel = slog.LevelWarn
	case "error":
		c.LogLevel = slog.LevelError
	default:
		return nil, fmt.Errorf("ATOMIC_LOG_LEVEL: unknown level %q", os.Getenv("ATOMIC_LOG_LEVEL"))
	}
	if c.S3PublicEndpoint == "" {
		c.S3PublicEndpoint = c.S3Endpoint
	}

	var missing []string
	for name, v := range map[string]string{
		"ATOMIC_DB_URL":              c.DBURL,
		"ATOMIC_S3_ENDPOINT":         c.S3Endpoint,
		"ATOMIC_S3_ACCESS_KEY":       c.S3AccessKey,
		"ATOMIC_S3_SECRET_KEY":       c.S3SecretKey,
		"ATOMIC_TLS_CERT":            c.TLSCert,
		"ATOMIC_TLS_KEY":             c.TLSKey,
		"ATOMIC_DEVICE_CA_CERT":      c.DeviceCACert,
		"ATOMIC_DEVICE_CA_KEY":       c.DeviceCAKey,
		"ATOMIC_MFG_CA_CERT":         c.MfgCACert,
		"ATOMIC_CA_CHAIN":            c.CAChain,
		"ATOMIC_ADMIN_PASSWORD_HASH": c.AdminPasswordHash,
	} {
		if v == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment: %s", strings.Join(missing, ", "))
	}
	return c, nil
}

func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) (bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s: %w", key, err)
	}
	return b, nil
}

func envInt(key string, def int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return n, nil
}

func envDuration(key string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return d, nil
}
