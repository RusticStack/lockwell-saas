package billing

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/RusticStack/lockwell-saas/internal/accounts"
)

type HTTPHandler struct {
	Service Service
}

func (h HTTPHandler) Checkout(w http.ResponseWriter, r *http.Request) {
	var request struct {
		PlanCode string `json:"plan_code"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	session, err := h.Service.Checkout(r.Context(), accounts.BearerToken(r), request.PlanCode, r.Header.Get("Idempotency-Key"))
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeRedirectJSON(w, session.ID, session.URL)
}

func (h HTTPHandler) Portal(w http.ResponseWriter, r *http.Request) {
	session, err := h.Service.Portal(r.Context(), accounts.BearerToken(r), r.Header.Get("Idempotency-Key"))
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeRedirectJSON(w, session.ID, session.URL)
}

func (h HTTPHandler) writeError(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	if errors.Is(err, accounts.ErrInvalidCredentials) {
		status = http.StatusUnauthorized
	} else if errors.Is(err, ErrUnsupportedPlan) {
		status = http.StatusBadRequest
	} else if errors.Is(err, ErrEmailUnverified) {
		status = http.StatusForbidden
	}
	http.Error(w, http.StatusText(status), status)
}

func writeRedirectJSON(w http.ResponseWriter, id, targetURL string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"id": id, "url": targetURL})
}
