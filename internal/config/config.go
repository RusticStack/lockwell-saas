package config

import (
	"errors"
	"os"
	"strings"
)

type Config struct {
	ListenAddr            string
	DatabaseURL           string
	StripeAPIKey          string
	StripeAPIVersion      string
	StripeWebhookSecret   string
	StripeStarterPrice    string
	StripeTeamPrice       string
	StripeCompliancePrice string
	CheckoutSuccessURL    string
	CheckoutCancelURL     string
	PortalReturnURL       string
	TermsVersion          string
}

func Load() (Config, error) {
	cfg := Config{
		ListenAddr:            strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_LISTEN_ADDR")),
		DatabaseURL:           strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_DATABASE_URL")),
		StripeAPIKey:          strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_STRIPE_API_KEY")),
		StripeAPIVersion:      strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_STRIPE_API_VERSION")),
		StripeWebhookSecret:   strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_STRIPE_WEBHOOK_SECRET")),
		StripeStarterPrice:    strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_STRIPE_STARTER_PRICE")),
		StripeTeamPrice:       strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_STRIPE_TEAM_PRICE")),
		StripeCompliancePrice: strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_STRIPE_COMPLIANCE_PRICE")),
		CheckoutSuccessURL:    strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_CHECKOUT_SUCCESS_URL")),
		CheckoutCancelURL:     strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_CHECKOUT_CANCEL_URL")),
		PortalReturnURL:       strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_PORTAL_RETURN_URL")),
		TermsVersion:          strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_TERMS_VERSION")),
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
	if cfg.StripeAPIKey == "" || cfg.StripeAPIVersion == "" || cfg.StripeStarterPrice == "" || cfg.StripeTeamPrice == "" || cfg.StripeCompliancePrice == "" {
		return Config{}, errors.New("Stripe API key and all allowlisted plan Price IDs are required")
	}
	if cfg.CheckoutSuccessURL == "" || cfg.CheckoutCancelURL == "" || cfg.PortalReturnURL == "" || cfg.TermsVersion == "" {
		return Config{}, errors.New("Checkout/Portal URLs and terms version are required")
	}
	return cfg, nil
}
