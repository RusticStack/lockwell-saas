package httpapi

import (
	"net/http"
	"strings"
)

const (
	allowMethods = "GET, POST, OPTIONS"
	allowHeaders = "Authorization, Content-Type, Idempotency-Key"
)

// CustomerCORS permits the configured browser origin to call customer-facing
// versioned routes. It intentionally does not enable credentials or expose
// webhook, health, or readiness endpoints cross-origin.
func CustomerCORS(origin string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestOrigin := r.Header.Get("Origin")
		customerRoute := strings.HasPrefix(r.URL.Path, "/v1/")
		if origin == "" || requestOrigin == "" || !customerRoute {
			next.ServeHTTP(w, r)
			return
		}
		if requestOrigin != origin {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}

		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		if r.Method == http.MethodOptions {
			if !allowedRequestMethod(r.Header.Get("Access-Control-Request-Method")) || !allowedRequestHeaders(r.Header.Get("Access-Control-Request-Headers")) {
				http.Error(w, "preflight not allowed", http.StatusForbidden)
				return
			}
			w.Header().Set("Access-Control-Allow-Methods", allowMethods)
			w.Header().Set("Access-Control-Allow-Headers", allowHeaders)
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func allowedRequestMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodPost
}

func allowedRequestHeaders(value string) bool {
	for _, header := range strings.Split(value, ",") {
		switch strings.ToLower(strings.TrimSpace(header)) {
		case "", "authorization", "content-type", "idempotency-key":
		default:
			return false
		}
	}
	return true
}
