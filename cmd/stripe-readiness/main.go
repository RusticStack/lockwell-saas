package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/RusticStack/lockwell-saas/internal/billing"
)

func main() {
	cfg := billing.ReadinessConfig{
		APIKey: os.Getenv("LOCKWELL_SAAS_STRIPE_API_KEY"), APIVersion: os.Getenv("LOCKWELL_SAAS_STRIPE_API_VERSION"),
		PriceIDs:        map[string]string{"starter": os.Getenv("LOCKWELL_SAAS_STRIPE_STARTER_PRICE"), "team": os.Getenv("LOCKWELL_SAAS_STRIPE_TEAM_PRICE"), "compliance": os.Getenv("LOCKWELL_SAAS_STRIPE_COMPLIANCE_PRICE")},
		MeterIDs:        map[string]string{"storage": os.Getenv("LOCKWELL_SAAS_STRIPE_STORAGE_METER_ID"), "operations": os.Getenv("LOCKWELL_SAAS_STRIPE_OPERATIONS_METER_ID"), "egress": os.Getenv("LOCKWELL_SAAS_STRIPE_EGRESS_METER_ID")},
		MeterEventNames: map[string]string{"storage": os.Getenv("LOCKWELL_SAAS_STRIPE_STORAGE_EVENT_NAME"), "operations": os.Getenv("LOCKWELL_SAAS_STRIPE_OPERATIONS_EVENT_NAME"), "egress": os.Getenv("LOCKWELL_SAAS_STRIPE_EGRESS_EVENT_NAME")},
		PortalConfigID:  os.Getenv("LOCKWELL_SAAS_STRIPE_PORTAL_CONFIG_ID"), WebhookID: os.Getenv("LOCKWELL_SAAS_STRIPE_WEBHOOK_ENDPOINT_ID"), WebhookURL: os.Getenv("LOCKWELL_SAAS_STRIPE_WEBHOOK_URL"),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	report, err := billing.VerifyStripeReadiness(ctx, cfg, &http.Client{Timeout: 10 * time.Second})
	if err != nil {
		fmt.Fprintln(os.Stderr, "stripe readiness failed:", err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
		fmt.Fprintln(os.Stderr, "encode readiness report:", err)
		os.Exit(1)
	}
}
