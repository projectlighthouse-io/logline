package config

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Port        int
	Env         string
	LogLevel    string
	DatabaseURL string
	DBMaxConns     int
	DBMaxIdle      int
	RateLimit      float64
	RateLimitBurst int
	CORSOrigins    []string
}

func LoadConfig() (Config, error) {
	cfg := Config{
		Port:        4000,
		Env:         "development",
		LogLevel:    "info",
		DatabaseURL: "postgres://logline:password@localhost:5433/logline?sslmode=disable",
		DBMaxConns:     25,
		DBMaxIdle:      5,
		RateLimit:      100,
		RateLimitBurst: 200,
		CORSOrigins:    []string{},
	}

	cfg.loadEnv()

	if err := cfg.loadFlags(); err != nil {
		return Config{}, err
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c *Config) loadEnv() {
	if v := os.Getenv("PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			c.Port = p
		}
	}

	if v := os.Getenv("APP_ENV"); v != "" {
		c.Env = v
	}

	if v := os.Getenv("LOG_LEVEL"); v != "" {
		c.LogLevel = v
	}

	if v := os.Getenv("DATABASE_URL"); v != "" {
		c.DatabaseURL = v
	}

	if v := os.Getenv("DB_MAX_CONNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.DBMaxConns = n
		}
	}

	if v := os.Getenv("DB_MAX_IDLE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.DBMaxIdle = n
		}
	}

	if v := os.Getenv("RATE_LIMIT"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			c.RateLimit = f
		}
	}

	if v := os.Getenv("RATE_LIMIT_BURST"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.RateLimitBurst = n
		}
	}

	if v := os.Getenv("CORS_ORIGINS"); v != "" {
		c.CORSOrigins = strings.Split(v, ",")
	}
}

func (c *Config) loadFlags() error {
	fs := flag.NewFlagSet("logline", flag.ContinueOnError)

	fs.IntVar(&c.Port, "port", c.Port, "HTTP server port")
	fs.StringVar(&c.Env, "env", c.Env, "Environment (development, production)")
	fs.StringVar(&c.LogLevel, "log-level", c.LogLevel, "Log level (debug, info, warn, error)")

	return fs.Parse(os.Args[1:])
}

func (c *Config) validate() error {
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535, got %d", c.Port)
	}

	switch c.Env {
	case "development", "production":
		// valid
	default:
		return fmt.Errorf("env must be 'development' or 'production', got %q", c.Env)
	}

	switch c.LogLevel {
	case "debug", "info", "warn", "error":
		// valid
	default:
		return fmt.Errorf("log-level must be one of: debug, info, warn, error — got %q", c.LogLevel)
	}

	if c.DatabaseURL == "" {
		return fmt.Errorf("database URL is required")
	}

	if c.DBMaxConns < 1 {
		return fmt.Errorf("DB_MAX_CONNS must be at least 1, got %d", c.DBMaxConns)
	}

	if c.DBMaxIdle < 0 {
		return fmt.Errorf("DB_MAX_IDLE must be non-negative, got %d", c.DBMaxIdle)
	}

	return nil
}
