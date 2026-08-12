package billing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"
)

func TestVerifyStripeSignature(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	payload := []byte(`{"id":"evt_1"}`)
	secret := "whsec_test_only"
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.", now.Unix())
	_, _ = mac.Write(payload)
	header := fmt.Sprintf("t=%d,v1=%s", now.Unix(), hex.EncodeToString(mac.Sum(nil)))

	if err := VerifyStripeSignature(header, payload, secret, now, 5*time.Minute); err != nil {
		t.Fatalf("verify valid signature: %v", err)
	}
	if err := VerifyStripeSignature(header, append(payload, 'x'), secret, now, 5*time.Minute); err != ErrInvalidSignature {
		t.Fatalf("tampered payload error = %v", err)
	}
	if err := VerifyStripeSignature(header, payload, secret, now.Add(6*time.Minute), 5*time.Minute); err != ErrExpiredSignature {
		t.Fatalf("expired signature error = %v", err)
	}
}
