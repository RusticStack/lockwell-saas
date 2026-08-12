package provisioning

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
	"time"
)

type memoryVault struct {
	mu     sync.Mutex
	values map[string][]byte
}

func (v *memoryVault) Put(_ context.Context, key string, value []byte) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.values == nil {
		v.values = map[string][]byte{}
	}
	ref := "vault://" + key
	v.values[ref] = append([]byte(nil), value...)
	return ref, nil
}
func (v *memoryVault) Get(_ context.Context, ref string) ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	value, ok := v.values[ref]
	if !ok {
		return nil, errors.New("missing secret")
	}
	return append([]byte(nil), value...), nil
}

type fakeCells struct {
	calls      int
	adminToken string
}

func (f *fakeCells) Provision(_ context.Context, _ Reservation, token string) (CreatedCredential, error) {
	f.calls++
	f.adminToken = token
	return CreatedCredential{AccessKeyID: "AKIA_TEST", SecretKey: "secret-once"}, nil
}

type fakeRepo struct {
	mu                   sync.Mutex
	r                    Reservation
	ready                bool
	secretRef, accessKey string
	redemptionID         string
	hash                 [32]byte
	expires              time.Time
	claimed, redeemed    bool
}

func (f *fakeRepo) Reserve(_ context.Context, account, plan string, _ time.Time) (Reservation, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.r.AccountID = account
	return f.r, f.ready, nil
}
func (f *fakeRepo) Complete(_ context.Context, _, access, ref string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.accessKey = access
	f.secretRef = ref
	f.ready = true
	return nil
}
func (*fakeRepo) Fail(context.Context, string, string, time.Time) error { return nil }
func (f *fakeRepo) CreateRedemption(_ context.Context, _ string, h [32]byte, expires, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.redemptionID = "redemption-1"
	f.hash = h
	f.expires = expires
	return nil
}
func (f *fakeRepo) ClaimRedemption(_ context.Context, account string, h [32]byte, now, _ time.Time) (string, Reservation, string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if account != f.r.AccountID || h != f.hash || f.claimed || f.redeemed || !now.Before(f.expires) {
		return "", Reservation{}, "", "", ErrInvalidRedemption
	}
	f.claimed = true
	return f.redemptionID, f.r, f.accessKey, f.secretRef, nil
}
func (f *fakeRepo) CompleteRedemption(_ context.Context, id string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id != f.redemptionID || !f.claimed || f.redeemed {
		return ErrInvalidRedemption
	}
	f.redeemed = true
	return nil
}
func (f *fakeRepo) ReleaseRedemption(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id != f.redemptionID {
		return ErrInvalidRedemption
	}
	f.claimed = false
	return nil
}

func TestProvisionAndRedeemCredentialExactlyOnce(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	repo := &fakeRepo{r: Reservation{ID: "provision-1", CellID: "cell-eu-1", Region: "fr-par", PublicEndpoint: "https://s3.example.test", AdminSecretRef: "vault://cell", TenantID: "acct_1", BucketName: "data"}}
	vault := &memoryVault{values: map[string][]byte{"vault://cell": []byte("admin-token")}}
	cells := &fakeCells{}
	svc := Service{Repo: repo, Cells: cells, Vault: vault, Now: func() time.Time { return now }}
	token, err := svc.Provision(context.Background(), "account-1", "starter")
	if err != nil || token == "" {
		t.Fatalf("token=%q err=%v", token, err)
	}
	if cells.calls != 1 || cells.adminToken != "admin-token" {
		t.Fatalf("provision calls=%d token=%q", cells.calls, cells.adminToken)
	}
	if repo.secretRef == "" || repo.secretRef == "secret-once" {
		t.Fatalf("repository secret reference=%q", repo.secretRef)
	}
	credential, err := svc.Redeem(context.Background(), "account-1", token)
	if err != nil {
		t.Fatal(err)
	}
	if credential.SecretKey != "secret-once" || credential.AccessKeyID != "AKIA_TEST" || credential.Endpoint != "https://s3.example.test" {
		t.Fatalf("credential=%#v", credential)
	}
	if _, err = svc.Redeem(context.Background(), "account-1", token); !errors.Is(err, ErrInvalidRedemption) {
		t.Fatalf("second redemption err=%v", err)
	}
	if _, err = svc.Redeem(context.Background(), "other-account", token); !errors.Is(err, ErrInvalidRedemption) {
		t.Fatalf("cross-account redemption err=%v", err)
	}
}

func TestRedemptionStoresOnlySHA256TokenHash(t *testing.T) {
	repo := &fakeRepo{r: Reservation{ID: "p", AdminSecretRef: "cell", Status: "ready"}, ready: true, secretRef: "tenant", accessKey: "key"}
	vault := &memoryVault{values: map[string][]byte{"tenant": []byte("secret")}}
	svc := Service{Repo: repo, Cells: &fakeCells{}, Vault: vault}
	token, err := svc.Provision(context.Background(), "a", "starter")
	if err != nil {
		t.Fatal(err)
	}
	if repo.hash != sha256.Sum256([]byte(token)) {
		t.Fatal("repository did not receive the token hash")
	}
}
