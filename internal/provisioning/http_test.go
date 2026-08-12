package provisioning

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RusticStack/lockwell-saas/internal/accounts"
)

type authStub struct {
	account accounts.Account
	err     error
}

func (a authStub) Authenticate(context.Context, string) (accounts.Account, error) {
	return a.account, a.err
}
func TestProvisioningHTTPRequiresVerifiedAccountAndReturnsOnceOnlyCredential(t *testing.T) {
	repo := &fakeRepo{r: Reservation{ID: "p", PlanCode: "starter", AdminSecretRef: "cell", PublicEndpoint: "https://s3.example", TenantID: "acct", BucketName: "data"}}
	vault := &memoryVault{values: map[string][]byte{"cell": []byte("admin")}}
	service := Service{Repo: repo, Cells: &fakeCells{}, Vault: vault, PlanQuotas: map[string]int64{"starter": 1 << 30}}
	handler := HTTPHandler{Accounts: authStub{account: accounts.Account{ID: "a", EmailVerified: true}}, Service: service}
	req := httptest.NewRequest(http.MethodPost, "/v1/provisioning/credentials", nil)
	req.Header.Set("Authorization", "Bearer session")
	response := httptest.NewRecorder()
	handler.RequestCredential(response, req)
	if response.Code != 200 {
		t.Fatalf("request status=%d body=%s", response.Code, response.Body.String())
	}
	var token map[string]string
	_ = json.Unmarshal(response.Body.Bytes(), &token)
	requestBody := `{"redemption_token":"` + token["redemption_token"] + `"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/provisioning/redeem", strings.NewReader(requestBody))
	req.Header.Set("Authorization", "Bearer session")
	response = httptest.NewRecorder()
	handler.RedeemCredential(response, req)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "secret-once") {
		t.Fatalf("redeem status=%d body=%s", response.Code, response.Body.String())
	}
	req = httptest.NewRequest(http.MethodPost, "/v1/provisioning/redeem", strings.NewReader(requestBody))
	req.Header.Set("Authorization", "Bearer session")
	response = httptest.NewRecorder()
	handler.RedeemCredential(response, req)
	if response.Code != http.StatusNotFound {
		t.Fatalf("replay status=%d", response.Code)
	}
}
func TestProvisioningHTTPDeniesUnverifiedAccount(t *testing.T) {
	handler := HTTPHandler{Accounts: authStub{account: accounts.Account{ID: "a"}}}
	req := httptest.NewRequest(http.MethodPost, "/v1/provisioning/credentials", nil)
	req.Header.Set("Authorization", "Bearer session")
	response := httptest.NewRecorder()
	handler.RequestCredential(response, req)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d", response.Code)
	}
}
