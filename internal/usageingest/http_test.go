package usageingest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPHandlerRequiresTokenAndDoesNotExposeRejectionDetails(t *testing.T) {
	window := testWindow()
	body, _ := json.Marshal(window)
	h := HTTPHandler{Service: testService(&recordingRepo{}), Token: "01234567890123456789012345678901"}
	for name, tc := range map[string]struct {
		token string
		want  int
	}{
		"missing": {want: http.StatusUnauthorized},
		"wrong":   {token: "wrong", want: http.StatusUnauthorized},
		"valid":   {token: "01234567890123456789012345678901", want: http.StatusOK},
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/internal/v1/usage-windows", bytes.NewReader(body))
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			res := httptest.NewRecorder()
			h.Record(res, req)
			if res.Code != tc.want {
				t.Fatalf("status=%d body=%q", res.Code, res.Body.String())
			}
		})
	}
}
