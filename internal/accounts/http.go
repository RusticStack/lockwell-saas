package accounts

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

const maxAccountBody = 32 << 10

type HTTPHandler struct {
	Service Service
}

type credentialsRequest struct {
	Email        string `json:"email"`
	Password     string `json:"password"`
	TermsVersion string `json:"terms_version,omitempty"`
}

func (h HTTPHandler) Signup(w http.ResponseWriter, r *http.Request) {
	var request credentialsRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	account, token, err := h.Service.Signup(r.Context(), request.Email, request.Password, request.TermsVersion)
	h.writeAuthResult(w, account, token, err, http.StatusCreated)
}

func (h HTTPHandler) Login(w http.ResponseWriter, r *http.Request) {
	var request credentialsRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	account, token, err := h.Service.Login(r.Context(), request.Email, request.Password)
	h.writeAuthResult(w, account, token, err, http.StatusOK)
}

func (h HTTPHandler) writeAuthResult(w http.ResponseWriter, account Account, token string, err error, status int) {
	if err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, ErrInvalidCredentials) {
			code = http.StatusUnauthorized
		} else if errors.Is(err, ErrEmailExists) {
			code = http.StatusConflict
		}
		http.Error(w, http.StatusText(code), code)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"account_id": account.ID, "email": account.Email, "session_token": token})
}

func BearerToken(r *http.Request) string {
	scheme, token, ok := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAccountBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return false
	}
	return true
}
