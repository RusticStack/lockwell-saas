package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/RusticStack/lockwell-saas/internal/accounts"
)

type AccountAuthenticator interface {
	Authenticate(context.Context, string) (accounts.Account, error)
}
type HTTPHandler struct {
	Accounts AccountAuthenticator
	Service  Service
}

func (h HTTPHandler) RequestCredential(w http.ResponseWriter, r *http.Request) {
	account, ok := h.account(w, r)
	if !ok {
		return
	}
	if !account.EmailVerified {
		http.Error(w, "verified email required", http.StatusForbidden)
		return
	}
	token, err := h.Service.Provision(r.Context(), account.ID)
	if err != nil {
		http.Error(w, "provisioning unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]string{"redemption_token": token})
}
func (h HTTPHandler) RedeemCredential(w http.ResponseWriter, r *http.Request) {
	account, ok := h.account(w, r)
	if !ok {
		return
	}
	if !account.EmailVerified {
		http.Error(w, "verified email required", http.StatusForbidden)
		return
	}
	defer r.Body.Close()
	var request struct {
		Token string `json:"redemption_token"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	credential, err := h.Service.Redeem(r.Context(), account.ID, strings.TrimSpace(request.Token))
	if err != nil {
		code := http.StatusServiceUnavailable
		if errors.Is(err, ErrInvalidRedemption) {
			code = http.StatusNotFound
		}
		http.Error(w, http.StatusText(code), code)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]string{"endpoint": credential.Endpoint, "region": credential.Region, "tenant_id": credential.TenantID, "bucket": credential.BucketName, "access_key_id": credential.AccessKeyID, "secret_key": credential.SecretKey})
}
func (h HTTPHandler) account(w http.ResponseWriter, r *http.Request) (accounts.Account, bool) {
	scheme, token, ok := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return accounts.Account{}, false
	}
	account, err := h.Accounts.Authenticate(r.Context(), strings.TrimSpace(token))
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return accounts.Account{}, false
	}
	return account, true
}
