package accounts

import (
	"context"
	"errors"
	"testing"
	"time"
)

type memoryRepo struct {
	account      Account
	passwordHash string
	tokenHash    [32]byte
	verifyHash   [32]byte
}

func (r *memoryRepo) CreateAccount(_ context.Context, email, passwordHash, _ string, _ time.Time) (Account, error) {
	if r.account.ID != "" {
		return Account{}, ErrEmailExists
	}
	r.account = Account{ID: "acct_1", Email: email}
	r.passwordHash = passwordHash
	return r.account, nil
}
func (r *memoryRepo) FindAccountByEmail(_ context.Context, email string) (Account, string, error) {
	if r.account.Email != email {
		return Account{}, "", ErrInvalidCredentials
	}
	return r.account, r.passwordHash, nil
}
func (r *memoryRepo) CreateSession(_ context.Context, accountID string, tokenHash [32]byte, _ time.Time) error {
	if accountID != r.account.ID {
		return errors.New("wrong account")
	}
	r.tokenHash = tokenHash
	return nil
}
func (r *memoryRepo) AccountBySession(_ context.Context, tokenHash [32]byte, _ time.Time) (Account, error) {
	if tokenHash != r.tokenHash {
		return Account{}, ErrInvalidCredentials
	}
	return r.account, nil
}
func (r *memoryRepo) BindStripeCustomer(_ context.Context, _, customerID string) error {
	r.account.StripeCustomerID = customerID
	return nil
}
func (r *memoryRepo) CreateEmailVerification(_ context.Context, _ string, hash [32]byte, _, _ time.Time) error {
	r.verifyHash = hash
	return nil
}
func (r *memoryRepo) ConsumeEmailVerification(_ context.Context, hash [32]byte, _ time.Time) (Account, error) {
	if hash != r.verifyHash {
		return Account{}, ErrInvalidCredentials
	}
	r.account.EmailVerified = true
	return r.account, nil
}

type mailerStub struct{ token string }

func (m *mailerStub) SendVerification(_ context.Context, _, token string) error {
	m.token = token
	return nil
}

func TestServiceSignupLoginAndAuthenticate(t *testing.T) {
	repo := &memoryRepo{}
	service := Service{Repo: repo, TermsVersion: "2026-08-12", Now: func() time.Time { return time.Unix(1_700_000_000, 0) }}
	account, signupToken, err := service.Signup(context.Background(), "USER@example.com", "correct horse battery staple", "2026-08-12")
	if err != nil || account.Email != "user@example.com" || signupToken == "" {
		t.Fatalf("signup account = %#v, token empty = %v, err = %v", account, signupToken == "", err)
	}
	if _, _, err := service.Login(context.Background(), account.Email, "wrong password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong-password error = %v", err)
	}
	_, loginToken, err := service.Login(context.Background(), account.Email, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	authenticated, err := service.Authenticate(context.Background(), loginToken)
	if err != nil || authenticated.ID != account.ID {
		t.Fatalf("authenticated = %#v, err = %v", authenticated, err)
	}
}

func TestEmailVerificationIsHashedAndSinglePurpose(t *testing.T) {
	repo := &memoryRepo{account: Account{ID: "acct_1", Email: "user@example.test"}}
	mailer := &mailerStub{}
	service := Service{Repo: repo, Mailer: mailer, Now: func() time.Time { return time.Unix(1_700_000_000, 0) }}
	if err := service.RequestEmailVerification(context.Background(), repo.account); err != nil {
		t.Fatal(err)
	}
	if mailer.token == "" || repo.verifyHash == [32]byte{} {
		t.Fatal("missing delivered token or stored hash")
	}
	account, err := service.VerifyEmail(context.Background(), mailer.token)
	if err != nil || !account.EmailVerified {
		t.Fatalf("account=%#v err=%v", account, err)
	}
}

func TestServiceRequiresExactCurrentTerms(t *testing.T) {
	service := Service{Repo: &memoryRepo{}, TermsVersion: "current"}
	if _, _, err := service.Signup(context.Background(), "user@example.com", "correct horse battery staple", "old"); err == nil {
		t.Fatal("stale terms accepted")
	}
}
