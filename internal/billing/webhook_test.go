package billing

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type recordingStore struct {
	calls   int
	event   StripeEvent
	payload []byte
	err     error
}

func (s *recordingStore) RecordStripeEvent(_ context.Context, event StripeEvent, payload []byte, _ [32]byte, _ string) (bool, error) {
	s.calls++
	s.event = event
	s.payload = append([]byte(nil), payload...)
	return true, s.err
}

func TestWebhookHandlerVerifiesBeforeRecordingRawPayload(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	secret := "whsec_test_only"
	payload := []byte(`{"id":"evt_1","type":"invoice.paid","api_version":"2026-06-30","created":1700000000}`)
	recorder := &recordingStore{}
	handler := WebhookHandler{Secret: secret, Now: func() time.Time { return now }, Recorder: recorder}

	request := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", strings.NewReader(string(payload)))
	request.Header.Set("Stripe-Signature", stripeTestSignature(secret, payload, now))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	if recorder.calls != 1 || recorder.event.ID != "evt_1" || string(recorder.payload) != string(payload) {
		t.Fatalf("recorded event = %#v, calls = %d, payload = %q", recorder.event, recorder.calls, recorder.payload)
	}
}

func TestWebhookHandlerRejectsTamperingBeforeRecording(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	secret := "whsec_test_only"
	signed := []byte(`{"id":"evt_1","type":"invoice.paid","created":1700000000}`)
	recorder := &recordingStore{}
	handler := WebhookHandler{Secret: secret, Now: func() time.Time { return now }, Recorder: recorder}

	request := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", strings.NewReader(`{"id":"evt_2","type":"invoice.paid","created":1700000000}`))
	request.Header.Set("Stripe-Signature", stripeTestSignature(secret, signed, now))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest || recorder.calls != 0 {
		t.Fatalf("status = %d, recorder calls = %d", response.Code, recorder.calls)
	}
}

func TestWebhookHandlerMapsConflictingReplayToConflict(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	secret := "whsec_test_only"
	payload := []byte(`{"id":"evt_1","type":"invoice.paid","created":1700000000}`)
	recorder := &recordingStore{err: ErrConflictingReplay}
	handler := WebhookHandler{Secret: secret, Now: func() time.Time { return now }, Recorder: recorder}

	request := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", strings.NewReader(string(payload)))
	request.Header.Set("Stripe-Signature", stripeTestSignature(secret, payload, now))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusConflict || recorder.calls != 1 {
		t.Fatalf("status = %d, recorder calls = %d", response.Code, recorder.calls)
	}
}

func stripeTestSignature(secret string, payload []byte, at time.Time) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.", at.Unix())
	_, _ = mac.Write(payload)
	return fmt.Sprintf("t=%d,v1=%s", at.Unix(), hex.EncodeToString(mac.Sum(nil)))
}
