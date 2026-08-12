package billing

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

const maxWebhookBody = 1 << 20

type StripeEvent struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	APIVersion string `json:"api_version"`
	Created    int64  `json:"created"`
}

type EventRecorder interface {
	RecordStripeEvent(context.Context, StripeEvent, []byte, [32]byte, string) (bool, error)
}

type WebhookHandler struct {
	Secret    string
	Tolerance time.Duration
	Now       func() time.Time
	Recorder  EventRecorder
}

func (h WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	now := time.Now().UTC()
	if h.Now != nil {
		now = h.Now().UTC()
	}
	tolerance := h.Tolerance
	if tolerance == 0 {
		tolerance = 5 * time.Minute
	}
	if err := VerifyStripeSignature(r.Header.Get("Stripe-Signature"), body, h.Secret, now, tolerance); err != nil {
		http.Error(w, "invalid signature", http.StatusBadRequest)
		return
	}
	var event StripeEvent
	if err := json.Unmarshal(body, &event); err != nil || event.ID == "" || event.Type == "" || event.Created <= 0 {
		http.Error(w, "invalid event", http.StatusBadRequest)
		return
	}
	if h.Recorder == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}
	digest := sha256.Sum256(body)
	outboxID, err := randomID()
	if err != nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}
	_, err = h.Recorder.RecordStripeEvent(r.Context(), event, body, digest, outboxID)
	if err != nil {
		if errors.Is(err, ErrConflictingReplay) {
			http.Error(w, "conflicting event replay", http.StatusConflict)
			return
		}
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func randomID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	h := hex.EncodeToString(value[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32], nil
}
