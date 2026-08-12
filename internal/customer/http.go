package customer

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/RusticStack/lockwell-saas/internal/accounts"
)

type Authenticator interface {
	Authenticate(context.Context, string) (accounts.Account, error)
}

type HTTPHandler struct {
	Accounts Authenticator
	Repo     Repository
}

func (h HTTPHandler) Status(w http.ResponseWriter, r *http.Request) {
	account, err := h.Accounts.Authenticate(r.Context(), accounts.BearerToken(r))
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	status, err := h.Repo.CustomerStatus(r.Context(), account.ID)
	if err != nil {
		http.Error(w, "status unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(struct {
		AccountID     string `json:"account_id"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Status
	}{account.ID, account.Email, account.EmailVerified, status})
}
