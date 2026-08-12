package accounts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/mail"
	"strings"
	"time"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrEmailExists        = errors.New("email already registered")
)

type Account struct {
	ID               string
	Email            string
	EmailVerified    bool
	StripeCustomerID string
}

type Repository interface {
	CreateAccount(context.Context, string, string, string, time.Time) (Account, error)
	FindAccountByEmail(context.Context, string) (Account, string, error)
	CreateSession(context.Context, string, [32]byte, time.Time) error
	AccountBySession(context.Context, [32]byte, time.Time) (Account, error)
	BindStripeCustomer(context.Context, string, string) error
	CreateEmailVerification(context.Context, string, [32]byte, time.Time, time.Time) error
	ConsumeEmailVerification(context.Context, [32]byte, time.Time) (Account, error)
}

type VerificationMailer interface {
	SendVerification(context.Context, string, string) error
}

func NormalizeEmail(value string) (string, error) {
	value = strings.TrimSpace(value)
	parsed, err := mail.ParseAddress(value)
	if err != nil || !strings.EqualFold(parsed.Address, value) || len(value) > 254 {
		return "", errors.New("invalid email address")
	}
	return strings.ToLower(parsed.Address), nil
}

func NewSessionToken() (string, [32]byte, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", [32]byte{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	return token, sha256.Sum256([]byte(token)), nil
}
