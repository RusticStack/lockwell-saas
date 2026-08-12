package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCustomerCORSAllowsExactOriginPreflight(t *testing.T) {
	handler := CustomerCORS("https://app.example.test", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("preflight reached application handler")
	}))
	request := httptest.NewRequest(http.MethodOptions, "/v1/accounts/login", nil)
	request.Header.Set("Origin", "https://app.example.test")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request.Header.Set("Access-Control-Request-Headers", "content-type, authorization, idempotency-key")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Origin") != "https://app.example.test" || response.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Fatalf("status=%d headers=%v", response.Code, response.Header())
	}
}

func TestCustomerCORSAllowsAuthenticatedStatusGET(t *testing.T) {
	handler := CustomerCORS("https://app.example.test", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("preflight reached application handler")
	}))
	request := httptest.NewRequest(http.MethodOptions, "/v1/customer/status", nil)
	request.Header.Set("Origin", "https://app.example.test")
	request.Header.Set("Access-Control-Request-Method", http.MethodGet)
	request.Header.Set("Access-Control-Request-Headers", "authorization")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Methods") != "GET, POST, OPTIONS" {
		t.Fatalf("status=%d headers=%v", response.Code, response.Header())
	}
}

func TestCustomerCORSRejectsUntrustedOriginAndHeaders(t *testing.T) {
	for name, values := range map[string][2]string{
		"origin":  {"https://evil.example", "content-type"},
		"headers": {"https://app.example.test", "content-type, x-admin-token"},
	} {
		t.Run(name, func(t *testing.T) {
			handler := CustomerCORS("https://app.example.test", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("denied request reached handler") }))
			request := httptest.NewRequest(http.MethodOptions, "/v1/accounts/login", nil)
			request.Header.Set("Origin", values[0])
			request.Header.Set("Access-Control-Request-Method", http.MethodPost)
			request.Header.Set("Access-Control-Request-Headers", values[1])
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("status=%d", response.Code)
			}
		})
	}
}

func TestCustomerCORSDoesNotExposeOperationalRoutes(t *testing.T) {
	handler := CustomerCORS("https://app.example.test", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.Header.Set("Origin", "https://app.example.test")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("status=%d headers=%v", response.Code, response.Header())
	}
}
