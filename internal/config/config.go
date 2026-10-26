package config

import (
	"crypto/rand"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the process configuration. Secrets come from the environment.
type Config struct {
	HTTPAddr             string
	DataDir              string
	PublicBaseURL        string
	SessionSecret        []byte
	SessionSecretFromEnv bool
	CookieSecure         bool
	AdminUsername        string
	AdminPassword        string
	TelegramBotToken     string
	TelegramToolMode     bool
	LLMAPIKey            string
	LogLevel             string
	TrustedProxyCIDRs    []string
	FXBackfillDays       int
	TelegramPollTimeout  time.Duration
	MaxActiveUsers       int
	Timezone             string
}

// Load reads configuration from environment variables and applies safe defaults.
func Load() (Config, error) {
	c := Config{
		HTTPAddr:            env("SKARB_HTTP_ADDR", ":8080"),
		DataDir:             env("SKARB_DATA_DIR", "./data"),
		PublicBaseURL:       strings.TrimRight(env("SKARB_PUBLIC_BASE_URL", "http://localhost:8080"), "/"),
		CookieSecure:        envBool("SKARB_COOKIE_SECURE", false),
		AdminUsername:       env("SKARB_ADMIN_USERNAME", ""),
		AdminPassword:       env("SKARB_ADMIN_PASSWORD", ""),
		TelegramBotToken:    env("SKARB_TELEGRAM_BOT_TOKEN", ""),
		TelegramToolMode:    envBool("SKARB_TELEGRAM_TOOL_MODE", true),
		LLMAPIKey:           env("SKARB_LLM_API_KEY", ""),
		LogLevel:            strings.ToLower(env("SKARB_LOG_LEVEL", "info")),
		MaxActiveUsers:      envInt("SKARB_MAX_ACTIVE_USERS", 2),
		Timezone:            env("SKARB_TZ", "Europe/Kyiv"),
		FXBackfillDays:      envInt("SKARB_FX_BACKFILL_DAYS", 730),
		TelegramPollTimeout: envDuration("SKARB_TELEGRAM_POLL_TIMEOUT", 30*time.Second),
	}

	if raw := os.Getenv("SKARB_TRUSTED_PROXY_CIDRS"); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				c.TrustedProxyCIDRs = append(c.TrustedProxyCIDRs, part)
			}
		}
	}

	if raw := os.Getenv("SKARB_SESSION_SECRET"); raw != "" {
		c.SessionSecret = []byte(raw)
		c.SessionSecretFromEnv = true
	} else {
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return Config{}, fmt.Errorf("generate session secret: %w", err)
		}
		c.SessionSecret = secret
	}

	if c.MaxActiveUsers < 1 {
		return Config{}, fmt.Errorf("SKARB_MAX_ACTIVE_USERS must be at least 1")
	}
	if c.FXBackfillDays < 0 {
		return Config{}, fmt.Errorf("SKARB_FX_BACKFILL_DAYS must not be negative")
	}
	if (c.AdminUsername == "") != (c.AdminPassword == "") {
		return Config{}, fmt.Errorf("SKARB_ADMIN_USERNAME and SKARB_ADMIN_PASSWORD must be set together")
	}
	if c.Timezone == "" {
		c.Timezone = "Europe/Kyiv"
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		return Config{}, fmt.Errorf("invalid SKARB_TZ: %w", err)
	}
	return c, nil
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return v
}

func envInt(key string, fallback int) int {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return v
}

func envDuration(key string, fallback time.Duration) time.Duration {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return v
}
