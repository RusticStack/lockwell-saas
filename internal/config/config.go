package config

import (
	"errors"
	"os"
	"strings"
)

type Config struct {
	ListenAddr          string
	DatabaseURL         string
	StripeWebhookSecret string
}

func Load() (Config, error) {
	cfg := Config{
		ListenAddr:          strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_LISTEN_ADDR")),
		DatabaseURL:         strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_DATABASE_URL")),
		StripeWebhookSecret: strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_STRIPE_WEBHOOK_SECRET")),
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = "127.0.0.1:8080"
	}
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("LOCKWELL_SAAS_DATABASE_URL is required")
	}
	if cfg.StripeWebhookSecret == "" {
		return Config{}, errors.New("LOCKWELL_SAAS_STRIPE_WEBHOOK_SECRET is required")
	}
	return cfg, nil
}
