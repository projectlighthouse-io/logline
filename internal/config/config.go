package config

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Port           int
	Env            string
	LogLevel       string
	DatabaseURL    string
	DBMaxConns     int
	DBMaxIdle      int
	RateLimit      float64
	RateLimitBurst int
	CORSOrigins    []string
	RetentionDays  int
	Version        string
	Commit         string
	BuildTime      string
}

func LoadConfig() (Config, error) {
	cfg := Config{
		Port:           4000,
		Env:            "development",
		LogLevel:       "info",
		DatabaseURL:    "postgres://logline:password@localhost:5432/logline?sslmode=disable",
		DBMaxConns:     25,
		DBMaxIdle:      5,
		RateLimit:      100,
		RateLimitBurst: 200,
		CORSOrigins:    []string{},
		RetentionDays:  30,
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
	if value := os.Getenv("PORT"); value != "" {
		if port, err := strconv.Atoi(value); err == nil {
			c.Port = port
		}
	}

	if value := os.Getenv("APP_ENV"); value != "" {
		c.Env = value
	}

	if value := os.Getenv("LOG_LEVEL"); value != "" {
		c.LogLevel = value
	}

	if value := os.Getenv("DATABASE_URL"); value != "" {
		c.DatabaseURL = value
	}

	if value := os.Getenv("DB_MAX_CONNS"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			c.DBMaxConns = parsed
		}
	}

	if value := os.Getenv("DB_MAX_IDLE"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			c.DBMaxIdle = parsed
		}
	}

	if value := os.Getenv("RATE_LIMIT"); value != "" {
		if rateLimit, err := strconv.ParseFloat(value, 64); err == nil {
			c.RateLimit = rateLimit
		}
	}

	if value := os.Getenv("RATE_LIMIT_BURST"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			c.RateLimitBurst = parsed
		}
	}

	if value := os.Getenv("CORS_ORIGINS"); value != "" {
		c.CORSOrigins = strings.Split(value, ",")
	}

	if value := os.Getenv("RETENTION_DAYS"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			c.RetentionDays = parsed
		}
	}
}

func (c *Config) loadFlags() error {
	flagSet := flag.NewFlagSet("logline", flag.ContinueOnError)

	flagSet.IntVar(&c.Port, "port", c.Port, "HTTP server port")
	flagSet.StringVar(&c.Env, "env", c.Env, "Environment (development, production)")
	flagSet.StringVar(&c.LogLevel, "log-level", c.LogLevel, "Log level (debug, info, warn, error)")

	return flagSet.Parse(os.Args[1:])
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
