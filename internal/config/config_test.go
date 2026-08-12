package config

import "testing"

func setRequiredBase(t *testing.T) {
	t.Helper()
	values := map[string]string{"LOCKWELL_SAAS_DATABASE_URL": "postgres://test", "LOCKWELL_SAAS_STRIPE_WEBHOOK_SECRET": "whsec_test", "LOCKWELL_SAAS_STRIPE_API_KEY": "sk_test", "LOCKWELL_SAAS_STRIPE_API_VERSION": "2026-06-30", "LOCKWELL_SAAS_STRIPE_STARTER_PRICE": "price_1", "LOCKWELL_SAAS_STRIPE_TEAM_PRICE": "price_2", "LOCKWELL_SAAS_STRIPE_COMPLIANCE_PRICE": "price_3", "LOCKWELL_SAAS_STRIPE_STORAGE_EVENT_NAME": "storage", "LOCKWELL_SAAS_STRIPE_OPERATIONS_EVENT_NAME": "operations", "LOCKWELL_SAAS_STRIPE_EGRESS_EVENT_NAME": "egress", "LOCKWELL_SAAS_CHECKOUT_SUCCESS_URL": "https://example.test/success", "LOCKWELL_SAAS_CHECKOUT_CANCEL_URL": "https://example.test/cancel", "LOCKWELL_SAAS_PORTAL_RETURN_URL": "https://example.test/portal", "LOCKWELL_SAAS_TERMS_VERSION": "v1"}
	for key, value := range values {
		t.Setenv(key, value)
	}
}
func TestLoadRejectsPartiallyConfiguredProvisioning(t *testing.T) {
	setRequiredBase(t)
	t.Setenv("LOCKWELL_SAAS_PROVISIONING_ENABLED", "true")
	t.Setenv("LOCKWELL_SAAS_SCALEWAY_PROJECT_ID", "project")
	if _, err := Load(); err == nil {
		t.Fatal("expected incomplete provisioning configuration denial")
	}
}
func TestLoadAcceptsCompleteProvisioningConfiguration(t *testing.T) {
	setRequiredBase(t)
	values := map[string]string{"LOCKWELL_SAAS_PROVISIONING_ENABLED": "true", "LOCKWELL_SAAS_SCALEWAY_PROJECT_ID": "project", "LOCKWELL_SAAS_SCALEWAY_REGION": "fr-par", "LOCKWELL_SAAS_SCALEWAY_AUTH_TOKEN": "token", "LOCKWELL_SAAS_CELL_ID": "cell-1", "LOCKWELL_SAAS_CELL_PUBLIC_ENDPOINT": "https://s3.example.test", "LOCKWELL_SAAS_CELL_ADMIN_ENDPOINT": "https://admin.example.test", "LOCKWELL_SAAS_CELL_ADMIN_SECRET_REF": "scaleway://fr-par/secret-id", "LOCKWELL_SAAS_CELL_CAPACITY": "100", "LOCKWELL_SAAS_STARTER_QUOTA_BYTES": "1073741824", "LOCKWELL_SAAS_TEAM_QUOTA_BYTES": "2147483648", "LOCKWELL_SAAS_COMPLIANCE_QUOTA_BYTES": "4294967296"}
	for key, value := range values {
		t.Setenv(key, value)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ProvisioningEnabled || cfg.CellCapacity != 100 {
		t.Fatalf("cfg=%#v", cfg)
	}
}
