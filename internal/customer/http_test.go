package customer

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RusticStack/lockwell-saas/internal/accounts"
)

type authStub struct{ err error }

func (a authStub) Authenticate(context.Context, string) (accounts.Account, error) {
	return accounts.Account{ID: "account-1", Email: "customer@example.test", EmailVerified: true}, a.err
}

type repoStub struct {
	status Status
	err    error
}

func (r repoStub) CustomerStatus(context.Context, string) (Status, error) { return r.status, r.err }

func TestStatusRequiresAuthentication(t *testing.T) {
	h := HTTPHandler{Accounts: authStub{err: accounts.ErrInvalidCredentials}, Repo: repoStub{}}
	response := httptest.NewRecorder()
	h.Status(response, httptest.NewRequest(http.MethodGet, "/v1/customer/status", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestStatusReturnsNoStoreProjection(t *testing.T) {
	h := HTTPHandler{Accounts: authStub{}, Repo: repoStub{status: Status{Usage: []Usage{}, Invoices: []Invoice{}, Refunds: []Refund{}}}}
	request := httptest.NewRequest(http.MethodGet, "/v1/customer/status", nil)
	request.Header.Set("Authorization", "Bearer session")
	response := httptest.NewRecorder()
	h.Status(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), `"email_verified":true`) {
		t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
}

func TestStatusHidesRepositoryErrors(t *testing.T) {
	h := HTTPHandler{Accounts: authStub{}, Repo: repoStub{err: errors.New("database details")}}
	request := httptest.NewRequest(http.MethodGet, "/v1/customer/status", nil)
	request.Header.Set("Authorization", "Bearer session")
	response := httptest.NewRecorder()
	h.Status(response, request)
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "database") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
