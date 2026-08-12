package billing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RusticStack/lockwell-saas/internal/accounts"
)

type billingRepo struct {
	account  accounts.Account
	session  [32]byte
	recorded CheckoutSession
}

func (r *billingRepo) CreateAccount(context.Context, string, string, string, time.Time) (accounts.Account, error) {
	return accounts.Account{}, errors.New("unused")
}
func (r *billingRepo) FindAccountByEmail(context.Context, string) (accounts.Account, string, error) {
	return accounts.Account{}, "", errors.New("unused")
}
func (r *billingRepo) CreateSession(context.Context, string, [32]byte, time.Time) error { return nil }
func (r *billingRepo) AccountBySession(_ context.Context, hash [32]byte, _ time.Time) (accounts.Account, error) {
	if hash != r.session {
		return accounts.Account{}, accounts.ErrInvalidCredentials
	}
	return r.account, nil
}
func (r *billingRepo) BindStripeCustomer(_ context.Context, _, customerID string) error {
	r.account.StripeCustomerID = customerID
	return nil
}
func (*billingRepo) CreateEmailVerification(context.Context, string, [32]byte, time.Time, time.Time) error {
	return errors.New("unused")
}
func (*billingRepo) ConsumeEmailVerification(context.Context, [32]byte, time.Time) (accounts.Account, error) {
	return accounts.Account{}, errors.New("unused")
}
func (r *billingRepo) RecordCheckoutSession(_ context.Context, _, _ string, session CheckoutSession, _ string) error {
	r.recorded = session
	return nil
}

func TestCheckoutRejectsBrowserSuppliedUnknownPlanBeforeStripe(t *testing.T) {
	token, hash, err := accounts.NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	repo := &billingRepo{account: accounts.Account{ID: "acct_1", Email: "user@example.com", EmailVerified: true, StripeCustomerID: "cus_1"}, session: hash}
	service := Service{Accounts: accounts.Service{Repo: repo}, Repo: repo, PriceIDs: map[string]string{"starter": "price_server_allowlisted"}}
	if _, err := service.Checkout(context.Background(), token, "attacker-price-id", "req_1"); !errors.Is(err, ErrUnsupportedPlan) {
		t.Fatalf("unknown plan error = %v", err)
	}
}

func TestCheckoutRejectsUnverifiedEmail(t *testing.T) {
	token, hash, err := accounts.NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	repo := &billingRepo{account: accounts.Account{ID: "acct_1", Email: "user@example.com"}, session: hash}
	service := Service{Accounts: accounts.Service{Repo: repo}, Repo: repo, PriceIDs: map[string]string{"starter": "price_1"}}
	if _, err := service.Checkout(context.Background(), token, "starter", "req_1"); !errors.Is(err, ErrEmailUnverified) {
		t.Fatalf("unverified email error = %v", err)
	}
}
