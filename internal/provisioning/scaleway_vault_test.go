package provisioning

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestScalewayVaultCreatesVersionAndAccessesLatestEnabled(t *testing.T) {
	var calls []string
	var stored string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Auth-Token") != "test-token" {
			t.Error("missing auth token")
		}
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/secrets"):
			if len(calls) == 1 {
				_ = json.NewEncoder(w).Encode(map[string]any{"secrets": []any{}, "total_count": 0})
			} else {
				_ = json.NewEncoder(w).Encode(map[string]any{"secrets": []any{map[string]any{"id": "secret-1", "name": secretName("tenant/p1"), "status": "ready"}}, "total_count": 1})
			}
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/secrets"):
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "secret-1", "name": secretName("tenant/p1"), "status": "ready"})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/versions"):
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			stored = body["data"]
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/versions/latest_enabled/access"):
			_ = json.NewEncoder(w).Encode(map[string]string{"data": stored})
		default:
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
	defer server.Close()
	vault := ScalewayVault{ProjectID: "project-1", Region: "fr-par", AuthToken: "test-token", BaseURL: server.URL, HTTPClient: server.Client()}
	ref, err := vault.Put(context.Background(), "tenant/p1", []byte("super-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if ref != "scaleway://fr-par/secret-1" {
		t.Fatalf("ref=%q", ref)
	}
	if stored != base64.StdEncoding.EncodeToString([]byte("super-secret")) {
		t.Fatal("secret was not base64 encoded")
	}
	value, err := vault.Get(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if string(value) != "super-secret" {
		t.Fatalf("value=%q", value)
	}
}

func TestScalewayVaultRejectsForeignReference(t *testing.T) {
	v := ScalewayVault{ProjectID: "p", Region: "fr-par", AuthToken: "t"}
	if _, err := v.Get(context.Background(), "scaleway://nl-ams/id"); err == nil {
		t.Fatal("expected region mismatch denial")
	}
}
