package usageingest

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
)

type HTTPHandler struct {
	Service Service
	Token   string
}

func (h HTTPHandler) Record(w http.ResponseWriter, r *http.Request) {
	if !authorized(r, h.Token) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var in Window
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		http.Error(w, "invalid usage window", http.StatusBadRequest)
		return
	}
	created, err := h.Service.Record(r.Context(), in)
	if err != nil {
		http.Error(w, "usage window rejected", http.StatusUnprocessableEntity)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"created": created})
}

func authorized(r *http.Request, want string) bool {
	scheme, token, ok := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || len(want) < 32 {
		return false
	}
	a := sha256.Sum256([]byte(token))
	b := sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}
