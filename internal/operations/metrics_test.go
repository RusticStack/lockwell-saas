package operations

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type repoStub struct {
	snapshot Snapshot
	err      error
}

func (r repoStub) OperationalSnapshot(context.Context) (Snapshot, error) { return r.snapshot, r.err }

func TestMetricsRequiresExactBearerToken(t *testing.T) {
	h := MetricsHandler{Repo: repoStub{}, Token: "01234567890123456789012345678901"}
	for _, header := range []string{"", "Bearer wrong", "Basic 01234567890123456789012345678901"} {
		request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		request.Header.Set("Authorization", header)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("header=%q status=%d", header, response.Code)
		}
	}
}

func TestMetricsReturnsBoundedAggregateGauges(t *testing.T) {
	h := MetricsHandler{Repo: repoStub{snapshot: Snapshot{Accounts: 3, DeadLetterOutboxJobs: 2}}, Token: "01234567890123456789012345678901"}
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	request.Header.Set("Authorization", "Bearer 01234567890123456789012345678901")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	body := response.Body.String()
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(body, "lockwell_saas_accounts 3") || !strings.Contains(body, "lockwell_saas_outbox_dead_letter 2") {
		t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), body)
	}
	if strings.Contains(body, "{") || strings.Contains(body, "account_id") || strings.Contains(body, "tenant_id") {
		t.Fatalf("unexpected high-cardinality identity in metrics: %s", body)
	}
}

func TestMetricsHidesRepositoryFailure(t *testing.T) {
	h := MetricsHandler{Repo: repoStub{err: errors.New("database details")}, Token: "01234567890123456789012345678901"}
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	request.Header.Set("Authorization", "Bearer 01234567890123456789012345678901")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "database") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
