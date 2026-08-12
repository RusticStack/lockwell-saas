package billing

import (
	"context"
	"errors"

	"github.com/RusticStack/lockwell-saas/internal/accounts"
)

var ErrUnsupportedPlan = errors.New("unsupported plan")
var ErrEmailUnverified = errors.New("email is not verified")

type AccountRepository interface {
	accounts.Repository
	RecordCheckoutSession(context.Context, string, string, CheckoutSession, string) error
}

type Service struct {
	Accounts   accounts.Service
	Repo       AccountRepository
	Stripe     StripeClient
	PriceIDs   map[string]string
	SuccessURL string
	CancelURL  string
	PortalURL  string
}

func (s Service) Checkout(ctx context.Context, token, planCode, requestID string) (CheckoutSession, error) {
	account, err := s.Accounts.Authenticate(ctx, token)
	if err != nil {
		return CheckoutSession{}, err
	}
	if !account.EmailVerified {
		return CheckoutSession{}, ErrEmailUnverified
	}
	priceID, ok := s.PriceIDs[planCode]
	if !ok || priceID == "" {
		return CheckoutSession{}, ErrUnsupportedPlan
	}
	if requestID == "" {
		return CheckoutSession{}, errors.New("request ID is required")
	}
	customerID := account.StripeCustomerID
	if customerID == "" {
		customerID, err = s.Stripe.CreateCustomer(ctx, account.Email, account.ID, "customer:"+account.ID)
		if err != nil {
			return CheckoutSession{}, err
		}
		if err := s.Repo.BindStripeCustomer(ctx, account.ID, customerID); err != nil {
			return CheckoutSession{}, err
		}
	}
	idempotencyKey := "checkout:" + account.ID + ":" + planCode + ":" + requestID
	session, err := s.Stripe.CreateCheckoutSession(ctx, CheckoutRequest{CustomerID: customerID, PriceID: priceID, AccountID: account.ID, PlanCode: planCode, SuccessURL: s.SuccessURL, CancelURL: s.CancelURL, IdempotencyKey: idempotencyKey})
	if err != nil {
		return CheckoutSession{}, err
	}
	if err := s.Repo.RecordCheckoutSession(ctx, account.ID, planCode, session, idempotencyKey); err != nil {
		return CheckoutSession{}, err
	}
	return session, nil
}

func (s Service) Portal(ctx context.Context, token, requestID string) (PortalSession, error) {
	account, err := s.Accounts.Authenticate(ctx, token)
	if err != nil {
		return PortalSession{}, err
	}
	if !account.EmailVerified {
		return PortalSession{}, ErrEmailUnverified
	}
	if account.StripeCustomerID == "" {
		return PortalSession{}, errors.New("account has no Stripe customer")
	}
	if requestID == "" {
		return PortalSession{}, errors.New("request ID is required")
	}
	return s.Stripe.CreatePortalSession(ctx, account.StripeCustomerID, s.PortalURL, "portal:"+account.ID+":"+requestID)
}
