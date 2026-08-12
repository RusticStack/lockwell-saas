package accounts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestScalewayMailerSendsVerificationWithoutLoggingOrPersistingToken(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Auth-Token") != "auth" {
			t.Error("missing auth")
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	mailer := ScalewayMailer{ProjectID: "project", Region: "fr-par", AuthToken: "auth", FromEmail: "accounts@example.test", FromName: "Lockwell", VerifyURL: "https://app.example.test/verify", BaseURL: server.URL, HTTPClient: server.Client()}
	if err := mailer.SendVerification(context.Background(), "user@example.test", "raw-token"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body["text"].(string), "#token=raw-token") || strings.Contains(body["text"].(string), "?token=") {
		t.Fatalf("body=%#v", body)
	}
}
func TestScalewayMailerRejectsNonHTTPSVerificationURL(t *testing.T) {
	m := ScalewayMailer{ProjectID: "p", Region: "fr-par", AuthToken: "a", FromEmail: "a@b.test", VerifyURL: "http://example.test"}
	if err := m.SendVerification(context.Background(), "u@example.test", "token"); err == nil {
		t.Fatal("expected insecure URL denial")
	}
}
