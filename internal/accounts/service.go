package accounts

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"
)

const SessionTTL = 24 * time.Hour

type Service struct {
	Repo         Repository
	TermsVersion string
	Now          func() time.Time
	Mailer       VerificationMailer
}

func (s Service) Signup(ctx context.Context, email, password, acceptedTerms string) (Account, string, error) {
	normalized, err := NormalizeEmail(email)
	if err != nil {
		return Account{}, "", err
	}
	if s.TermsVersion == "" || acceptedTerms != s.TermsVersion {
		return Account{}, "", errors.New("current terms must be accepted")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return Account{}, "", err
	}
	now := s.now()
	account, err := s.Repo.CreateAccount(ctx, normalized, hash, s.TermsVersion, now)
	if err != nil {
		return Account{}, "", err
	}
	token, tokenHash, err := NewSessionToken()
	if err != nil {
		return Account{}, "", err
	}
	if err := s.Repo.CreateSession(ctx, account.ID, tokenHash, now.Add(SessionTTL)); err != nil {
		return Account{}, "", err
	}
	return account, token, nil
}

func (s Service) Login(ctx context.Context, email, password string) (Account, string, error) {
	normalized, err := NormalizeEmail(email)
	if err != nil {
		return Account{}, "", ErrInvalidCredentials
	}
	account, passwordHash, err := s.Repo.FindAccountByEmail(ctx, normalized)
	if err != nil {
		VerifyPassword("$argon2id$v=19$m=65536,t=3,p=2$AAAAAAAAAAAAAAAAAAAAAA$RB3fE7TUXJ2tLzm3WqDpoI8M5YeYhx2MmN2AzHv4th4", password)
		return Account{}, "", ErrInvalidCredentials
	}
	if !VerifyPassword(passwordHash, password) {
		return Account{}, "", ErrInvalidCredentials
	}
	token, tokenHash, err := NewSessionToken()
	if err != nil {
		return Account{}, "", err
	}
	if err := s.Repo.CreateSession(ctx, account.ID, tokenHash, s.now().Add(SessionTTL)); err != nil {
		return Account{}, "", err
	}
	return account, token, nil
}

func (s Service) Authenticate(ctx context.Context, token string) (Account, error) {
	if token == "" {
		return Account{}, ErrInvalidCredentials
	}
	return s.Repo.AccountBySession(ctx, sha256.Sum256([]byte(token)), s.now())
}

func (s Service) RequestEmailVerification(ctx context.Context, account Account) error {
	if account.ID == "" || account.Email == "" {
		return ErrInvalidCredentials
	}
	if account.EmailVerified {
		return nil
	}
	if s.Mailer == nil {
		return errors.New("email verification delivery is not configured")
	}
	token, hash, err := NewSessionToken()
	if err != nil {
		return err
	}
	now := s.now()
	if err = s.Repo.CreateEmailVerification(ctx, account.ID, hash, now.Add(time.Hour), now); err != nil {
		return err
	}
	return s.Mailer.SendVerification(ctx, account.Email, token)
}
func (s Service) VerifyEmail(ctx context.Context, token string) (Account, error) {
	if token == "" {
		return Account{}, ErrInvalidCredentials
	}
	return s.Repo.ConsumeEmailVerification(ctx, sha256.Sum256([]byte(token)), s.now())
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
