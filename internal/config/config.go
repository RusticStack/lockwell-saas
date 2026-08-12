package config

import (
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	ListenAddr                string
	DatabaseURL               string
	StripeAPIKey              string
	StripeAPIVersion          string
	StripeWebhookSecret       string
	StripeStarterPrice        string
	StripeTeamPrice           string
	StripeCompliancePrice     string
	StripeStorageEventName    string
	StripeOperationsEventName string
	StripeEgressEventName     string
	StripeStorageMeterID      string
	StripeOperationsMeterID   string
	StripeEgressMeterID       string
	CheckoutSuccessURL        string
	CheckoutCancelURL         string
	PortalReturnURL           string
	TermsVersion              string
	ProvisioningEnabled       bool
	ScalewayProjectID         string
	ScalewayRegion            string
	ScalewayAuthToken         string
	CellID                    string
	CellPublicEndpoint        string
	CellAdminEndpoint         string
	CellAdminSecretRef        string
	CellCapacity              int
	StarterQuotaBytes         int64
	TeamQuotaBytes            int64
	ComplianceQuotaBytes      int64
	EmailEnabled              bool
	EmailFrom                 string
	EmailFromName             string
	EmailVerificationURL      string
	CustomerOrigin            string
	MetricsToken              string
	UsageIngestToken          string
}

func Load() (Config, error) {
	cfg := Config{
		ListenAddr:                strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_LISTEN_ADDR")),
		DatabaseURL:               strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_DATABASE_URL")),
		StripeAPIKey:              strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_STRIPE_API_KEY")),
		StripeAPIVersion:          strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_STRIPE_API_VERSION")),
		StripeWebhookSecret:       strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_STRIPE_WEBHOOK_SECRET")),
		StripeStarterPrice:        strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_STRIPE_STARTER_PRICE")),
		StripeTeamPrice:           strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_STRIPE_TEAM_PRICE")),
		StripeCompliancePrice:     strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_STRIPE_COMPLIANCE_PRICE")),
		StripeStorageEventName:    strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_STRIPE_STORAGE_EVENT_NAME")),
		StripeOperationsEventName: strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_STRIPE_OPERATIONS_EVENT_NAME")),
		StripeEgressEventName:     strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_STRIPE_EGRESS_EVENT_NAME")),
		StripeStorageMeterID:      strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_STRIPE_STORAGE_METER_ID")),
		StripeOperationsMeterID:   strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_STRIPE_OPERATIONS_METER_ID")),
		StripeEgressMeterID:       strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_STRIPE_EGRESS_METER_ID")),
		CheckoutSuccessURL:        strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_CHECKOUT_SUCCESS_URL")),
		CheckoutCancelURL:         strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_CHECKOUT_CANCEL_URL")),
		PortalReturnURL:           strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_PORTAL_RETURN_URL")),
		TermsVersion:              strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_TERMS_VERSION")),
		ScalewayProjectID:         strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_SCALEWAY_PROJECT_ID")),
		ScalewayRegion:            strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_SCALEWAY_REGION")),
		ScalewayAuthToken:         strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_SCALEWAY_AUTH_TOKEN")),
		CellID:                    strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_CELL_ID")),
		CellPublicEndpoint:        strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_CELL_PUBLIC_ENDPOINT")),
		CellAdminEndpoint:         strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_CELL_ADMIN_ENDPOINT")),
		CellAdminSecretRef:        strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_CELL_ADMIN_SECRET_REF")),
		EmailFrom:                 strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_EMAIL_FROM")),
		EmailFromName:             strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_EMAIL_FROM_NAME")),
		EmailVerificationURL:      strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_EMAIL_VERIFICATION_URL")),
		CustomerOrigin:            strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_CUSTOMER_ORIGIN")),
		MetricsToken:              strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_METRICS_TOKEN")),
		UsageIngestToken:          strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_USAGE_INGEST_TOKEN")),
	}
	cfg.ProvisioningEnabled, _ = strconv.ParseBool(strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_PROVISIONING_ENABLED")))
	cfg.EmailEnabled, _ = strconv.ParseBool(strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_EMAIL_ENABLED")))
	cfg.CellCapacity, _ = strconv.Atoi(strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_CELL_CAPACITY")))
	cfg.StarterQuotaBytes, _ = strconv.ParseInt(strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_STARTER_QUOTA_BYTES")), 10, 64)
	cfg.TeamQuotaBytes, _ = strconv.ParseInt(strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_TEAM_QUOTA_BYTES")), 10, 64)
	cfg.ComplianceQuotaBytes, _ = strconv.ParseInt(strings.TrimSpace(os.Getenv("LOCKWELL_SAAS_COMPLIANCE_QUOTA_BYTES")), 10, 64)
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = "127.0.0.1:8080"
	}
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("LOCKWELL_SAAS_DATABASE_URL is required")
	}
	if len(cfg.MetricsToken) < 32 {
		return Config{}, errors.New("LOCKWELL_SAAS_METRICS_TOKEN must contain at least 32 characters")
	}
	if len(cfg.UsageIngestToken) < 32 {
		return Config{}, errors.New("LOCKWELL_SAAS_USAGE_INGEST_TOKEN must contain at least 32 characters")
	}
	if cfg.StripeWebhookSecret == "" {
		return Config{}, errors.New("LOCKWELL_SAAS_STRIPE_WEBHOOK_SECRET is required")
	}
	if cfg.StripeAPIKey == "" || cfg.StripeAPIVersion == "" || cfg.StripeStarterPrice == "" || cfg.StripeTeamPrice == "" || cfg.StripeCompliancePrice == "" {
		return Config{}, errors.New("Stripe API key and all allowlisted plan Price IDs are required")
	}
	if cfg.StripeStorageEventName == "" || cfg.StripeOperationsEventName == "" || cfg.StripeEgressEventName == "" {
		return Config{}, errors.New("all Stripe meter event names are required")
	}
	if cfg.StripeStorageMeterID == "" || cfg.StripeOperationsMeterID == "" || cfg.StripeEgressMeterID == "" {
		return Config{}, errors.New("all Stripe meter IDs are required")
	}
	if cfg.CheckoutSuccessURL == "" || cfg.CheckoutCancelURL == "" || cfg.PortalReturnURL == "" || cfg.TermsVersion == "" {
		return Config{}, errors.New("Checkout/Portal URLs and terms version are required")
	}
	if cfg.ProvisioningEnabled && (cfg.ScalewayProjectID == "" || cfg.ScalewayRegion == "" || cfg.ScalewayAuthToken == "" || cfg.CellID == "" || cfg.CellPublicEndpoint == "" || cfg.CellAdminEndpoint == "" || cfg.CellAdminSecretRef == "" || cfg.CellCapacity <= 0 || cfg.StarterQuotaBytes <= 0 || cfg.TeamQuotaBytes <= 0 || cfg.ComplianceQuotaBytes <= 0) {
		return Config{}, errors.New("enabled provisioning requires complete Scaleway, cell, capacity, and positive plan-quota configuration")
	}
	if cfg.EmailEnabled && (cfg.ScalewayProjectID == "" || cfg.ScalewayAuthToken == "" || cfg.EmailFrom == "" || cfg.EmailVerificationURL == "") {
		return Config{}, errors.New("enabled email verification requires Scaleway project/auth, sender, and verification URL")
	}
	if cfg.CustomerOrigin != "" {
		origin, err := url.Parse(cfg.CustomerOrigin)
		if err != nil || (origin.Scheme != "https" && origin.Scheme != "http") || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || (origin.Path != "" && origin.Path != "/") {
			return Config{}, errors.New("LOCKWELL_SAAS_CUSTOMER_ORIGIN must be an HTTP(S) origin without credentials, path, query, or fragment")
		}
		cfg.CustomerOrigin = strings.TrimSuffix(cfg.CustomerOrigin, "/")
	}
	return cfg, nil
}
