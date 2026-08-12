package billing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalidSignature = errors.New("invalid Stripe webhook signature")
	ErrExpiredSignature = errors.New("expired Stripe webhook signature")
)

func VerifyStripeSignature(header string, payload []byte, secret string, now time.Time, tolerance time.Duration) error {
	var timestamp int64
	var signatures [][]byte
	for _, field := range strings.Split(header, ",") {
		name, value, ok := strings.Cut(strings.TrimSpace(field), "=")
		if !ok {
			continue
		}
		switch name {
		case "t":
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return ErrInvalidSignature
			}
			timestamp = parsed
		case "v1":
			decoded, err := hex.DecodeString(value)
			if err == nil {
				signatures = append(signatures, decoded)
			}
		}
	}
	if timestamp == 0 || len(signatures) == 0 || secret == "" {
		return ErrInvalidSignature
	}
	signedAt := time.Unix(timestamp, 0)
	if delta := now.Sub(signedAt); delta > tolerance || delta < -tolerance {
		return ErrExpiredSignature
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.", timestamp)
	_, _ = mac.Write(payload)
	expected := mac.Sum(nil)
	for _, signature := range signatures {
		if hmac.Equal(expected, signature) {
			return nil
		}
	}
	return ErrInvalidSignature
}
